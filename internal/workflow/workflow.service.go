package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Config is the service's policy. Zero values are the defaults.
type Config struct {
	Logger *slog.Logger
	Now    func() time.Time
	// OwnDir holds the person's own workflow files; Paths more files and
	// directories of them, shared ones (absolute).
	OwnDir string
	Paths  []string
	// Home makes "~/" in triggers' repo absolute.
	Home string
	// MaxRuns caps runs running at once (default DefaultMaxRuns).
	MaxRuns int
	// Keep and KeepAge bound the finished runs kept (defaults DefaultKeep
	// and DefaultKeepAge; a negative KeepAge keeps them however old).
	Keep    int
	KeepAge time.Duration
	// Interval is how often workflow files and watched repositories are
	// read again (default DefaultInterval).
	Interval time.Duration
}

func (c Config) withDefaults() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.MaxRuns <= 0 {
		c.MaxRuns = DefaultMaxRuns
	}
	if c.Keep <= 0 {
		c.Keep = DefaultKeep
	}
	if c.KeepAge == 0 {
		c.KeepAge = DefaultKeepAge
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	return c
}

// Service owns workflows' definitions and runs.
type Service struct {
	cfg   Config
	log   *slog.Logger
	store Store
	ports Ports

	loaded   chan struct{}
	loadOnce sync.Once
	loadErr  error

	mu      sync.Mutex
	ctx     context.Context // the daemon's, once Run starts
	runs    map[string]*Run
	order   []*Run // oldest first
	live    map[liveKey]*liveAttempt
	changed map[string]chan struct{}
	dirty   map[string]bool
	wake    chan struct{}
	saveNow chan struct{}
	wg      sync.WaitGroup

	defs     map[string]*Workflow // by name
	files    map[string]fileStamp // by path
	trust    map[string]string    // path → digest
	triggers *triggers
}

type liveKey struct {
	run  string
	step int
}

// liveAttempt is a running attempt: how to cancel it.
type liveAttempt struct {
	cancel context.CancelCauseFunc
}

// NewService returns a service over store; Load reads what a previous
// daemon left.
func NewService(cfg Config, store Store, ports Ports) *Service {
	cfg = cfg.withDefaults()
	s := &Service{
		cfg: cfg, log: logging.OrDiscard(cfg.Logger), store: store, ports: ports,
		loaded:  make(chan struct{}),
		runs:    map[string]*Run{},
		live:    map[liveKey]*liveAttempt{},
		changed: map[string]chan struct{}{},
		dirty:   map[string]bool{},
		wake:    make(chan struct{}, 1),
		saveNow: make(chan struct{}, 1),
		defs:    map[string]*Workflow{},
		files:   map[string]fileStamp{},
		trust:   map[string]string{},
	}
	if s.ports.Events == nil {
		s.ports.Events = nopEvents{}
	}
	s.triggers = newTriggers(s)
	return s
}

type nopEvents struct{}

func (nopEvents) Publish(string, any) {}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Load reads the runs, trust and trigger state a previous daemon left,
// and the workflow files, once; Run calls it first. Unfinished runs
// resume once Run starts.
func (s *Service) Load(ctx context.Context) error {
	s.loadOnce.Do(func() {
		s.loadErr = s.load(ctx)
		close(s.loaded)
	})
	return s.loadErr
}

func (s *Service) ready(ctx context.Context) error {
	select {
	case <-s.loaded:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) load(ctx context.Context) error {
	runs, err := s.store.ListRuns(ctx)
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	trust, terr := s.store.LoadTrust()
	if terr != nil {
		errs = append(errs, terr)
	}
	st, serr := s.store.LoadState()
	if serr != nil {
		errs = append(errs, serr)
	}
	slices.SortFunc(runs, func(a, b Run) int { return a.CreatedAt.Compare(b.CreatedAt) })
	now := s.cfg.Now()
	s.mu.Lock()
	s.trust = trust
	for i := range runs {
		r := &runs[i]
		if !r.State.Finished() {
			s.resumeLocked(r, now)
		}
		s.runs[r.ID] = r
		s.order = append(s.order, r)
	}
	s.mu.Unlock()
	s.triggers.load(st)
	s.reload(ctx)
	return errors.Join(errs...)
}

// resumeLocked prepares an unfinished run a previous daemon left: agent
// steps wait on their plans again (their agents kept working), and
// commands and actions that were running run again, without spending a
// retry.
func (s *Service) resumeLocked(r *Run, now time.Time) {
	for i := range r.Steps {
		st := &r.Steps[i]
		if st.State != StepRunning {
			continue
		}
		if st.Kind == KindAgent && st.Current != nil && st.Current.Plan != "" {
			logStep(st, now, "the daemon restarted; waiting on plan "+st.Current.Plan+" again")
			continue
		}
		st.State, st.NextAt = StepRetrying, now
		st.Attempt--
		st.Attempts--
		logStep(st, now, "interrupted by a daemon restart; running it again")
		s.dirty[r.ID] = true
	}
}

// Run drives runs and triggers until ctx ends. Steps that are running
// then are left as they are, to resume with the next daemon.
func (s *Service) Run(ctx context.Context) {
	if err := s.Load(ctx); err != nil {
		s.log.Warn("reading the workflow runs a previous daemon left", "err", err) // the rest runs
	}
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	var bg sync.WaitGroup
	bg.Go(func() { s.saveLoop(ctx) })
	bg.Go(func() { s.triggers.run(ctx) })
	signal(s.wake)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		next := s.tick(ctx)
		wait := time.Hour
		if !next.IsZero() {
			wait = max(time.Until(next), 10*time.Millisecond)
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			s.wg.Wait() // attempts end with ctx; they record nothing
			bg.Wait()
			s.flush(context.WithoutCancel(ctx))
			return
		case <-s.wake:
		case <-timer.C:
		}
	}
}

