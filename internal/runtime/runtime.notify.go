package runtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/presence"
	"github.com/admirable-oss/hive/internal/task"
)

// Notifications for teams (RFC 0007): the daemon decides who is told that
// work needs someone, the work's owner or, while they are away, the
// [access] notify_fallback, and clients show only what is addressed to
// their person.

// EventNotify says work needs someone: a Notification.
const EventNotify = "notify"

// Notification kinds.
const (
	NotifyBlocked  = "blocked"  // an agent waits for a decision
	NotifyDone     = "done"     // an agent finished
	NotifyApproval = "approval" // a task waits for approval
	NotifyFailed   = "failed"   // a task failed
)

// Fallback entries besides members' names.
const (
	fallbackOwner     = "owner"
	fallbackOperators = "operators"
)

// Notification is who is told what.
type Notification struct {
	Kind        string         `json:"kind"`
	Environment string         `json:"environment,omitempty"`
	Agent       *agent.Agent   `json:"agent,omitempty"`
	Task        *task.TaskView `json:"task,omitempty"`
	// Owner is who the work belongs to; absent, the runtime's own user.
	Owner *presence.Person `json:"owner,omitempty"`
	// To is who is told: the owner, or the fallback while the owner is
	// away. It is never empty.
	To []presence.Person `json:"to"`
	// Fallback says the owner was away.
	Fallback bool `json:"fallback,omitempty"`
}

// For reports whether n is addressed to the person with ID id.
func (n Notification) For(id string) bool {
	return slices.ContainsFunc(n.To, func(p presence.Person) bool { return p.ID == id })
}

// notifier publishes notify events.
type notifier struct {
	bus      *event.Bus
	agents   *agent.Service
	store    *access.Store
	owner    presence.Person
	fallback []string
	// ownerHere and connections say who has a client connected now.
	ownerHere   func() bool
	connections func() map[string]int
	log         *slog.Logger
}

// run follows agents and tasks until ctx ends.
func (n *notifier) run(ctx context.Context) {
	sub := n.bus.Subscribe(event.DefaultBuffer, agent.EventState, task.EventApproval, task.EventTask)
	defer sub.Close()
	// What was already so when the daemon started is not news.
	states := map[string]agent.State{}
	if list, err := n.agents.List(ctx, false); err == nil {
		for _, a := range list {
			states[a.ID] = a.State
		}
	}
	failed := map[string]int{} // plan/task → the attempt told about
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		switch ev.Type {
		case agent.EventState:
			var a agent.Agent
			if json.Unmarshal(ev.Data, &a) != nil || a.Kind == "" {
				continue
			}
			prev, known := states[a.ID]
			states[a.ID] = a.State
			if a.State == agent.StateExited {
				delete(states, a.ID)
			}
			if known && prev == a.State || a.State != agent.StateBlocked && a.State != agent.StateDone {
				continue
			}
			n.publish(Notification{Kind: string(a.State), Environment: a.EnvironmentID, Agent: &a, Owner: a.Owner})
		case task.EventApproval:
			var v task.TaskView
			if json.Unmarshal(ev.Data, &v) == nil {
				n.publish(Notification{Kind: NotifyApproval, Environment: v.Environment, Task: &v, Owner: v.Owner})
			}
		case task.EventTask:
			var v task.TaskView
			if json.Unmarshal(ev.Data, &v) != nil || v.State != task.StateFailed {
				continue
			}
			key := v.PlanID + "/" + v.ID
			if at, ok := failed[key]; ok && at == v.Attempt {
				continue // told already: a later revision of the same failure
			}
			failed[key] = v.Attempt
			n.publish(Notification{Kind: NotifyFailed, Environment: v.Environment, Task: &v, Owner: v.Owner})
		}
	}
}

func (n *notifier) publish(note Notification) {
	note.To, note.Fallback = n.recipients(note.Owner, note.Environment)
	n.log.Debug("notify", "kind", note.Kind, "environment", note.Environment, "to", names(note.To), "fallback", note.Fallback)
	n.bus.Publish(EventNotify, note)
}

// recipients is who hears about work in env that owner owns: the owner
// when they are here and may still see it, else the fallback (and the
// runtime's own user when the fallback names nobody who may see it).
func (n *notifier) recipients(owner *presence.Person, env string) ([]presence.Person, bool) {
	if owner == nil {
		if n.ownerHere() {
			return []presence.Person{n.owner}, false
		}
	} else if m, ok := n.member(owner.ID, env, access.RoleObserver); ok && n.connections()[m.ID] > 0 {
		return []presence.Person{*owner}, false
	}
	var to []presence.Person
	add := func(p presence.Person) {
		if !slices.ContainsFunc(to, func(q presence.Person) bool { return q.ID == p.ID }) {
			to = append(to, p)
		}
	}
	for _, f := range n.fallback {
		switch f {
		case fallbackOwner:
			add(n.owner)
		case fallbackOperators:
			list, _ := n.store.List()
			slices.SortFunc(list, func(a, b access.Member) int { return strings.Compare(a.Name, b.Name) })
			for _, m := range list {
				if _, ok := n.member(m.ID, env, access.RoleOperator); ok {
					add(presence.Person{ID: m.ID, Name: m.Name})
				}
			}
		default:
			if m, err := n.store.Get(f); err == nil {
				if m, ok := n.member(m.ID, env, access.RoleObserver); ok {
					add(presence.Person{ID: m.ID, Name: m.Name})
				}
			}
		}
	}
	if len(to) == 0 {
		add(n.owner)
	}
	return to, true
}

// names lists people's names, for logs.
func names(ps []presence.Person) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

// member returns member id when they are active and hold at least role in
// env (in every environment when env is "").
func (n *notifier) member(id, env string, role access.Role) (access.Member, bool) {
	m, err := n.store.Check(id)
	if err != nil {
		return access.Member{}, false
	}
	if env == "" {
		env = access.AllEnvironments
	}
	return m, m.RoleIn(env).AtLeast(role)
}
