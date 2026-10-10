package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// Event types the service publishes.
const (
	// EventState says an agent's state changed: Agent.
	EventState = "agent.state"
	// EventOutput says a terminal's screen changed: {"id": process ID},
	// at most once per OutputInterval per terminal.
	EventOutput = "process.output"
)

// Timing.
const (
	// QuietAfter is how long a screen must stay unchanged to count as
	// quiet when activity decides.
	QuietAfter = 1500 * time.Millisecond
	// OutputInterval bounds process.output events per terminal.
	OutputInterval = time.Second
	// tick is how often states are re-evaluated for time (quiet screens,
	// expired reports) and kinds re-detected.
	tick = 250 * time.Millisecond
	// redetectAfter is how often a terminal whose screen changed is asked
	// again which program is in its foreground.
	redetectAfter = 2 * time.Second
	// keepExited is how long an exited agent stays listed with its last
	// screen's verdict.
	keepExited = 10 * time.Minute
)

// HIVE_AGENT, in a process's environment, names its manifest and turns
// off detection by process.
const EnvAgent = "HIVE_AGENT"

// Processes is what the service reads about processes.
type Processes interface {
	Get(ctx context.Context, id string) (process.Process, error)
	List(ctx context.Context, envID string) ([]process.Process, error)
}

// Terminals finds a process's terminal session.
type Terminals interface {
	Get(processID string) (terminal.Session, error)
}

// Locator tells where an agent's process is shown: its pane, if any.
type Locator interface {
	Locate(ctx context.Context) (map[string]Location, error) // by process ID
}

// Location is an agent's place in the workspace.
type Location struct {
	PaneID, TabID, Name string
}

// Events receives what the service publishes.
type Events interface {
	Publish(typ string, data any)
}

// Config tunes the service.
type Config struct {
	// UserDir holds manifests that add to or replace the built-in ones
	// (~/.config/hive/agent-detection); empty means none.
	UserDir string
	Logger  *slog.Logger
	// Now replaces the clock in tests.
	Now func() time.Time
}

// Service follows every terminal and keeps each one's agent state. It is
// a terminal.Watcher: the terminal service passes it every screen.
type Service struct {
	cfg    Config
	procs  Processes
	terms  Terminals
	locate Locator
	events Events
	log    *slog.Logger
	now    func() time.Time

	mu        sync.Mutex
	manifests []*Manifest
	trackers  map[string]*tracker // by process ID
	pending   []Agent             // state changes to publish, in order
	wake      chan struct{}       // a new terminal needs its kind resolved

	publishing sync.Mutex // keeps batches of state events in order
}

var _ terminal.Watcher = (*Service)(nil)

// NewService loads the manifests and returns a service. Bad user manifests
// are skipped with a warning; the built-in ones must load.
func NewService(cfg Config, procs Processes, terms Terminals, locate Locator, events Events) (*Service, []string, error) {
	s := &Service{
		cfg: cfg, procs: procs, terms: terms, locate: locate, events: events,
		log:      logging.OrDiscard(cfg.Logger),
		now:      cfg.Now,
		trackers: map[string]*tracker{},
		wake:     make(chan struct{}, 1),
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.events == nil {
		s.events = nopEvents{}
	}
	ms, warnings, err := LoadManifests(cfg.UserDir)
	if err != nil {
		return nil, warnings, err
	}
	s.manifests = ms
	return s, warnings, nil
}

type nopEvents struct{}

func (nopEvents) Publish(string, any) {}

// Manifests returns the loaded manifests, by ID.
func (s *Service) Manifests() []*Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.manifests)
}

