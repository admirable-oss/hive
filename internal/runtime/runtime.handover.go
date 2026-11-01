package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/presence"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/task"
)

// Handing work over (RFC 0007): agents, plans and tasks belong to someone,
// the member who started them or the runtime's own user, and are given to
// someone else with agent.handover and task.assign.

// EventAgentHandover says an agent changed hands: AgentHandedOver.
const EventAgentHandover = "agent.handover"

// AgentHandedOver is agent.handover's result and event: the agent with its
// new owner, and who had it.
type AgentHandedOver struct {
	Agent agent.Agent `json:"agent"`
	// From is who it belonged to; absent, the runtime's own user.
	From *presence.Person `json:"from,omitempty"`
}

type handoverParams struct {
	ID string `json:"id" desc:"the agent's ID, or its pane's"`
	// To is the member who gets it, by name or ID, or "owner" for the
	// runtime's own user.
	To string `json:"to" desc:"who gets it: a member's name or ID, or owner for the runtime's own user"`
}

type assignParams struct {
	ID   string `json:"id" desc:"the plan's ID, or a unique prefix of it"`
	Task string `json:"task,omitempty" desc:"the task to assign; without it, the plan and its unfinished tasks"`
	To   string `json:"to" desc:"who gets it: a member's name or ID, or owner for the runtime's own user"`
}

// handover gives agents and tasks to other people.
type handover struct {
	agents  *agent.Service
	procs   process.Service
	tasks   *task.Service
	reg     *presence.Registry
	members *access.Store
	owner   presence.Person
	bus     *event.Bus
}

var (
	errNotOwned     = errors.New("an operator hands over only what is theirs")
	errNoRecipients = errors.New("give to: a member's name, or owner")
)

// recipient resolves to: nil for the runtime's own user, else an active
// member who is an operator or owner in every one of envs (in all of them
// when envs is nil: work placed nowhere yet).
func (h *handover) recipient(to string, envs []string) (*presence.Person, error) {
	switch to {
	case "":
		return nil, protocol.NewError(protocol.ErrorCodeInvalidParams, errNoRecipients)
	case presence.OwnerID, h.owner.Name:
		return nil, nil
	}
	m, err := h.members.Get(to)
	if err == nil {
		m, err = h.members.Check(m.ID)
	}
	if err != nil {
		return nil, protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf("no active member %s", to))
	}
	if envs == nil {
		envs = []string{access.AllEnvironments}
	}
	for _, env := range envs {
		if !m.RoleIn(env).AtLeast(access.RoleOperator) {
			return nil, protocol.NewError(protocol.ErrorCodeInvalidParams, fmt.Errorf("%s is not an operator in %s: work goes to operators and owners", m.Name, env))
		}
	}
	return &presence.Person{ID: m.ID, Name: m.Name}, nil
}

// mayGive checks that the caller may give away what owner owns in envs:
// the runtime's own user and an environment's owners give anything, an
// operator only what is theirs.
func mayGive(ctx context.Context, owner *presence.Person, envs []string) error {
	c, ok := access.CallerOf(ctx)
	if !ok {
		return nil
	}
	if envs == nil {
		envs = []string{access.AllEnvironments}
	}
	if !slices.ContainsFunc(envs, func(e string) bool { return c.Member.RoleIn(e) != access.RoleOwner }) {
		return nil
	}
	if owner != nil && owner.ID == c.Member.ID {
		return nil
	}
	return protocol.NewError(protocol.ErrorCodePermissionDenied, errNotOwned)
}

