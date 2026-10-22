package task_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/task"
)

// memStore keeps plans in memory, as JSON-free copies.
type memStore struct {
	mu    sync.Mutex
	plans map[string]task.Plan
	puts  int
}

func newMemStore() *memStore { return &memStore{plans: map[string]task.Plan{}} }

func (m *memStore) List(context.Context) ([]task.Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Collect(maps.Values(m.plans)), nil
}

func (m *memStore) Put(_ context.Context, p *task.Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *p
	c.Tasks = slices.Clone(p.Tasks)
	m.plans[p.ID] = c
	m.puts++
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.plans, id)
	return nil
}

func (m *memStore) get(id string) (task.Plan, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plans[id]
	return p, ok
}

// recorder records published events.
type recorder struct {
	mu     sync.Mutex
	events []string
	data   []any
}

func (r *recorder) Publish(typ string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, typ)
	r.data = append(r.data, data)
}

func (r *recorder) count(typ string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == typ {
			n++
		}
	}
	return n
}

// behaviour is what a fake executor does with an attempt.
type behaviour func(ctx context.Context, a task.Attempt, progress func(task.Progress)) task.Outcome

// fakeExec is a machine's executor that records what runs on it.
type fakeExec struct {
	name string
	act  behaviour

	mu       sync.Mutex
	running  int
	peak     int
	attempts []task.Attempt
	kinds    map[string]int
	kindPeak map[string]int
}

func (e *fakeExec) Execute(ctx context.Context, a task.Attempt, progress func(task.Progress)) task.Outcome {
	e.mu.Lock()
	e.running++
	e.peak = max(e.peak, e.running)
	e.attempts = append(e.attempts, a)
	if e.kinds == nil {
		e.kinds, e.kindPeak = map[string]int{}, map[string]int{}
	}
	e.kinds[a.Task.Spec.Kind]++
	e.kindPeak[a.Task.Spec.Kind] = max(e.kindPeak[a.Task.Spec.Kind], e.kinds[a.Task.Spec.Kind])
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running--
		e.kinds[a.Task.Spec.Kind]--
		e.mu.Unlock()
	}()
	e.mu.Lock()
	act := e.act
	e.mu.Unlock()
	return act(ctx, a, progress)
}

// setAct changes what the executor does with later attempts.
func (e *fakeExec) setAct(act behaviour) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.act = act
}

func (e *fakeExec) snapshot() (peak int, attempts []task.Attempt) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.peak, slices.Clone(e.attempts)
}

// fakeMachines is a fixed set of machines, each with an executor.
type fakeMachines struct {
	mu    sync.Mutex
	list  []task.Machine
	execs map[string]*fakeExec
}

func newMachines(act behaviour, ms ...task.Machine) *fakeMachines {
	f := &fakeMachines{list: ms, execs: map[string]*fakeExec{}}
	for _, m := range ms {
		f.execs[m.Name] = &fakeExec{name: m.Name, act: act}
	}
	return f
}

func (f *fakeMachines) Machines(context.Context) ([]task.Machine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.list), nil
}

func (f *fakeMachines) Executor(name string) (task.Executor, error) {
	if e := f.execs[name]; e != nil {
		return e, nil
	}
	return nil, fmt.Errorf("%w: %s", task.ErrUnreachable, name)
}

// work is a behaviour that runs an agent for d, then succeeds, unless the
// task's ID is in fail. A cancelled attempt is cancelled; one interrupted
// by the daemon stopping returns no state.
func work(d time.Duration, fail ...string) behaviour {
	return func(ctx context.Context, a task.Attempt, progress func(task.Progress)) task.Outcome {
		progress(task.Progress{AgentID: "agent-" + a.Task.Spec.ID, Phase: task.PhasePrompted, Log: "prompted"})
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return interrupted(ctx)
		case <-t.C:
		}
		if slices.Contains(fail, a.Task.Spec.ID) {
			return task.Outcome{State: task.StateFailed, Reason: "tests fail", Result: &task.Result{Outcome: "failed"}}
		}
		return task.Outcome{State: task.StateSucceeded, Result: &task.Result{Outcome: "met", Summary: "did " + a.Task.Spec.ID, Changed: []string{a.Task.Spec.ID + ".go"}}}
	}
}

// hold is a behaviour that works until the attempt's context ends.
func hold(started chan<- string) behaviour {
	return func(ctx context.Context, a task.Attempt, progress func(task.Progress)) task.Outcome {
		progress(task.Progress{AgentID: "agent-" + a.Task.Spec.ID, Phase: task.PhasePrompted})
		if started != nil {
			started <- a.Task.Spec.ID
		}
		<-ctx.Done()
		return interrupted(ctx)
	}
}

func interrupted(ctx context.Context) task.Outcome {
	if errors.Is(context.Cause(ctx), task.ErrCancelled) {
		return task.Outcome{State: task.StateCancelled, Reason: context.Cause(ctx).Error(), Result: &task.Result{Outcome: "cancelled"}}
	}
	return task.Outcome{}
}

// harness runs a service until the test ends (or stop is called).
type harness struct {
	t      *testing.T
	svc    *task.Service
	store  *memStore
	events *recorder
	stop   func()
}

func run(t *testing.T, cfg task.Config, store *memStore, machines task.Machines) *harness {
	t.Helper()
	rec := &recorder{}
	svc := task.NewService(cfg, store, machines, rec)
	if err := svc.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return &harness{t: t, svc: svc, store: store, events: rec, stop: stop}
}

// wait waits for the plan until it is in a state or the deadline passes.
func (h *harness) wait(id string, until task.WaitUntil) task.WaitResult {
	h.t.Helper()
	res, err := h.svc.Wait(h.t.Context(), task.WaitRequest{ID: id, Until: until, Timeout: 20 * time.Second})
	if err != nil {
		h.t.Fatal(err)
	}
	if res.Outcome == task.OutcomeTimeout {
		h.t.Fatalf("plan %s: wait %s timed out: %+v", id, until, res.Plan)
	}
	return res
}

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func states(p task.Plan) map[string]task.State {
	out := map[string]task.State{}
	for _, t := range p.Tasks {
		out[t.Spec.ID] = t.State
	}
	return out
}

func spec(id string, needs ...string) task.Spec {
	return task.Spec{ID: id, Prompt: "do " + id, Kind: "claude", EnvironmentID: "env", Needs: needs}
}