// Reload reads the manifests again (after editing a user manifest) and
// re-detects every terminal's agent.
func (s *Service) Reload() ([]string, error) {
	ms, warnings, err := LoadManifests(s.cfg.UserDir)
	if err != nil {
		return warnings, err
	}
	s.mu.Lock()
	s.manifests = ms
	for _, t := range s.trackers {
		t.resolved = false // detect again with the new manifests
	}
	s.mu.Unlock()
	s.poke()
	return warnings, nil
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// --- watching terminals ---

// Frame applies a terminal's screen change (terminal.Watcher).
func (s *Service) Frame(processID string, f *vt.Frame) {
	now := s.now()
	s.mu.Lock()
	t := s.trackers[processID]
	if t == nil {
		t = &tracker{id: processID, started: now, status: Status{State: StateUnknown, Since: now}}
		s.trackers[processID] = t
		defer s.poke()
	}
	changed := t.apply(f, now)
	if changed {
		t.lastFrame = now
	}
	t.ended, t.endedAt = false, time.Time{}
	if t.resolved {
		s.evaluateLocked(t, now)
	}
	publishOutput := changed && !f.Keyframe && now.Sub(t.lastOutputEvent) >= OutputInterval
	if publishOutput {
		t.lastOutputEvent = now
	}
	s.unlockAndPublish()
	if publishOutput {
		s.events.Publish(EventOutput, map[string]string{"id": processID})
	}
}

// Ended notes that a terminal's output ended (terminal.Watcher). Whether
// the process exited is confirmed from its record on the next tick.
func (s *Service) Ended(processID string) {
	s.mu.Lock()
	if t := s.trackers[processID]; t != nil {
		t.ended, t.endedAt = true, s.now()
	}
	s.mu.Unlock()
	s.poke()
}

// Run re-evaluates states over time and resolves agents' kinds until ctx
// ends.
func (s *Service) Run(ctx context.Context) {
	tk := time.NewTicker(tick)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		case <-s.wake:
		}
		s.resolve(ctx)
		s.mu.Lock()
		now := s.now()
		for id, t := range s.trackers {
			if t.status.State == StateExited && now.Sub(t.status.Since) > keepExited {
				delete(s.trackers, id)
				continue
			}
			if t.resolved {
				s.evaluateLocked(t, now)
			}
		}
		s.unlockAndPublish()
	}
}

// resolve works out, outside the lock, which manifest recognises each
// terminal that needs it: new terminals, terminals whose screen changed a
// while after the last check (the shell may have started an agent, or an
// agent returned to the shell), and terminals that ended.
func (s *Service) resolve(ctx context.Context) {
	type job struct {
		id    string
		ended bool
	}
	now := s.now()
	var jobs []job
	s.mu.Lock()
	for id, t := range s.trackers {
		switch {
		case t.ended && t.status.State != StateExited:
			jobs = append(jobs, job{id: id, ended: true})
		case !t.resolved:
			jobs = append(jobs, job{id: id})
		case !t.pinned && t.lastFrame.After(t.detectedAt) && now.Sub(t.detectedAt) >= redetectAfter:
			jobs = append(jobs, job{id: id})
		}
	}
	manifests := s.manifests
	s.mu.Unlock()

	for _, j := range jobs {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		p, err := s.procs.Get(ctx, j.id)
		if j.ended {
			cancel()
			s.mu.Lock()
			if t := s.trackers[j.id]; t != nil {
				switch {
				case err == nil && !p.Active():
					s.setLocked(t, Status{State: StateExited, Source: SourceProcess, Reason: statusReason(p)}, s.now())
				case err == nil:
					delete(s.trackers, j.id) // still running: the daemon let go of its shim
				}
			}
			s.unlockAndPublish()
			continue
		}
		var args []string
		pinned := false
		var m *Manifest
		if err == nil {
			if kind := p.Env[EnvAgent]; kind != "" {
				m, pinned = byID(manifests, kind), true
			}
		}
		if m == nil {
			args = s.foreground(ctx, j.id)
			if len(args) == 0 && err == nil {
				args = append([]string{p.Command}, p.Args...)
			}
			m = Recognise(manifests, args)
		}
		cancel()
		s.mu.Lock()
		if t := s.trackers[j.id]; t != nil {
			if t.manifest != m {
				t.manifest, t.verdict = m, Verdict{State: StateUnknown, Rule: -1}
				t.classify()
				if t.status.State != StateExited {
					t.worked = false
					s.setLocked(t, Status{State: StateUnknown}, s.now()) // a different program: start over
				}
			}
			t.pinned, t.resolved, t.detectedAt = pinned, true, s.now()
			if p.PID > 0 {
				t.pid = p.PID
			}
			s.evaluateLocked(t, s.now())
		}
		s.unlockAndPublish()
	}
}

// foreground returns the arguments of the program in front of a
// terminal, nil when it cannot be known.
func (s *Service) foreground(ctx context.Context, processID string) []string {
	sess, err := s.terms.Get(processID)
	if err != nil {
		return nil
	}
	fr, ok := sess.(terminal.ForegroundReader)
	if !ok {
		return nil
	}
	fg, err := fr.Foreground(ctx)
	if err != nil {
		return nil
	}
	return fg.Args
}

