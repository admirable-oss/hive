package task

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Machines is where tasks may run: this machine and the saved ones.
type Machines interface {
	// Machines lists the machines tasks may run on, this one first. On an
	// error it still returns what it can (this machine at least).
	Machines(ctx context.Context) ([]Machine, error)
	// Executor returns what runs attempts on the machine called name.
	Executor(name string) (Executor, error)
}

// Events is where the service publishes its changes (task.*).
type Events interface {
	Publish(typ string, data any)
}

// Config tunes the service; zero values are the defaults.
type Config struct {
	Logger *slog.Logger
	Now    func() time.Time
	// Keep is how many finished plans are kept, on disk and in memory
	// (default 100). The oldest go first.
	Keep int
	// Kinds caps the tasks running at once per agent kind, across plans.
	Kinds map[string]int
	// SaveDelay gathers changes made close together into one write
	// (default 100ms).
	SaveDelay time.Duration
	// Rescan is how often queued tasks are looked at again without a
	// change to wake the scheduler, so machines that gain capacity are
	// used (default 5 seconds).
	Rescan time.Duration
	// BranchName names an isolated task's branch when its plan names
	// none; nil uses hive/<plan>/<task>.
	BranchName func(plan, task, kind string) (string, error)
	// Hold says why a queued task must not start yet (a budget over its
	// limit, say); "" lets it start. It is called under the service's
	// lock, so it must not call the service.
	Hold func(p *Plan, t *Task) string
}

// DefaultKeep is how many finished plans are kept by default.
const DefaultKeep = 100

// Service runs plans. It is the one owner of their state: every change
// happens under its lock, in memory, and is then saved by one writer and
// published in revision order. Attempts run on their own goroutines,
// which the service starts, cancels and joins.
type Service struct {
	cfg      Config
	log      *slog.Logger
	store    Store
	machines Machines
	events   Events

	wake chan struct{} // the scheduler has work: a task is ready or a slot freed
	kick chan struct{} // the saver has work

	// loaded is closed once the saved plans are read; the API waits for it.
	loaded   chan struct{}
	loadOnce sync.Once
	loadErr  error

	mu       sync.Mutex
	plans    map[string]*Plan
	order    []*Plan // by submission
	attempts map[taskKey]*attempt
	resume   map[taskKey]bool // tasks that ran when the daemon stopped
	dirty    map[string]bool
	removed  []string
	// changed holds, per plan with waiters, a channel closed at its next
	// revision: a change wakes only that plan's waiters.
	changed map[string]chan struct{}
	running sync.WaitGroup
}

type taskKey struct{ plan, task string }

// attempt is a running attempt: its number, and how to cancel it.
type attempt struct {
	n      int
	cancel context.CancelCauseFunc
}

