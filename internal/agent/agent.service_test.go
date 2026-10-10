package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// Screens a Claude Code manifest reads (see claude.toml).
const (
	claudeIdle    = "\x1b[H\x1b[2J ▐▛███▛█   Claude Code\x1b[18;1H❯ \x1b[20;1H  ? for shortcuts"
	claudeWorking = "\x1b[H\x1b[2J ▐▛███▛█   Claude Code\x1b[16;1H✻ Osmosing…\x1b[18;1H❯ \x1b[20;1H  esc to interrupt"
	claudeBlocked = "\x1b[H\x1b[2J Bash command\x1b[3;1H Do you want to proceed?\x1b[4;1H ❯ 1. Yes\x1b[5;1H   2. No\x1b[7;1H Esc to cancel · Tab to amend"
)

type fakeProcs struct {
	mu    sync.Mutex
	procs map[string]process.Process
}

func (f *fakeProcs) Get(_ context.Context, id string) (process.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.procs[id]
	if !ok {
		return process.Process{}, errors.New("no such process")
	}
	return p, nil
}

func (f *fakeProcs) List(context.Context, string) ([]process.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []process.Process
	for _, p := range f.procs {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeProcs) set(p process.Process) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.procs[p.ID] = p
}

type noTerminals struct{}

func (noTerminals) Get(string) (terminal.Session, error) { return nil, errors.New("none") }

type fakeLocator map[string]Location

func (l fakeLocator) Locate(context.Context) (map[string]Location, error) { return l, nil }

type recorder struct {
	mu     sync.Mutex
	states []Agent
}

func (r *recorder) Publish(typ string, data any) {
	if typ != EventState {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, data.(Agent))
}

func (r *recorder) seen() []State {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []State
	for _, a := range r.states {
		out = append(out, a.State)
	}
	return out
}

// harness drives a Service with a fake clock and terminals it writes to.
type harness struct {
	t      testing.TB
	svc    *Service
	procs  *fakeProcs
	events *recorder
	now    time.Time
	terms  map[string]*term
}

type term struct {
	vt   *vt.Terminal
	view vt.View
}

func newHarness(t testing.TB) *harness {
	t.Helper()
	h := &harness{
		t:      t,
		procs:  &fakeProcs{procs: map[string]process.Process{}},
		events: &recorder{},
		now:    time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
		terms:  map[string]*term{},
	}
	loc := fakeLocator{"p1": {PaneID: "pane-1", TabID: "tab-1"}}
	svc, warnings, err := NewService(Config{Now: func() time.Time { return h.now }}, h.procs, noTerminals{}, loc, h.events)
	if err != nil || len(warnings) > 0 {
		t.Fatalf("NewService: %v %v", err, warnings)
	}
	h.svc = svc
	t.Cleanup(func() {
		for _, tm := range h.terms {
			tm.vt.Close()
		}
	})
	return h
}

// start runs a process with a terminal showing screen.
func (h *harness) start(id string, argv []string, env map[string]string, screen string) {
	h.t.Helper()
	h.procs.set(process.Process{
		ID: id, EnvironmentID: "env", Command: argv[0], Args: argv[1:], Env: env,
		Status: process.StatusRunning, Terminal: true, PID: 100, StartedAt: h.now,
	})
	h.terms[id] = &term{vt: vt.New(80, 20, vt.Options{})}
	h.show(id, screen)
	h.svc.step(context.Background())
}

// show writes a screen to a process's terminal and passes the change on.
func (h *harness) show(id, screen string) {
	h.t.Helper()
	tm := h.terms[id]
	if _, err := tm.vt.Write([]byte(screen)); err != nil {
		h.t.Fatal(err)
	}
	h.svc.Frame(id, tm.vt.Frame(&tm.view))
}

func (h *harness) advance(d time.Duration) {
	h.now = h.now.Add(d)
	h.svc.step(context.Background())
}

func (h *harness) want(id string, state State) Agent {
	h.t.Helper()
	a, err := h.svc.Get(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	if a.State != state {
		h.t.Fatalf("%s is %s (%s: %s), want %s", id, a.State, a.Source, a.Reason, state)
	}
	return a
}

func TestStatesFollowTheScreen(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeIdle)
	a := h.want("p1", StateIdle)
	if a.Kind != "claude" || a.PaneID != "pane-1" || a.TabID != "tab-1" {
		t.Fatalf("agent = %+v", a)
	}

	h.show("p1", claudeWorking)
	h.want("p1", StateWorking)
	h.show("p1", claudeBlocked)
	h.want("p1", StateBlocked)
	h.show("p1", claudeWorking)
	h.show("p1", claudeIdle)
	a = h.want("p1", StateDone)
	if a.CompletionSeq != 1 {
		t.Fatalf("completion seq = %d, want 1", a.CompletionSeq)
	}

	// Done holds until someone looks.
	h.advance(time.Minute)
	h.want("p1", StateDone)
	if err := h.svc.Seen(context.Background(), "pane-1"); err != nil {
		t.Fatal(err)
	}
	a = h.want("p1", StateIdle)

	want := []State{StateIdle, StateWorking, StateBlocked, StateWorking, StateDone, StateIdle}
	if got := h.events.seen(); !slices.Equal(got, want) {
		t.Fatalf("published %v, want %v", got, want)
	}
	if a.StateSeq != uint64(len(want)) {
		t.Fatalf("state seq = %d, want %d", a.StateSeq, len(want))
	}
}

