package task

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Scheduling is two passes over the plans, both under the service's lock
// and both without I/O:
//
//   - settle moves tasks along their dependencies: waiting tasks whose
//     needs succeeded become ready (queued, or approval first), and tasks
//     whose needs did not are skipped. Plans keep their tasks in an order
//     where needs come first, so one pass settles a whole plan.
//   - place picks queued tasks to start, within every limit: the plan's
//     max_parallel and kinds, the daemon's per-kind caps, and the
//     machines' capacity and labels. Older plans go first.

// placement is a decision about a queued task: start it on machine, or,
// with reason, fail it because no machine could ever run it.
type placement struct {
	plan    *Plan
	index   int
	machine string
	reason  string
}

// place decides which queued tasks start now and where. plans must be in
// the order they were submitted. kinds caps running tasks per agent kind
// across plans.
func place(plans []*Plan, machines []Machine, kinds map[string]int) []placement {
	machineLoad := make(map[string]int, len(machines))
	kindLoad := make(map[string]int, len(kinds))
	queued := false
	for _, p := range plans {
		for i := range p.Tasks {
			t := &p.Tasks[i]
			switch {
			case t.State.Active():
				machineLoad[t.Machine]++
				kindLoad[t.Spec.Kind]++
			case t.State == StateQueued:
				queued = true
			}
		}
	}
	if !queued || machines == nil {
		return nil // nothing to place, or the machines are not known
	}
	var out []placement
	for _, p := range plans {
		if p.State.Finished() || p.Cancelled {
			continue
		}
		planLoad, planKinds := 0, make(map[string]int, len(p.Kinds))
		for i := range p.Tasks {
			if t := &p.Tasks[i]; t.State.Active() {
				planLoad++
				planKinds[t.Spec.Kind]++
			}
		}
		for i := range p.Tasks {
			t := &p.Tasks[i]
			if t.State != StateQueued {
				continue
			}
			if p.MaxParallel > 0 && planLoad >= p.MaxParallel {
				break
			}
			kind := t.Spec.Kind
			if n, ok := p.Kinds[kind]; ok && planKinds[kind] >= n {
				continue
			}
			if n, ok := kinds[kind]; ok && kindLoad[kind] >= n {
				continue
			}
			m, reason := choose(t.Spec, machines, machineLoad)
			if reason != "" {
				out = append(out, placement{plan: p, index: i, reason: reason})
				continue
			}
			if m == "" {
				continue // every machine that fits is full
			}
			out = append(out, placement{plan: p, index: i, machine: m})
			machineLoad[m]++
			kindLoad[kind]++
			planLoad++
			planKinds[kind]++
		}
	}
	return out
}

// choose picks the machine for s with the most free slots (the earliest
// listed on a tie: this machine first). It returns "" when every machine
// that fits is full, and a reason when no machine fits at all.
func choose(s Spec, machines []Machine, load map[string]int) (name, reason string) {
	best, bestFree, fits := "", 0, false
	for _, m := range machines {
		if s.Machine != "" && m.Name != s.Machine || !m.Has(s.Labels) {
			continue
		}
		fits = true
		if free := m.Capacity - load[m.Name]; free > bestFree {
			best, bestFree = m.Name, free
		}
	}
	switch {
	case fits:
		return best, ""
	case s.Machine != "" && len(s.Labels) > 0:
		return "", fmt.Sprintf("no machine called %q with labels %s", s.Machine, strings.Join(s.Labels, ", "))
	case s.Machine != "":
		return "", fmt.Sprintf("no machine called %q (see hive machine list)", s.Machine)
	}
	return "", "no machine has the labels " + strings.Join(s.Labels, ", ")
}

// settle moves p's tasks along their dependencies and updates p's state.
// It reports the tasks it changed, and appends to approvals the tasks that
// now wait for a person.
func settle(p *Plan, now time.Time) (changed []int) {
	if p.State.Finished() {
		return nil
	}
	states := make(map[string]State, len(p.Tasks))
	for i := range p.Tasks {
		t := &p.Tasks[i]
		if t.State == StateWaiting || (p.Cancelled && !t.State.Finished() && !t.State.Active()) {
			next, reason := ready(t, states, p.Cancelled)
			if next != t.State {
				t.State, t.Reason = next, reason
				switch next {
				case StateApproval:
					t.ReadyAt = now
					t.Approval = &Approval{RequestedAt: now}
					t.Log = appendLog(t.Log, now, "waiting for approval")
				case StateQueued:
					t.ReadyAt = now
				case StateSkipped, StateCancelled:
					t.EndedAt = now
					t.Log = appendLog(t.Log, now, reason)
				}
				changed = append(changed, i)
			}
		}
		states[t.Spec.ID] = t.State
	}
	if next := planState(p); next != p.State {
		p.State = next
		if next.Finished() {
			p.EndedAt = now
		}
	}
	return changed
}

// ready decides what a waiting task becomes, given the states of the
// tasks before it.
func ready(t *Task, states map[string]State, cancelled bool) (State, string) {
	if cancelled {
		return StateCancelled, "the plan was cancelled"
	}
	// A need that will never succeed decides at once, even while others
	// are still unfinished.
	waiting := false
	for _, n := range t.Spec.Needs {
		switch s := states[n]; {
		case s == StateSucceeded:
		case s.Finished():
			return StateSkipped, fmt.Sprintf("it needs %s, which %s", n, verb(s))
		default:
			waiting = true
		}
	}
	if waiting {
		return StateWaiting, ""
	}
	if t.Spec.Approve && (t.Approval == nil || !t.Approval.Approved) {
		return StateApproval, "waiting for approval"
	}
	return StateQueued, ""
}

func verb(s State) string {
	switch s {
	case StateFailed:
		return "failed"
	case StateCancelled:
		return "was cancelled"
	case StateSkipped:
		return "was skipped"
	}
	return string(s)
}

// planState is the plan's state given its tasks: running while any task
// is unfinished, then succeeded only if every task succeeded.
func planState(p *Plan) PlanState {
	ok := true
	for i := range p.Tasks {
		s := p.Tasks[i].State
		if !s.Finished() {
			return PlanRunning
		}
		ok = ok && s == StateSucceeded
	}
	switch {
	case ok:
		return PlanSucceeded
	case p.Cancelled:
		return PlanCancelled
	}
	return PlanFailed
}

// dependents returns the indexes of the tasks that need the task at i,
// directly or not. Tasks come after what they need, so one forward pass
// finds them all.
func dependents(p *Plan, i int) []int {
	affected := map[string]bool{p.Tasks[i].Spec.ID: true}
	var out []int
	for j := i + 1; j < len(p.Tasks); j++ {
		if slices.ContainsFunc(p.Tasks[j].Spec.Needs, func(n string) bool { return affected[n] }) {
			affected[p.Tasks[j].Spec.ID] = true
			out = append(out, j)
		}
	}
	return out
}

// appendLog adds an entry, dropping the oldest beyond MaxLog. It never
// writes into an array a snapshot may share: past the cap it copies.
func appendLog(log []LogEntry, now time.Time, msg string) []LogEntry {
	e := LogEntry{Time: now, Message: msg}
	if len(log) < MaxLog {
		return append(log, e)
	}
	out := make([]LogEntry, MaxLog)
	copy(out, log[len(log)-MaxLog+1:])
	out[MaxLog-1] = e
	return out
}