// --- Saving and publishing ---

// commitLocked records a change to r: a new revision, a save soon, and
// events for the run and the steps given.
func (s *Service) commitLocked(r *Run, steps ...int) {
	r.Rev++
	s.dirty[r.ID] = true
	signal(s.saveNow)
	if ch := s.changed[r.ID]; ch != nil {
		close(ch)
		delete(s.changed, r.ID)
	}
	for _, i := range slices.Compact(slices.Sorted(slices.Values(steps))) {
		s.ports.Events.Publish(EventStep, StepView{Run: r.ID, Workflow: r.Workflow, StepRun: cloneStep(r.Steps[i])})
	}
	s.ports.Events.Publish(EventRun, r.Summary())
}

func cloneStep(st StepRun) StepRun {
	st.Results = slices.Clone(st.Results)
	st.Log = slices.Clone(st.Log)
	if st.Current != nil {
		c := *st.Current
		st.Current = &c
	}
	return st
}

// saveLoop writes changed runs, gathering the changes of 100 ms into one
// write.
func (s *Service) saveLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.saveNow:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		s.flush(ctx)
	}
}

// flush writes every changed run.
func (s *Service) flush(ctx context.Context) {
	s.mu.Lock()
	var runs []*Run
	for id := range s.dirty {
		if r := s.runs[id]; r != nil {
			runs = append(runs, r.clone())
		}
	}
	clear(s.dirty)
	s.mu.Unlock()
	for _, r := range runs {
		if err := s.store.PutRun(ctx, r); err != nil {
			s.log.Error("saving a workflow run", "run", r.ID, "err", err)
		}
	}
}

// saveRun writes r before the caller goes on: a run (or a plan ID)
// the caller was told about survives a crash right after.
func (s *Service) saveRun(ctx context.Context, r *Run) error {
	return s.store.PutRun(ctx, r)
}

func logStep(st *StepRun, now time.Time, msg string) {
	if len(msg) > maxLineBytes {
		msg = msg[:maxLineBytes]
	}
	st.Log = append(st.Log, LogEntry{Time: now, Message: msg})
	if len(st.Log) > MaxLog {
		st.Log = st.Log[len(st.Log)-MaxLog:]
	}
}

// --- The API ---

// SubmitRequest starts a run: of a known workflow by name, or of the text
// of one.
type SubmitRequest struct {
	Name   string
	TOML   string
	Source string
	Inputs map[string]string
	// Dir is where the run's steps run unless they say: the caller's
	// directory.
	Dir string
	By  string
}

// Submit validates a run's workflow and inputs, and queues it.
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (Run, error) {
	if err := s.ready(ctx); err != nil {
		return Run{}, err
	}
	var (
		spec   Spec
		source string
		path   string
		err    error
	)
	switch {
	case req.Name != "" && req.TOML != "":
		return Run{}, invalid("give a workflow's name or its text, not both")
	case req.TOML != "":
		source = req.TOML
		if spec, err = Parse([]byte(req.TOML), cmpStr(req.Source, "workflow")); err != nil {
			return Run{}, err
		}
	case req.Name != "":
		s.mu.Lock()
		w := s.defs[req.Name]
		var wc Workflow
		if w != nil {
			wc = *w
		}
		s.mu.Unlock()
		if w == nil {
			return Run{}, fmt.Errorf("%w: no workflow %q (hive workflow list)", ErrNotFound, req.Name)
		}
		if wc.Error != "" {
			return Run{}, invalid("workflow %s does not parse: %s", wc.Name, wc.Error)
		}
		spec, source, path = *wc.Spec, string(wc.source), wc.Path
	default:
		return Run{}, invalid("give a workflow's name or its text")
	}
	tr := Trigger{Type: TriggerManual, Time: s.cfg.Now(), By: req.By}
	r, err := s.newRun(spec, source, path, req.Inputs, tr, cmpStr(req.Dir, defaultDir(path)))
	if err != nil {
		return Run{}, err
	}
	return s.accept(ctx, r)
}