func TestReportWinsUntilItExpires(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeIdle)
	ctx := context.Background()

	a, err := h.svc.Report(ctx, "pane-1", Report{State: StateWorking, TTL: time.Minute, SessionID: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateWorking || a.Source != SourceReport || a.SessionID != "abc" {
		t.Fatalf("after report: %+v", a)
	}
	h.advance(30 * time.Second)
	h.want("p1", StateWorking)
	h.advance(31 * time.Second) // expired: the idle screen decides, after work
	a = h.want("p1", StateDone)
	if a.SessionID != "abc" {
		t.Fatalf("session id lost: %+v", a)
	}

	if _, err := h.svc.Report(ctx, "p1", Report{State: StateExited}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("report exited: %v, want ErrInvalid", err)
	}
	if _, err := h.svc.Report(ctx, "nope", Report{State: StateIdle}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("report unknown: %v, want ErrNotFound", err)
	}
}

func TestDoneReportIsAnsweredBySeeing(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeIdle)
	ctx := context.Background()
	if _, err := h.svc.Report(ctx, "p1", Report{State: StateDone}); err != nil {
		t.Fatal(err)
	}
	h.want("p1", StateDone)
	if err := h.svc.Seen(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	h.want("p1", StateIdle)
}

func TestActivityDecidesWithoutAManifest(t *testing.T) {
	h := newHarness(t)
	h.start("p2", []string{"bash"}, nil, "$ ")
	list, err := h.svc.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("a shell listed as an agent: %+v", list)
	}
	a := h.want("p2", StateUnknown)
	if a.Kind != "" {
		t.Fatalf("kind = %q", a.Kind)
	}
	h.show("p2", "make\r\nbuilding…")
	h.want("p2", StateWorking)
	h.advance(QuietAfter)
	h.want("p2", StateDone)
}

func TestHiveAgentPinsTheManifest(t *testing.T) {
	h := newHarness(t)
	h.start("p3", []string{"/opt/wrapper.sh"}, map[string]string{EnvAgent: "claude"}, claudeWorking)
	a := h.want("p3", StateWorking)
	if a.Kind != "claude" {
		t.Fatalf("kind = %q, want claude", a.Kind)
	}
	ex, err := h.svc.Explain(context.Background(), "p3")
	if err != nil {
		t.Fatal(err)
	}
	if ex.Detected != "env "+EnvAgent || ex.Verdict.State != StateWorking || len(ex.Screen) == 0 {
		t.Fatalf("explanation = %+v", ex)
	}
}

func TestInterpreterArgumentsAreRecognised(t *testing.T) {
	h := newHarness(t)
	h.start("p4", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js"}, nil, "")
	if a := h.want("p4", StateUnknown); a.Kind != "codex" {
		t.Fatalf("kind = %q, want codex", a.Kind)
	}
	h.start("p5", []string{"/bin/bash", "/home/u/.asdf/shims/claude"}, nil, "")
	if a := h.want("p5", StateUnknown); a.Kind != "claude" {
		t.Fatalf("kind = %q, want claude", a.Kind)
	}
}

func TestExitedAgents(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeWorking)
	code := 0
	h.procs.set(process.Process{ID: "p1", Command: "claude", Status: process.StatusExited, ExitCode: &code, Terminal: true})
	h.svc.Ended("p1")
	h.advance(time.Millisecond)
	a := h.want("p1", StateExited)
	if a.Source != SourceProcess || a.Reason != "exited 0" {
		t.Fatalf("exited agent = %+v", a)
	}
	// A frame after the end does not bring it back to life.
	h.advance(time.Second)
	h.want("p1", StateExited)
}

func TestRollups(t *testing.T) {
	r := RollupOf([]Agent{
		{Kind: "claude", EnvironmentID: "a", TabID: "t1", Status: Status{State: StateIdle}},
		{Kind: "codex", EnvironmentID: "a", TabID: "t1", Status: Status{State: StateBlocked}},
		{Kind: "codex", EnvironmentID: "b", TabID: "t2", Status: Status{State: StateDone}},
		{Kind: "", EnvironmentID: "b", TabID: "t2", Status: Status{State: StateWorking}}, // not an agent
	})
	if r.Session != StateBlocked || r.Tabs["t1"] != StateBlocked || r.Tabs["t2"] != StateDone ||
		r.Environments["a"] != StateBlocked || r.Environments["b"] != StateDone {
		t.Fatalf("rollups = %+v", r)
	}
	if Rollup() != "" {
		t.Fatal("empty rollup is not empty")
	}
}

// BenchmarkIdleStep is the service's periodic work with 100 idle agents:
// it runs four times a second, so 100 agents at idle cost 4×this per
// second.
func BenchmarkIdleStep(b *testing.B) {
	h := newHarness(b)
	for i := range 100 {
		h.start(fmt.Sprintf("p%d", i), []string{"claude"}, nil, claudeIdle)
	}
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		h.svc.step(ctx)
	}
}