// NewService returns a service over store, running tasks on machines.
func NewService(cfg Config, store Store, machines Machines, events Events) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Keep <= 0 {
		cfg.Keep = DefaultKeep
	}
	if cfg.SaveDelay <= 0 {
		cfg.SaveDelay = 100 * time.Millisecond
	}
	if cfg.Rescan <= 0 {
		cfg.Rescan = 5 * time.Second
	}
	return &Service{
		cfg: cfg, log: logging.OrDiscard(cfg.Logger), store: store, machines: machines, events: events,
		wake: make(chan struct{}, 1), kick: make(chan struct{}, 1), loaded: make(chan struct{}),
		plans: map[string]*Plan{}, attempts: map[taskKey]*attempt{}, resume: map[taskKey]bool{},
		dirty: map[string]bool{}, changed: map[string]chan struct{}{},
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Load reads the saved plans, once; Run calls it first. Tasks that were
// running when the daemon stopped are resumed once Run starts: their
// agents kept working.
func (s *Service) Load(ctx context.Context) error {
	s.loadOnce.Do(func() {
		s.loadErr = s.load(ctx)
		close(s.loaded)
	})
	return s.loadErr
}

// ready waits until the saved plans are read, so no caller sees a plan
// list without them.
func (s *Service) ready(ctx context.Context) error {
	select {
	case <-s.loaded:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) load(ctx context.Context) error {
	plans, err := s.store.List(ctx)
	if err != nil {
		s.log.Warn("some plans could not be loaded", "err", err)
	}
	slices.SortFunc(plans, func(a, b Plan) int { return a.CreatedAt.Compare(b.CreatedAt) })
	s.mu.Lock()
	defer s.mu.Unlock()
	resumed := 0
	for i := range plans {
		p := &plans[i]
		if s.plans[p.ID] != nil {
			continue
		}
		s.plans[p.ID] = p
		s.order = append(s.order, p)
		for j := range p.Tasks {
			if t := &p.Tasks[j]; t.State.Active() {
				s.resume[taskKey{p.ID, t.Spec.ID}] = true
				resumed++
			}
		}
	}
	s.pruneLocked()
	if len(plans) > 0 {
		s.log.Info("plans loaded", "plans", len(plans), "resumed", resumed)
	}
	signal(s.wake)
	return err
}

// Run schedules tasks until ctx ends. Running attempts are then
// interrupted, not cancelled: their agents keep working, and the next
// daemon resumes them. Run returns once every attempt has returned and
// the plans are saved.
func (s *Service) Run(ctx context.Context) {
	_ = s.Load(ctx) // what could not be read is logged; the rest runs
	saveCtx, stopSaving := context.WithCancel(context.WithoutCancel(ctx))
	saved := make(chan struct{})
	go func() {
		defer close(saved)
		s.saveLoop(saveCtx)
	}()

	rescan := time.NewTicker(s.cfg.Rescan)
	defer rescan.Stop()
	budget := time.NewTimer(time.Hour)
	defer budget.Stop()
	m := &machineCache{s: s}
	for {
		next := s.schedule(ctx, m)
		budget.Stop()
		if !next.IsZero() {
			budget.Reset(max(next.Sub(s.cfg.Now()), 0))
		}
		select {
		case <-ctx.Done():
			s.running.Wait()
			stopSaving()
			<-saved
			return
		case <-s.wake:
		case <-rescan.C:
		case <-budget.C:
		}
	}
}

// machineCache reads the machines at most once a second, and only when a
// task waits for one. Only Run's goroutine uses it.
type machineCache struct {
	s       *Service
	list    []Machine
	at      time.Time
	lastErr string
}

func (c *machineCache) get(ctx context.Context) []Machine {
	now := c.s.cfg.Now()
	if c.list != nil && now.Sub(c.at) < time.Second {
		return c.list
	}
	list, err := c.s.machines.Machines(ctx)
	if err != nil {
		if msg := err.Error(); msg != c.lastErr {
			c.s.log.Warn("reading the machines tasks may run on", "err", err)
			c.lastErr = msg
		}
	} else {
		c.lastErr = ""
	}
	if len(list) > 0 || c.list == nil {
		c.list = list
	}
	c.at = now
	return c.list
}

// start is an attempt to launch once the lock is released.
type start struct {
	key     taskKey
	machine string
	ctx     context.Context
	attempt Attempt
}

// schedule enforces plan budgets, resumes what the last daemon left
// running, and starts what may start. It returns when the next plan budget
// runs out (zero for none).
func (s *Service) schedule(ctx context.Context, mc *machineCache) time.Time {
	if ctx.Err() != nil {
		return time.Time{}
	}
	var machines []Machine
	if s.needsMachines() {
		machines = mc.get(ctx) // outside the lock: it reads files
	}
	var starts []start
	var next time.Time
	s.mu.Lock()
	now := s.cfg.Now() // under the lock: after every change it could follow
	for _, p := range s.order {
		if p.State.Finished() || p.TimeoutMS <= 0 {
			continue
		}
		deadline := p.CreatedAt.Add(time.Duration(p.TimeoutMS) * time.Millisecond)
		if !now.Before(deadline) {
			s.cancelLocked(p, "", fmt.Sprintf("its %s budget ran out", time.Duration(p.TimeoutMS)*time.Millisecond))
			continue
		}
		if next.IsZero() || deadline.Before(next) {
			next = deadline
		}
	}
	for key := range s.resume {
		delete(s.resume, key)
		p, i := s.findLocked(key)
		if p == nil || !p.Tasks[i].State.Active() {
			continue
		}
		t := &p.Tasks[i]
		t.Log = appendLog(t.Log, now, "resumed after the daemon restarted")
		starts = append(starts, s.launchLocked(ctx, p, t, true))
		s.commitLocked(p, i)
	}
	if machines == nil && s.hasQueuedLocked() {
		// A task became ready after the machines were (not) read: look
		// again rather than place it nowhere.
		signal(s.wake)
	}
	for _, pl := range place(s.order, machines, s.cfg.Kinds, s.heldLocked(now)) {
		p, i := pl.plan, pl.index
		t := &p.Tasks[i]
		if pl.reason != "" {
			t.State, t.Reason, t.EndedAt = StateFailed, pl.reason, now
			t.Log = appendLog(t.Log, now, pl.reason)
			s.commitLocked(p, append([]int{i}, settle(p, now)...)...)
			continue
		}
		t.State, t.Machine, t.Attempt = StateRunning, pl.machine, t.Attempt+1
		t.StartedAt, t.EndedAt, t.Phase, t.Remote, t.WorkedMS, t.Reason = now, time.Time{}, "", "", 0, ""
		t.Log = appendLog(t.Log, now, fmt.Sprintf("attempt %d on %s", t.Attempt, pl.machine))
		starts = append(starts, s.launchLocked(ctx, p, t, false))
		s.commitLocked(p, i)
	}
	s.mu.Unlock()

	for _, st := range starts {
		ex, err := s.machines.Executor(st.machine)
		if err != nil {
			s.finish(st.key, st.attempt.Task.Attempt, Outcome{State: StateFailed, Reason: err.Error(), Result: &Result{Outcome: "unreachable"}})
			continue
		}
		go s.execute(st, ex)
	}
	return next
}

// heldLocked marks the queued tasks Hold holds with why, and returns
// the test place skips them by. A task no longer held loses the reason.
func (s *Service) heldLocked(now time.Time) func(p *Plan, t *Task) bool {
	if s.cfg.Hold == nil {
		return nil
	}
	held := map[*Task]bool{}
	for _, p := range s.order {
		if p.State.Finished() {
			continue
		}
		for i := range p.Tasks {
			t := &p.Tasks[i]
			if t.State != StateQueued {
				continue
			}
			why := s.cfg.Hold(p, t)
			held[t] = why != ""
			was := strings.HasPrefix(t.Reason, "held by ")
			switch {
			case why != "" && t.Reason != why:
				t.Reason = why
				t.Log = appendLog(t.Log, now, why)
				s.commitLocked(p, i)
			case why == "" && was:
				t.Reason = ""
				t.Log = appendLog(t.Log, now, "no longer held")
				s.commitLocked(p, i)
			}
		}
	}
	return func(_ *Plan, t *Task) bool { return held[t] }
}

// needsMachines reports whether a task waits for a machine.
func (s *Service) needsMachines() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasQueuedLocked()
}

func (s *Service) hasQueuedLocked() bool {
	for _, p := range s.order {
		if p.State.Finished() {
			continue
		}
		for i := range p.Tasks {
			if p.Tasks[i].State == StateQueued {
				return true
			}
		}
	}
	return false
}

// launchLocked registers an attempt of t, so it can be cancelled from the
// moment it exists, and returns it to be started.
func (s *Service) launchLocked(ctx context.Context, p *Plan, t *Task, resume bool) start {
	key := taskKey{p.ID, t.Spec.ID}
	actx, cancel := context.WithCancelCause(ctx)
	s.attempts[key] = &attempt{n: t.Attempt, cancel: cancel}
	s.running.Add(1)
	return start{key: key, machine: t.Machine, ctx: actx, attempt: Attempt{PlanID: p.ID, PlanName: p.Name, Task: *t, Resume: resume}}
}

// execute runs one attempt on its executor and records how it ended.
func (s *Service) execute(st start, ex Executor) {
	defer s.running.Done()
	n := st.attempt.Task.Attempt
	out := ex.Execute(st.ctx, st.attempt, func(pr Progress) { s.progress(st.key, n, pr) })
	s.finish(st.key, n, out)
}

// progress records what an attempt reports. Worked time and phases are
// saved, but are not news: they bump no revision and publish nothing.
func (s *Service) progress(key taskKey, n int, pr Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, i := s.findLocked(key)
	if p == nil || p.Tasks[i].Attempt != n || !p.Tasks[i].State.Active() {
		return // a stale report from an attempt that ended
	}
	t := &p.Tasks[i]
	news := false
	set := func(dst *string, v string) {
		if v != "" && *dst != v {
			*dst, news = v, true
		}
	}
	if pr.State.Active() && pr.State != t.State {
		t.State, news = pr.State, true
	}
	set(&t.AgentID, pr.AgentID)
	set(&t.PaneID, pr.PaneID)
	set(&t.EnvironmentID, pr.EnvironmentID)
	set(&t.Path, pr.Path)
	set(&t.BaseCommit, pr.BaseCommit)
	set(&t.Remote, pr.Remote)
	if pr.Phase != "" {
		t.Phase = pr.Phase
	}
	if pr.Worked > 0 {
		t.WorkedMS = pr.Worked.Milliseconds()
	}
	if pr.Result != nil {
		t.Result, news = pr.Result, true
	}
	if pr.Log != "" {
		t.Log, news = appendLog(t.Log, s.cfg.Now(), pr.Log), true
	}
	if news {
		s.commitLocked(p, i)
		return
	}
	s.dirty[p.ID] = true
	signal(s.kick)
}

// finish records how an attempt ended. A failed attempt with retries left
// is queued again.
func (s *Service) finish(key taskKey, n int, out Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.attempts[key]; a != nil && a.n == n {
		a.cancel(nil)
		delete(s.attempts, key)
	}
	p, i := s.findLocked(key)
	if p == nil || out.State == "" {
		return // forgotten, or interrupted: resumed by the next daemon
	}
	t := &p.Tasks[i]
	if t.Attempt != n || !t.State.Active() {
		return
	}
	now := s.cfg.Now()
	if out.Result != nil {
		t.Result = out.Result
	}
	t.Reason, t.EndedAt, t.Phase = out.Reason, now, ""
	msg := string(out.State)
	if out.Reason != "" {
		msg += ": " + out.Reason
	}
	if out.State == StateFailed && t.Attempt-t.Retried <= t.Spec.Retries && !p.Cancelled {
		t.State = StateQueued
		t.Log = appendLog(t.Log, now, fmt.Sprintf("attempt %d %s; retrying", n, msg))
	} else {
		t.State = out.State
		t.Log = appendLog(t.Log, now, msg)
	}
	s.commitLocked(p, append([]int{i}, settle(p, now)...)...)
	if p.State.Finished() {
		s.log.Info("plan finished", "plan", p.ID, "status", p.State)
		s.pruneLocked()
	}
	signal(s.wake)
}

// commitLocked makes changes to p visible: a new revision, saved soon,
// published now and woken waiters. Publishing under the lock keeps events
// in revision order; the bus never blocks.
func (s *Service) commitLocked(p *Plan, tasks ...int) {
	p.Rev++
	s.dirty[p.ID] = true
	for _, i := range tasks {
		t := &p.Tasks[i]
		v := t.View(p)
		s.events.Publish(EventTask, v)
		if t.State == StateApproval {
			s.events.Publish(EventApproval, v)
		}
	}
	s.events.Publish(EventPlan, p.Summary())
	if ch := s.changed[p.ID]; ch != nil {
		close(ch)
		delete(s.changed, p.ID)
	}
	signal(s.kick)
}

// findLocked returns the plan and index of a task, nil when it is gone.
func (s *Service) findLocked(k taskKey) (*Plan, int) {
	p := s.plans[k.plan]
	if p == nil {
		return nil, 0
	}
	i := slices.IndexFunc(p.Tasks, func(t Task) bool { return t.Spec.ID == k.task })
	if i < 0 {
		return nil, 0
	}
	return p, i
}

// pruneLocked forgets the oldest finished plans beyond Keep.
func (s *Service) pruneLocked() {
	finished := 0
	for _, p := range s.order {
		if p.State.Finished() {
			finished++
		}
	}
	if finished <= s.cfg.Keep {
		return
	}
	var gone []string
	s.order = slices.DeleteFunc(s.order, func(p *Plan) bool {
		if finished > s.cfg.Keep && p.State.Finished() {
			finished--
			gone = append(gone, p.ID)
			delete(s.plans, p.ID)
			delete(s.dirty, p.ID)
			if ch := s.changed[p.ID]; ch != nil {
				close(ch) // its waiters find it gone
				delete(s.changed, p.ID)
			}
			return true
		}
		return false
	})
	s.removed = append(s.removed, gone...)
	s.events.Publish(EventRemoved, map[string][]string{"ids": gone})
	signal(s.kick)
}

// saveLoop writes changed plans, gathering changes made within SaveDelay
// into one write. When ctx ends it writes what is left and returns.
func (s *Service) saveLoop(ctx context.Context) {
	delay := time.NewTimer(s.cfg.SaveDelay)
	delay.Stop()
	defer delay.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flush(context.WithoutCancel(ctx))
			return
		case <-s.kick:
		}
		delay.Reset(s.cfg.SaveDelay)
		select {
		case <-ctx.Done():
		case <-delay.C:
		}
		s.flush(context.WithoutCancel(ctx))
	}
}