// newRun makes a queued run of spec: its inputs checked and completed.
func (s *Service) newRun(spec Spec, source, path string, inputs map[string]string, tr Trigger, dir string) (*Run, error) {
	values := map[string]string{}
	for name, v := range inputs {
		if spec.Input(name) == nil {
			return nil, invalid("workflow %s has no input %q", spec.Name, name)
		}
		if len(v) > MaxInputBytes {
			return nil, invalid("input %s is longer than %d bytes", name, MaxInputBytes)
		}
		values[name] = v
	}
	for _, in := range spec.Inputs {
		if _, ok := values[in.Name]; ok {
			continue
		}
		if in.Required && in.Default == "" {
			return nil, invalid("workflow %s needs input %s (-i %s=…)", spec.Name, in.Name, in.Name)
		}
		values[in.Name] = in.Default
	}
	if tr.Data == nil {
		tr.Data = map[string]any{}
	}
	tr.Data["type"] = tr.Type
	sum := sha256.Sum256([]byte(source))
	r := &Run{
		Workflow: spec.Name, Path: path, Digest: hex.EncodeToString(sum[:]), Source: source, Spec: spec,
		Inputs: values, Trigger: tr, Dir: dir, State: StateQueued, CreatedAt: s.cfg.Now(),
		Steps: make([]StepRun, len(spec.Steps)),
	}
	for i, st := range spec.Steps {
		r.Steps[i] = StepRun{ID: st.ID, Kind: st.Kind(), State: StepWaiting}
	}
	return r, nil
}