func (h *handover) Handover(ctx context.Context, p handoverParams) (AgentHandedOver, error) {
	a, err := h.agents.Get(ctx, p.ID)
	if err != nil {
		return AgentHandedOver{}, agentOpsError(err)
	}
	envs := []string{a.EnvironmentID}
	to, err := h.recipient(p.To, envs)
	if err != nil {
		return AgentHandedOver{}, err
	}
	if err := mayGive(ctx, a.Owner, envs); err != nil {
		return AgentHandedOver{}, err
	}
	// The task it works on goes with it.
	if plan, t := h.tasks.TaskOf(ctx, a.ID); plan != "" {
		if _, err := h.tasks.Assign(ctx, task.AssignRequest{ID: plan, Task: t, To: to, By: personOf(ctx, h.owner).Name}); err != nil {
			return AgentHandedOver{}, err
		}
	}
	return h.give(ctx, a, to)
}

// give makes to the owner of agent a, and of its pane's input if someone
// holds it.
func (h *handover) give(ctx context.Context, a agent.Agent, to *presence.Person) (AgentHandedOver, error) {
	if _, err := h.procs.SetOwner(ctx, a.ID, to); err != nil {
		return AgentHandedOver{}, err
	}
	who := h.owner
	if to != nil {
		who = *to
	}
	if holder, held := h.reg.Holder(a.ID); held && holder.ID != who.ID {
		if _, err := h.reg.Claim(a.ID, a.PaneID, a.EnvironmentID, who, true); err != nil {
			return AgentHandedOver{}, fmt.Errorf("agent %s is %s's, but its pane's input stays %s's: %w", a.ID, who.Name, holder.Name, err)
		}
	}
	res := AgentHandedOver{From: a.Owner}
	res.Agent, _ = h.agents.Get(ctx, a.ID)
	if res.Agent.ID == "" {
		res.Agent, res.Agent.Owner = a, to
	}
	event.PublishContext(ctx, h.bus, EventAgentHandover, res)
	return res, nil
}

func (h *handover) Assign(ctx context.Context, p assignParams) (task.Plan, error) {
	pl, err := h.tasks.Get(ctx, p.ID)
	if err != nil {
		return task.Plan{}, task.WireError(err)
	}
	var (
		owners []*presence.Person
		envs   []string
		placed = true
	)
	if p.Task == "" {
		owners = append(owners, pl.Owner)
	}
	for _, t := range pl.Tasks {
		if p.Task != "" && t.Spec.ID != p.Task || p.Task == "" && t.State.Finished() {
			continue
		}
		owners = append(owners, t.Owner)
		env := taskEnvironment(t)
		if env == "" {
			placed = false
		}
		envs = append(envs, env)
	}
	if p.Task != "" && len(owners) == 0 {
		return task.Plan{}, protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf("plan %s has no task %q", pl.ID, p.Task))
	}
	if !placed {
		envs = nil
	}
	to, err := h.recipient(p.To, envs)
	if err != nil {
		return task.Plan{}, err
	}
	for _, o := range owners {
		if err := mayGive(ctx, o, envs); err != nil {
			return task.Plan{}, err
		}
	}
	res, err := h.tasks.Assign(ctx, task.AssignRequest{ID: pl.ID, Task: p.Task, To: to, By: personOf(ctx, h.owner).Name})
	if err != nil {
		return task.Plan{}, task.WireError(err)
	}
	// The agents working on its tasks go with them; one left behind is
	// said, as the plan is given already.
	var left []error
	for _, id := range res.Agents {
		a, err := h.agents.Get(ctx, id)
		if errors.Is(err, agent.ErrNotFound) {
			continue // it ended meanwhile
		}
		if err == nil {
			_, err = h.give(ctx, a, to)
		}
		if err != nil {
			left = append(left, fmt.Errorf("agent %s: %w", id, err))
		}
	}
	if len(left) > 0 {
		return task.Plan{}, protocol.NewError(protocol.ErrorCodeInternal, fmt.Errorf("plan %s was assigned, but not all its agents went with it: %w", pl.ID, errors.Join(left...)))
	}
	return res.Plan, nil
}

func registerHandover(r *protocol.Router, h *handover) {
	r.MustRegister("agent.handover", protocol.Method(h.Handover))
	r.MustRegister("task.assign", protocol.Method(h.Assign))
}