// flush writes the changed plans and deletes the forgotten ones, outside
// the lock: it copies what it writes first.
func (s *Service) flush(ctx context.Context) {
	s.mu.Lock()
	var plans []*Plan
	for id := range s.dirty {
		if p := s.plans[id]; p != nil {
			plans = append(plans, p.clone())
		}
	}
	clear(s.dirty)
	removed := s.removed
	s.removed = nil
	s.mu.Unlock()

	for _, p := range plans {
		if err := s.store.Put(ctx, p); err != nil {
			// Kept dirty: the next change tries again.
			s.log.Error("saving a plan", "plan", p.ID, "err", err)
			s.mu.Lock()
			if s.plans[p.ID] != nil {
				s.dirty[p.ID] = true
			}
			s.mu.Unlock()
		}
	}
	for _, id := range removed {
		if err := s.store.Delete(ctx, id); err != nil {
			s.log.Warn("removing a finished plan", "plan", id, "err", err)
		}
	}
}

// clone copies the plan and its tasks. Everything a task points to (its
// spec's slices, its log, result and approval) is replaced, never changed
// in place, so sharing it is safe.
func (p *Plan) clone() *Plan {
	c := *p
	c.Tasks = slices.Clone(p.Tasks)
	return &c
}

// --- The API ---