func byID(ms []*Manifest, id string) *Manifest {
	for _, m := range ms {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// Recognise returns the manifest that recognises a process with args (the
// highest priority, then the first by ID), nil for none.
func Recognise(ms []*Manifest, args []string) *Manifest {
	var best *Manifest
	for _, m := range ms {
		if m.Matches(args) && (best == nil || m.Priority > best.Priority) {
			best = m
		}
	}
	return best
}

func statusReason(p process.Process) string {
	if p.ExitCode != nil {
		return fmt.Sprintf("%s %d", p.Status, *p.ExitCode)
	}
	return string(p.Status)
}

// evaluateLocked recomputes a tracker's state and publishes a change.
func (s *Service) evaluateLocked(t *tracker, now time.Time) {
	if t.status.State == StateExited {
		return
	}
	s.setLocked(t, t.decide(now), now)
}

// setLocked moves a tracker to a new status, counting and publishing the
// change. Only state changes count; a new reason alone updates silently.
func (s *Service) setLocked(t *tracker, st Status, now time.Time) {
	prev := t.status
	if st.State == prev.State {
		t.status.Source, t.status.Reason = st.Source, st.Reason
		return
	}
	st.Since = now
	st.StateSeq = prev.StateSeq + 1
	st.CompletionSeq = prev.CompletionSeq
	if st.State == StateDone {
		st.CompletionSeq++
	}
	st.LastOutput = prev.LastOutput
	st.SessionID = prev.SessionID
	t.status = st
	s.pending = append(s.pending, t.agent())
}

// unlockAndPublish releases mu, then publishes the state changes queued
// while it was held, in order, with their panes looked up once.
func (s *Service) unlockAndPublish() {
	if len(s.pending) == 0 {
		s.mu.Unlock() // the common case: a frame that changed no state
		return
	}
	s.publishing.Lock() // before Unlock: batches leave in the order they were made
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()
	defer s.publishing.Unlock()
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	loc, _ := s.locate.Locate(ctx)
	for _, a := range batch {
		a.withLocation(loc)
		s.events.Publish(EventState, a)
	}
}

// --- queries and commands ---

// ErrNotFound means no agent has that ID.
var ErrNotFound = errors.New("agent: not found")

// ErrInvalid wraps a bad request.
var ErrInvalid = errors.New("agent: invalid")

// List returns the agents: terminal processes a manifest recognises, or
// with all every terminal process.
func (s *Service) List(ctx context.Context, all bool) ([]Agent, error) {
	procs, err := s.procs.List(ctx, "")
	if err != nil {
		return nil, err
	}
	loc, err := s.locate.Locate(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	manifests := s.manifests
	var out []Agent
	for _, p := range procs {
		if !p.Terminal {
			continue
		}
		a := s.agentLocked(p, manifests)
		if a.Kind == "" && !all {
			continue
		}
		out = append(out, a)
	}
	s.mu.Unlock()
	for i := range out {
		out[i].withLocation(loc)
	}
	return out, nil
}

// agentLocked describes process p as an agent: from its tracker when it is
// watched, else from its record (an agent that ended before this daemon).
func (s *Service) agentLocked(p process.Process, manifests []*Manifest) Agent {
	if t := s.trackers[p.ID]; t != nil && t.resolved {
		a := t.agent()
		a.EnvironmentID, a.PID = p.EnvironmentID, p.PID
		if t.status.State != StateExited && !p.Active() {
			a.State, a.Source, a.Reason = StateExited, SourceProcess, statusReason(p)
		}
		return a
	}
	var m *Manifest
	if kind := p.Env[EnvAgent]; kind != "" {
		m = byID(manifests, kind)
	}
	if m == nil {
		m = Recognise(manifests, append([]string{p.Command}, p.Args...))
	}
	a := Agent{ID: p.ID, EnvironmentID: p.EnvironmentID, PID: p.PID, Status: Status{State: StateUnknown, Since: p.StartedAt}}
	if m != nil {
		a.Kind, a.Name = m.ID, m.Name
	}
	if !p.Active() {
		a.State, a.Source, a.Reason = StateExited, SourceProcess, statusReason(p)
		if p.EndedAt != nil {
			a.Since = *p.EndedAt
		}
	}
	return a
}

// Get returns one agent, by process ID or pane ID.
func (s *Service) Get(ctx context.Context, id string) (Agent, error) {
	list, err := s.List(ctx, true)
	if err != nil {
		return Agent{}, err
	}
	for _, a := range list {
		if a.ID == id || (a.PaneID != "" && a.PaneID == id) {
			return a, nil
		}
	}
	return Agent{}, fmt.Errorf("%w: %q", ErrNotFound, id)
}

// Report records what an agent says about itself (a hook). It holds until
// its TTL passes or another report replaces it.
func (s *Service) Report(ctx context.Context, id string, r Report) (Agent, error) {
	if !r.State.reportable() {
		return Agent{}, fmt.Errorf("%w: state %q (want working, blocked, idle or done)", ErrInvalid, r.State)
	}
	if r.TTL <= 0 {
		r.TTL = DefaultReportTTL
	}
	procID, err := s.processOf(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	now := s.now()
	s.mu.Lock()
	t := s.trackers[procID]
	if t == nil {
		s.mu.Unlock()
		return Agent{}, fmt.Errorf("%w: %q has no terminal", ErrNotFound, id)
	}
	t.report = &report{Report: r, until: now.Add(r.TTL)}
	if r.SessionID != "" {
		t.status.SessionID = r.SessionID
	}
	if t.resolved {
		s.evaluateLocked(t, now)
	}
	s.unlockAndPublish()
	return s.Get(ctx, procID)
}

// Seen marks an agent looked at (its pane focused, its output read): done
// becomes idle.
func (s *Service) Seen(ctx context.Context, id string) error {
	procID, err := s.processOf(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if t := s.trackers[procID]; t != nil && t.resolved {
		t.worked = false
		if t.report != nil && t.report.State == StateDone {
			t.report = nil // a done report is answered by looking
		}
		s.evaluateLocked(t, s.now())
	}
	s.unlockAndPublish()
	return nil
}

// processOf resolves a process or pane ID to a process ID.
func (s *Service) processOf(ctx context.Context, id string) (string, error) {
	s.mu.Lock()
	_, ok := s.trackers[id]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	loc, err := s.locate.Locate(ctx)
	if err != nil {
		return "", err
	}
	for procID, l := range loc {
		if l.PaneID == id {
			return procID, nil
		}
	}
	if _, err := s.procs.Get(ctx, id); err == nil {
		return id, nil
	}
	return "", fmt.Errorf("%w: %q", ErrNotFound, id)
}

// Explanation says how an agent's state was decided.
type Explanation struct {
	Agent Agent `json:"agent"`
	// Manifest is the recognising manifest's ID and file.
	Manifest string `json:"manifest,omitempty"`
	Source   string `json:"manifest_source,omitempty"`
	// Detected is how the kind was found: env (HIVE_AGENT), process.
	Detected string `json:"detected,omitempty"`
	// Verdict is what the rules read from the current screen.
	Verdict Verdict `json:"verdict"`
	// Report is the self-report in force, if any.
	Report *ReportState `json:"report,omitempty"`
	// Quiet is how long the screen has not changed.
	Quiet  time.Duration `json:"quiet_ns"`
	Worked bool          `json:"worked_since_seen"`
	// Screen is the mirrored screen, as text.
	Screen []string `json:"screen"`
}

// ReportState is a report and when it expires.
type ReportState struct {
	Report
	Until time.Time `json:"until"`
}

// Explain shows the evidence behind an agent's state.
func (s *Service) Explain(ctx context.Context, id string) (Explanation, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return Explanation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.trackers[a.ID]
	ex := Explanation{Agent: a, Verdict: Verdict{State: StateUnknown, Rule: -1}}
	if t == nil {
		return ex, nil
	}
	if t.manifest != nil {
		ex.Manifest, ex.Source = t.manifest.ID, t.manifest.Source
		ex.Detected = "process"
		if t.pinned {
			ex.Detected = "env " + EnvAgent
		}
	}
	ex.Verdict = t.verdict
	if t.report != nil {
		ex.Report = &ReportState{Report: t.report.Report, Until: t.report.until}
	}
	ex.Quiet = s.now().Sub(t.lastFrame)
	ex.Worked = t.worked
	for y := range t.screen.Lines {
		ex.Screen = append(ex.Screen, t.screen.LineText(y))
	}
	return ex, nil
}

// Rollups summarises states per tab, environment and the whole session.
type Rollups struct {
	Tabs         map[string]State `json:"tabs"`
	Environments map[string]State `json:"environments"`
	Session      State            `json:"session"`
}

// RollupOf computes the summaries of the agents in list.
func RollupOf(list []Agent) Rollups {
	r := Rollups{Tabs: map[string]State{}, Environments: map[string]State{}}
	var all []State
	for _, a := range list {
		if a.Kind == "" {
			continue
		}
		all = append(all, a.State)
		if a.TabID != "" {
			r.Tabs[a.TabID] = Rollup(r.Tabs[a.TabID], a.State)
		}
		r.Environments[a.EnvironmentID] = Rollup(r.Environments[a.EnvironmentID], a.State)
	}
	r.Session = Rollup(all...)
	return r
}

func (a *Agent) withLocation(loc map[string]Location) {
	if l, ok := loc[a.ID]; ok {
		a.PaneID, a.TabID = l.PaneID, l.TabID
		if l.Name != "" {
			a.Name = l.Name
		}
	}
}

// --- trackers ---

// tracker is one terminal's mirrored screen and detection state.
type tracker struct {
	id         string
	pid        int
	screen     vt.Screen
	manifest   *Manifest // nil: no agent recognised
	pinned     bool      // the kind came from HIVE_AGENT
	resolved   bool      // the kind has been looked for
	detectedAt time.Time

	verdict         Verdict
	started         time.Time
	lastFrame       time.Time // the screen last changed
	lastOutputEvent time.Time
	output          bool // the screen ever changed after the first keyframe
	worked          bool // it worked since it was last seen: quiet means done
	report          *report
	ended           bool
	endedAt         time.Time

	status Status
}

type report struct {
	Report
	until time.Time
}

// apply updates the mirror and reports whether the screen changed. Rules
// are re-read only when a change touches a region they read.
func (t *tracker) apply(f *vt.Frame, now time.Time) bool {
	first := t.screen.Rows == 0
	titleChanged := f.Title != t.screen.Title
	rows := map[int]bool{}
	for _, l := range f.Lines {
		rows[l.Y] = true
	}
	t.screen.Apply(f)
	changed := len(f.Lines) > 0 || titleChanged
	if !first && changed {
		t.output = true
		t.status.LastOutput = now
	}
	if t.manifest != nil && (f.Keyframe || t.manifest.touches(rows, t.screen.Rows, titleChanged)) {
		t.classify()
	}
	return changed && !first
}

func (t *tracker) classify() {
	if t.manifest == nil {
		t.verdict = Verdict{State: StateUnknown, Rule: -1}
		return
	}
	t.verdict = t.manifest.Classify(&t.screen)
}

// decide works out the state from the evidence: a report beats the
// manifest's rules, which beat screen activity.
func (t *tracker) decide(now time.Time) Status {
	st := t.evidence(now)
	switch st.State {
	case StateWorking, StateBlocked:
		t.worked = true
	case StateIdle:
		if t.worked {
			st.State = StateDone
		}
	case StateDone:
		t.worked = true // stays done until seen
	}
	return st
}

func (t *tracker) evidence(now time.Time) Status {
	if r := t.report; r != nil {
		if now.Before(r.until) {
			reason := "reported"
			if r.Message != "" {
				reason = "reported: " + r.Message
			}
			return Status{State: r.State, Source: SourceReport, Reason: reason}
		}
		t.report = nil
	}
	if t.manifest != nil && t.verdict.State != StateUnknown {
		reason := t.verdict.Description
		if reason == "" {
			reason = fmt.Sprintf("%s matched %q", t.verdict.Region, truncate(t.verdict.Text, 60))
		}
		return Status{State: t.verdict.State, Source: SourceManifest, Reason: reason}
	}
	if t.manifest != nil && !t.manifest.Activity {
		if !t.output && t.screen.Text() == "" {
			return Status{State: StateUnknown}
		}
		return Status{State: StateIdle, Source: SourceManifest, Reason: "no rule matched"}
	}
	switch {
	case !t.output:
		return Status{State: StateUnknown}
	case now.Sub(t.lastFrame) < QuietAfter:
		return Status{State: StateWorking, Source: SourceActivity, Reason: "the screen is changing"}
	}
	return Status{State: StateIdle, Source: SourceActivity, Reason: "the screen is quiet"}
}

// agent describes the tracker as an Agent (location filled in by callers).
func (t *tracker) agent() Agent {
	a := Agent{ID: t.id, PID: t.pid, Status: t.status}
	if t.manifest != nil {
		a.Kind, a.Name = t.manifest.ID, t.manifest.Name
	}
	return a
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return strings.TrimSpace(s)
}