// accept names a new run, saves it and queues it.
func (s *Service) accept(ctx context.Context, r *Run) (Run, error) {
	s.mu.Lock()
	r.ID = s.newIDLocked()
	s.mu.Unlock()
	// Saved before it is accepted: a run the caller was told about
	// survives a crash right after.
	if err := s.saveRun(ctx, r.clone()); err != nil {
		return Run{}, fmt.Errorf("saving the run: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
	s.order = append(s.order, r)
	s.commitLocked(r)
	s.log.Info("workflow run queued", "run", r.ID, "workflow", r.Workflow, "trigger", r.Trigger.Type)
	signal(s.wake)
	return *r.clone(), nil
}

// newIDLocked returns an unused run ID: 8 hex digits.
func (s *Service) newIDLocked() string {
	for {
		var b [4]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if s.runs[id] == nil {
			return id
		}
	}
}

// Rerun starts a new run from a run's recorded text, inputs and trigger.
func (s *Service) Rerun(ctx context.Context, id, by string) (Run, error) {
	if err := s.ready(ctx); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	old, err := s.resolveLocked(id)
	var src *Run
	if err == nil {
		src = old.clone()
	}
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}
	tr := Trigger{Type: TriggerRerun, Time: s.cfg.Now(), By: by, From: src.ID, Data: maps.Clone(src.Trigger.Data)}
	if tr.Data == nil {
		tr.Data = map[string]any{}
	}
	tr.Data["from"] = src.Trigger.Type
	r, err := s.newRun(src.Spec, src.Source, src.Path, src.Inputs, tr, src.Dir)
	if err != nil {
		return Run{}, err
	}
	return s.accept(ctx, r)
}

// resolveLocked finds a run by ID or by a unique prefix of one.
func (s *Service) resolveLocked(id string) (*Run, error) {
	if r := s.runs[id]; r != nil {
		return r, nil
	}
	if id == "" {
		return nil, invalid("give a run ID")
	}
	var found *Run
	for rid, r := range s.runs {
		if strings.HasPrefix(rid, id) {
			if found != nil {
				return nil, fmt.Errorf("%w: %q", ErrAmbiguous, id)
			}
			found = r
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: no run %q", ErrNotFound, id)
	}
	return found, nil
}

// Get returns a run.
func (s *Service) Get(ctx context.Context, id string) (Run, error) {
	if err := s.ready(ctx); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.resolveLocked(id)
	if err != nil {
		return Run{}, err
	}
	return *r.clone(), nil
}

// List returns runs' summaries, newest first: of one workflow when named,
// at most limit when positive.
func (s *Service) List(ctx context.Context, workflow string, limit int) []RunSummary {
	if s.ready(ctx) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []RunSummary{}
	for i := len(s.order) - 1; i >= 0; i-- {
		r := s.order[i]
		if workflow != "" && r.Workflow != workflow {
			continue
		}
		out = append(out, r.Summary())
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// Cancel cancels a run: its agents' tasks are cancelled, its commands
// stopped, and its steps that had not started are cancelled.
func (s *Service) Cancel(ctx context.Context, id string) (Run, error) {
	if err := s.ready(ctx); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.resolveLocked(id)
	if err != nil {
		return Run{}, err
	}
	if r.State.Finished() {
		return Run{}, fmt.Errorf("%w: run %s already %s", ErrState, r.ID, r.State)
	}
	s.cancelLocked(r, "cancelled")
	signal(s.wake)
	return *r.clone(), nil
}

// cancelLocked marks r cancelled and stops its running attempts; the next
// tick settles its steps.
func (s *Service) cancelLocked(r *Run, reason string) {
	if r.Cancelled {
		return
	}
	r.Cancelled, r.Reason = true, reason
	for k, l := range s.live {
		if k.run == r.ID {
			l.cancel(ErrCancelled)
		}
	}
	s.log.Info("workflow run cancelled", "run", r.ID, "reason", reason)
	s.commitLocked(r)
}

// WaitResult is how a wait ended.
type WaitResult struct {
	Outcome string `json:"outcome"`
	Run     Run    `json:"run"`
}

// Wait outcomes.
const (
	WaitFinished = "finished"
	WaitTimeout  = "timeout"
)

// Wait waits for a run to finish, or for timeout (0: no bound).
func (s *Service) Wait(ctx context.Context, id string, timeout time.Duration) (WaitResult, error) {
	if err := s.ready(ctx); err != nil {
		return WaitResult{}, err
	}
	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	for {
		s.mu.Lock()
		r, err := s.resolveLocked(id)
		if err != nil {
			s.mu.Unlock()
			return WaitResult{}, err
		}
		if r.State.Finished() {
			snap := r.clone()
			s.mu.Unlock()
			return WaitResult{Outcome: WaitFinished, Run: *snap}, nil
		}
		ch := s.changed[r.ID]
		if ch == nil {
			ch = make(chan struct{})
			s.changed[r.ID] = ch
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return WaitResult{}, ctx.Err()
		case <-expired:
			r, err := s.Get(ctx, id)
			return WaitResult{Outcome: WaitTimeout, Run: r}, err
		case <-ch:
		}
	}
}

// pruneLocked forgets the oldest finished runs beyond Keep, and those
// older than KeepAge.
func (s *Service) pruneLocked(now time.Time) []string {
	finished := 0
	for _, r := range s.order {
		if r.State.Finished() {
			finished++
		}
	}
	var gone []string
	kept := s.order[:0]
	for _, r := range s.order {
		old := s.cfg.KeepAge > 0 && !r.EndedAt.IsZero() && now.Sub(r.EndedAt) > s.cfg.KeepAge
		if r.State.Finished() && (finished > s.cfg.Keep || old) {
			finished--
			gone = append(gone, r.ID)
			delete(s.runs, r.ID)
			delete(s.dirty, r.ID)
			continue
		}
		kept = append(kept, r)
	}
	clear(s.order[len(kept):])
	s.order = kept
	return gone
}

// Prune forgets finished runs beyond the retention settings now (hive gc).
func (s *Service) Prune(ctx context.Context) int {
	if s.ready(ctx) != nil {
		return 0
	}
	s.mu.Lock()
	gone := s.pruneLocked(s.cfg.Now())
	s.mu.Unlock()
	s.forget(ctx, gone)
	return len(gone)
}

func (s *Service) forget(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		if err := s.store.DeleteRun(ctx, id); err != nil {
			s.log.Warn("forgetting a workflow run", "run", id, "err", err)
		}
	}
	s.ports.Events.Publish(EventRemoved, map[string][]string{"ids": ids})
}

// running counts runs that run, overall and per workflow.
func (s *Service) runningLocked() (int, map[string]int) {
	n, per := 0, map[string]int{}
	for _, r := range s.order {
		if r.State == StateRunning {
			n++
			per[r.Workflow]++
		}
	}
	return n, per
}

// pendingLocked counts a workflow's runs that have not finished.
func (s *Service) pendingLocked(workflow string) (queued, running int) {
	for _, r := range s.order {
		if r.Workflow != workflow {
			continue
		}
		switch r.State {
		case StateQueued:
			queued++
		case StateRunning:
			running++
		}
	}
	return queued, running
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