// Submit validates a plan and starts it.
func (s *Service) Submit(ctx context.Context, spec PlanSpec) (Plan, error) {
	if err := s.ready(ctx); err != nil {
		return Plan{}, err
	}
	tasks, err := Validate(spec)
	if err != nil {
		return Plan{}, err
	}
	now := s.cfg.Now()
	p := &Plan{
		Schema: schema, Name: spec.Name, State: PlanRunning,
		MaxParallel: spec.MaxParallel, Kinds: spec.Kinds, TimeoutMS: spec.TimeoutMS,
		CreatedAt: now, Tasks: make([]Task, len(tasks)),
	}
	for i, t := range tasks {
		p.Tasks[i] = Task{Spec: t, State: StateWaiting}
	}
	changed := settle(p, now)
	s.mu.Lock()
	p.ID = s.newIDLocked()
	s.mu.Unlock()
	if s.cfg.BranchName != nil {
		for i := range p.Tasks {
			t := &p.Tasks[i].Spec
			if !t.Isolate || t.Worktree != "" {
				continue
			}
			// Named now and kept with the plan: a resumed or retried
			// task finds its worktree by it.
			if t.Worktree, err = s.cfg.BranchName(p.ID, t.ID, t.Kind); err != nil {
				return Plan{}, errors.Join(ErrInvalid, fmt.Errorf("task %s: %w", t.ID, err))
			}
		}
	}
	// Saved before it is accepted: a plan the caller was told about
	// survives a crash right after.
	if err := s.store.Put(ctx, p.clone()); err != nil {
		return Plan{}, fmt.Errorf("saving the plan: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[p.ID] = p
	s.order = append(s.order, p)
	s.commitLocked(p, changed...)
	s.log.Info("plan submitted", "plan", p.ID, "tasks", len(p.Tasks))
	signal(s.wake)
	return *p.clone(), nil
}

// newIDLocked returns an unused plan ID: 8 hex digits, short enough to
// type and to name branches with.
func (s *Service) newIDLocked() string {
	for {
		var b [4]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if s.plans[id] == nil {
			return id
		}
	}
}

// resolveLocked finds a plan by ID or by a unique prefix of one.
func (s *Service) resolveLocked(id string) (*Plan, error) {
	if p := s.plans[id]; p != nil {
		return p, nil
	}
	if id == "" {
		return nil, fmt.Errorf("%w: give a plan ID", ErrInvalid)
	}
	var found *Plan
	for pid, p := range s.plans {
		if strings.HasPrefix(pid, id) {
			if found != nil {
				return nil, fmt.Errorf("%w: %q", ErrAmbiguous, id)
			}
			found = p
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: no plan %q", ErrNotFound, id)
	}
	return found, nil
}

// Get returns a plan.
func (s *Service) Get(ctx context.Context, id string) (Plan, error) {
	if err := s.ready(ctx); err != nil {
		return Plan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.resolveLocked(id)
	if err != nil {
		return Plan{}, err
	}
	return *p.clone(), nil
}

// List returns every plan's summary, newest first.
func (s *Service) List(ctx context.Context) []Summary {
	if s.ready(ctx) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Summary, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		out = append(out, s.order[i].Summary())
	}
	return out
}

// All returns every plan, newest first.
func (s *Service) All(ctx context.Context) []Plan {
	if s.ready(ctx) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Plan, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		out = append(out, *s.order[i].clone())
	}
	return out
}

// TaskOf is the plan and task an agent of this machine works or last
// worked on, newest plan first; "" for none.
func (s *Service) TaskOf(ctx context.Context, agentID string) (plan, task string) {
	if agentID == "" || s.ready(ctx) != nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.order) - 1; i >= 0; i-- {
		p := s.order[i]
		for j := range p.Tasks {
			t := &p.Tasks[j]
			if t.AgentID == agentID && (t.Machine == "" || t.Machine == LocalMachine) {
				return p.ID, t.Spec.ID
			}
		}
	}
	return "", ""
}

// Wake has the scheduler look at queued tasks again: what Hold says may
// have changed.
func (s *Service) Wake() { signal(s.wake) }

// Cancel cancels a plan's unfinished tasks, or one task. Running agents
// are stopped; the plan finishes once they have.
func (s *Service) Cancel(ctx context.Context, id, taskID string) (Plan, error) {
	if err := s.ready(ctx); err != nil {
		return Plan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.resolveLocked(id)
	if err != nil {
		return Plan{}, err
	}
	if taskID != "" {
		t := p.Task(taskID)
		if t == nil {
			return Plan{}, fmt.Errorf("%w: plan %s has no task %q", ErrNotFound, p.ID, taskID)
		}
		if t.State.Finished() {
			return Plan{}, fmt.Errorf("%w: task %s is %s", ErrState, taskID, t.State)
		}
	} else if p.State.Finished() {
		return Plan{}, fmt.Errorf("%w: plan %s is %s", ErrState, p.ID, p.State)
	}
	s.cancelLocked(p, taskID, "cancelled")
	return *p.clone(), nil
}

// cancelLocked cancels one task of p, or (taskID "") the whole plan.
func (s *Service) cancelLocked(p *Plan, taskID, reason string) {
	now := s.cfg.Now()
	if taskID == "" {
		p.Cancelled, p.Reason = true, reason
	}
	var changed []int
	for i := range p.Tasks {
		t := &p.Tasks[i]
		if taskID != "" && t.Spec.ID != taskID || t.State.Finished() {
			continue
		}
		key := taskKey{p.ID, t.Spec.ID}
		if a := s.attempts[key]; a != nil && t.State.Active() {
			a.cancel(fmt.Errorf("%w: %s", ErrCancelled, reason)) // finish records it
			continue
		}
		delete(s.resume, key)
		t.State, t.Reason, t.EndedAt = StateCancelled, reason, now
		t.Log = appendLog(t.Log, now, reason)
		changed = append(changed, i)
	}
	s.commitLocked(p, append(changed, settle(p, now)...)...)
	if p.State.Finished() {
		s.pruneLocked()
	}
	signal(s.wake)
}

// Retry runs a plan's failed, cancelled and skipped tasks again, or one of
// them, with the tasks skipped because of it. A task in a worktree goes on
// in the worktree its first attempt made.
func (s *Service) Retry(ctx context.Context, id, taskID string) (Plan, error) {
	if err := s.ready(ctx); err != nil {
		return Plan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.resolveLocked(id)
	if err != nil {
		return Plan{}, err
	}
	var targets []int
	if taskID != "" {
		i := slices.IndexFunc(p.Tasks, func(t Task) bool { return t.Spec.ID == taskID })
		if i < 0 {
			return Plan{}, fmt.Errorf("%w: plan %s has no task %q", ErrNotFound, p.ID, taskID)
		}
		if !p.Tasks[i].State.Retryable() {
			return Plan{}, fmt.Errorf("%w: task %s is %s", ErrState, taskID, p.Tasks[i].State)
		}
		targets = append(targets, i)
		for _, j := range dependents(p, i) {
			if s := p.Tasks[j].State; s == StateSkipped || s == StateCancelled {
				targets = append(targets, j)
			}
		}
	} else {
		for i := range p.Tasks {
			if p.Tasks[i].State.Retryable() {
				targets = append(targets, i)
			}
		}
		if len(targets) == 0 {
			return Plan{}, fmt.Errorf("%w: plan %s has nothing to retry", ErrState, p.ID)
		}
	}
	now := s.cfg.Now()
	p.Cancelled, p.Reason, p.State, p.EndedAt = false, "", PlanRunning, time.Time{}
	for _, i := range targets {
		t := &p.Tasks[i]
		t.State, t.Reason, t.EndedAt, t.Result, t.Retried = StateWaiting, "", time.Time{}, nil, t.Attempt
		if t.Approval != nil && !t.Approval.Approved {
			t.Approval = nil // a refusal is asked again
		}
		t.Log = appendLog(t.Log, now, "retry requested")
	}
	s.commitLocked(p, append(targets, settle(p, now)...)...)
	signal(s.wake)
	return *p.clone(), nil
}

// ApproveRequest decides on a task waiting for approval.
type ApproveRequest struct {
	ID string
	// Task may be left out when one task waits.
	Task    string
	Approve bool
	By      string
	Note    string
}

// Approve lets a waiting task start, or (Approve false) cancels it.
func (s *Service) Approve(ctx context.Context, req ApproveRequest) (Plan, error) {
	if err := s.ready(ctx); err != nil {
		return Plan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.resolveLocked(req.ID)
	if err != nil {
		return Plan{}, err
	}
	i := -1
	if req.Task != "" {
		if i = slices.IndexFunc(p.Tasks, func(t Task) bool { return t.Spec.ID == req.Task }); i < 0 {
			return Plan{}, fmt.Errorf("%w: plan %s has no task %q", ErrNotFound, p.ID, req.Task)
		}
	} else {
		var waiting []string
		for j := range p.Tasks {
			if p.Tasks[j].State == StateApproval {
				i = j
				waiting = append(waiting, p.Tasks[j].Spec.ID)
			}
		}
		if len(waiting) != 1 {
			return Plan{}, fmt.Errorf("%w: %d tasks wait for approval (%s); name one", ErrInvalid, len(waiting), strings.Join(waiting, ", "))
		}
	}
	t := &p.Tasks[i]
	if t.State != StateApproval {
		return Plan{}, fmt.Errorf("%w: task %s is %s, not waiting for approval", ErrState, t.Spec.ID, t.State)
	}
	now := s.cfg.Now()
	a := &Approval{DecidedAt: now, Approved: req.Approve, By: req.By, Note: req.Note, RequestedAt: now}
	if t.Approval != nil {
		a.RequestedAt = t.Approval.RequestedAt
	}
	t.Approval = a
	who := ""
	if req.By != "" {
		who = " by " + req.By
	}
	if req.Approve {
		t.State, t.Reason = StateQueued, ""
		t.Log = appendLog(t.Log, now, "approved"+who)
	} else {
		reason := "not approved" + who
		if req.Note != "" {
			reason += ": " + req.Note
		}
		t.State, t.Reason, t.EndedAt = StateCancelled, reason, now
		t.Log = appendLog(t.Log, now, reason)
	}
	s.commitLocked(p, append([]int{i}, settle(p, now)...)...)
	if p.State.Finished() {
		s.pruneLocked()
	}
	signal(s.wake)
	return *p.clone(), nil
}

// WaitUntil is what a wait waits for.
type WaitUntil string

const (
	// WaitFinished (the default) waits for the plan, or the task, to
	// finish.
	WaitFinished WaitUntil = "finished"
	// WaitAttention also ends when a task waits for a person: an approval
	// or a blocked agent.
	WaitAttention WaitUntil = "attention"
	// WaitChange ends at the plan's next revision after After.
	WaitChange WaitUntil = "change"
)

// WaitUntils lists what a wait may wait for.
var WaitUntils = []WaitUntil{WaitFinished, WaitAttention, WaitChange}

// WaitRequest says what to wait for.
type WaitRequest struct {
	ID string
	// Task narrows finished and attention to one task.
	Task  string
	Until WaitUntil
	// After is a revision seen earlier, for WaitChange.
	After   uint64
	Timeout time.Duration
}

// WaitResult is how a wait ended: finished, attention, changed or timeout.
type WaitResult struct {
	Outcome string `json:"outcome"`
	Plan    Plan   `json:"plan"`
}

// Wait outcomes.
const (
	OutcomeFinished  = "finished"
	OutcomeAttention = "attention"
	OutcomeChanged   = "changed"
	OutcomeTimeout   = "timeout"
)

// Wait waits for a plan to finish, change or need a person.
func (s *Service) Wait(ctx context.Context, req WaitRequest) (WaitResult, error) {
	if err := s.ready(ctx); err != nil {
		return WaitResult{}, err
	}
	req.Until = cmp.Or(req.Until, WaitFinished)
	if !slices.Contains(WaitUntils, req.Until) {
		return WaitResult{}, fmt.Errorf("%w: until %q (want one of %v)", ErrInvalid, req.Until, WaitUntils)
	}
	var timeout <-chan time.Time
	if req.Timeout > 0 {
		t := time.NewTimer(req.Timeout)
		defer t.Stop()
		timeout = t.C
	}
	for {
		s.mu.Lock()
		p, err := s.resolveLocked(req.ID)
		if err != nil {
			s.mu.Unlock()
			return WaitResult{}, err
		}
		outcome, err := waitCheck(p, req)
		changed := s.changed[p.ID]
		if changed == nil {
			changed = make(chan struct{})
			s.changed[p.ID] = changed
		}
		var snap *Plan
		if outcome != "" || err != nil {
			snap = p.clone()
		}
		s.mu.Unlock()
		if err != nil {
			return WaitResult{}, err
		}
		if outcome != "" {
			return WaitResult{Outcome: outcome, Plan: *snap}, nil
		}
		select {
		case <-ctx.Done():
			return WaitResult{}, ctx.Err()
		case <-timeout:
			p, err := s.Get(ctx, req.ID)
			return WaitResult{Outcome: OutcomeTimeout, Plan: p}, err
		case <-changed:
		}
	}
}

// waitCheck reports how a wait on p ends now, "" when it goes on.
func waitCheck(p *Plan, req WaitRequest) (string, error) {
	tasks := p.Tasks
	if req.Task != "" {
		t := p.Task(req.Task)
		if t == nil {
			return "", fmt.Errorf("%w: plan %s has no task %q", ErrNotFound, p.ID, req.Task)
		}
		tasks = []Task{*t}
	}
	finished := p.State.Finished() || req.Task != "" && tasks[0].State.Finished()
	switch {
	case req.Until == WaitChange:
		if p.Rev > req.After {
			return OutcomeChanged, nil
		}
	case finished:
		return OutcomeFinished, nil
	case req.Until == WaitAttention && slices.ContainsFunc(tasks, func(t Task) bool {
		return t.State == StateApproval || t.State == StateBlocked
	}):
		return OutcomeAttention, nil
	}
	return "", nil
}
