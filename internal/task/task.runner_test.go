package task_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/task"
)

// fakeAgents plays an agent for the runner: a script of states that a
// prompt starts, and a screen.
type fakeAgents struct {
	t   *testing.T
	dir string

	mu      sync.Mutex
	state   agent.State
	seq     uint64
	screen  []string
	prompts []string
	starts  []task.StartRequest
	stopped []string
	changed chan struct{}
	// onPrompt runs (without the lock) after a prompt is taken.
	onPrompt func(f *fakeAgents, text string)
}

func newFakeAgents(t *testing.T) *fakeAgents {
	return &fakeAgents{t: t, dir: t.TempDir(), state: agent.StateIdle, changed: make(chan struct{})}
}

// set moves the agent to a state; done counts a completion.
func (f *fakeAgents) set(s agent.State, screen ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = s
	if s == agent.StateDone {
		f.seq++
	}
	if screen != nil {
		f.screen = screen
	}
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *fakeAgents) Start(_ context.Context, req task.StartRequest) (task.Started, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, req)
	env := req.EnvironmentID
	if req.Worktree != "" {
		env = "wt-" + strings.ReplaceAll(req.Worktree, "/", "-")
	}
	return task.Started{AgentID: "p1", PaneID: "pane1", EnvironmentID: env, Path: f.dir}, nil
}

func (f *fakeAgents) Prompt(_ context.Context, _ string, req agent.PromptRequest) (agent.PromptResult, error) {
	f.mu.Lock()
	if f.state == agent.StateBlocked {
		f.mu.Unlock()
		return agent.PromptResult{}, agent.ErrBlocked
	}
	f.prompts = append(f.prompts, req.Text)
	f.state = agent.StateWorking
	close(f.changed)
	f.changed = make(chan struct{})
	a := f.agentLocked()
	hook := f.onPrompt
	f.mu.Unlock()
	if hook != nil {
		go hook(f, req.Text)
	}
	return agent.PromptResult{Outcome: agent.OutcomeStarted, Agent: a}, nil
}

func (f *fakeAgents) agentLocked() agent.Agent {
	return agent.Agent{ID: "p1", Kind: "claude", Status: agent.Status{State: f.state, CompletionSeq: f.seq}}
}

func (f *fakeAgents) Wait(ctx context.Context, _ string, req agent.WaitRequest) (agent.WaitResult, error) {
	f.mu.Lock()
	start := f.seq
	f.mu.Unlock()
	after := start
	if req.After != nil {
		after = *req.After
	}
	var timeout <-chan time.Time
	if req.Timeout > 0 {
		tm := time.NewTimer(req.Timeout)
		defer tm.Stop()
		timeout = tm.C
	}
	for {
		f.mu.Lock()
		a, ch := f.agentLocked(), f.changed
		f.mu.Unlock()
		reached := false
		switch req.Until {
		case agent.UntilIdle:
			reached = a.State == agent.StateIdle || a.State == agent.StateDone
		case agent.UntilDone:
			reached = a.CompletionSeq > after
		case agent.UntilUnblocked:
			reached = a.State != agent.StateBlocked
		}
		switch {
		case reached:
			return agent.WaitResult{Outcome: agent.OutcomeReached, Agent: a}, nil
		case a.State == agent.StateExited:
			return agent.WaitResult{Outcome: agent.OutcomeExited, Agent: a}, nil
		case a.State == agent.StateBlocked:
			return agent.WaitResult{Outcome: agent.OutcomeBlocked, Agent: a}, nil
		}
		select {
		case <-ctx.Done():
			return agent.WaitResult{}, ctx.Err()
		case <-timeout:
			return agent.WaitResult{Outcome: agent.OutcomeTimeout, Agent: a}, nil
		case <-ch:
		}
	}
}

func (f *fakeAgents) Read(context.Context, string, int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.screen), nil
}

func (f *fakeAgents) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *fakeAgents) lists() (prompts, stopped []string, starts []task.StartRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.prompts), slices.Clone(f.stopped), slices.Clone(f.starts)
}

// fakeWork is a workspace whose command exits with exit.
type fakeWork struct {
	exit int
	ran  [][]string
	mu   sync.Mutex
}

func (w *fakeWork) Head(context.Context, string) (string, error) { return "abc123", nil }

func (w *fakeWork) Changes(_ context.Context, _, base string, _ int) ([]string, int, error) {
	if base != "abc123" {
		return nil, 0, fmt.Errorf("base %q", base)
	}
	return []string{"a.go", "b.go"}, 3, nil
}

func (w *fakeWork) Run(_ context.Context, _ string, argv []string, _ map[string]string) (int, []string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ran = append(w.ran, argv)
	return w.exit, []string{"ok " + strings.Join(argv, " ")}, nil
}

// progressLog collects an attempt's progress.
type progressLog struct {
	mu   sync.Mutex
	list []task.Progress
}

func (p *progressLog) add(pr task.Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.list = append(p.list, pr)
}

func (p *progressLog) states() []task.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []task.State
	for _, x := range p.list {
		if x.State != "" {
			out = append(out, x.State)
		}
	}
	return out
}

func newRunner(f *fakeAgents, w *fakeWork) *task.Runner {
	return task.NewRunner(task.RunnerConfig{Ready: 5 * time.Second, Segment: 50 * time.Millisecond, Poll: 10 * time.Millisecond}, f, w)
}

func attemptOf(s task.Spec) task.Attempt {
	return task.Attempt{PlanID: "plan1", Task: task.Task{Spec: s, Attempt: 1}}
}

func TestRunnerRunsATurnAndCollectsItsResult(t *testing.T) {
	f := newFakeAgents(t)
	f.onPrompt = func(f *fakeAgents, text string) {
		// The agent writes its summary where the prompt says, then finishes.
		rel := task.SummaryPath("plan1", "fix")
		if !strings.Contains(text, filepath.ToSlash(rel)) {
			t.Errorf("the prompt does not ask for a summary: %q", text)
		}
		_ = os.WriteFile(filepath.Join(f.dir, rel), []byte("Fixed the bug.\n"), 0o644)
		f.set(agent.StateDone, "• Fixed it", "› ")
	}
	s := spec("fix")
	s.Isolate = true
	s.Env = map[string]string{"X": "1"}
	pl := &progressLog{}
	out := newRunner(f, &fakeWork{}).Execute(t.Context(), attemptOf(s), pl.add)
	if out.State != task.StateSucceeded || out.Result == nil {
		t.Fatalf("outcome: %+v", out)
	}
	r := out.Result
	if r.Outcome != "met" || r.Summary != "Fixed the bug." || strings.Join(r.Screen, "|") != "• Fixed it|› " ||
		strings.Join(r.Changed, ",") != "a.go,b.go" || r.ChangedMore != 3 {
		t.Fatalf("result: %+v", r)
	}
	prompts, stopped, starts := f.lists()
	if len(prompts) != 1 || !strings.HasPrefix(prompts[0], "do fix") {
		t.Fatalf("prompts: %q", prompts)
	}
	if len(starts) != 1 || starts[0].Worktree != "hive/plan1/fix" || starts[0].Env["X"] != "1" ||
		starts[0].Env[task.EnvPlan] != "plan1" || starts[0].Env[task.EnvSummary] != filepath.ToSlash(task.SummaryPath("plan1", "fix")) {
		t.Fatalf("start: %+v", starts)
	}
	if len(stopped) != 1 {
		t.Fatalf("the agent is stopped when its task ends: %v", stopped)
	}
}

func TestRunnerPausesWhileTheAgentIsBlocked(t *testing.T) {
	f := newFakeAgents(t)
	f.onPrompt = func(f *fakeAgents, _ string) { f.set(agent.StateBlocked) }
	pl := &progressLog{}
	done := make(chan task.Outcome, 1)
	go func() { done <- newRunner(f, &fakeWork{}).Execute(t.Context(), attemptOf(spec("ask")), pl.add) }()
	waitFor(t, "the task to show blocked", func() bool { return slices.Contains(pl.states(), task.StateBlocked) })
	f.set(agent.StateWorking) // someone answered
	waitFor(t, "the task to run again", func() bool {
		st := pl.states()
		return len(st) > 0 && st[len(st)-1] == task.StateRunning
	})
	f.set(agent.StateDone)
	if out := <-done; out.State != task.StateSucceeded {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestRunnerConditions(t *testing.T) {
	cases := map[string]struct {
		spec   func(s *task.Spec)
		agent  func(f *fakeAgents, text string)
		exit   int
		state  task.State
		reason string
	}{
		"a command that passes": {
			spec:  func(s *task.Spec) { s.Until, s.Command = task.UntilCommand, []string{"go", "test"} },
			agent: func(f *fakeAgents, _ string) { f.set(agent.StateDone) },
			state: task.StateSucceeded,
		},
		"a command that fails": {
			spec:  func(s *task.Spec) { s.Until, s.Command = task.UntilCommand, []string{"go", "test"} },
			agent: func(f *fakeAgents, _ string) { f.set(agent.StateDone) },
			exit:  2, state: task.StateFailed, reason: "go exited 2",
		},
		"a screen that matches": {
			spec:  func(s *task.Spec) { s.Until, s.Match = task.UntilMatch, `ALL \d+ PASS` },
			agent: func(f *fakeAgents, _ string) { f.set(agent.StateWorking, "running", "ALL 12 PASS") },
			state: task.StateSucceeded,
		},
		"a turn that ends without a match": {
			spec:  func(s *task.Spec) { s.Until, s.Match = task.UntilMatch, `ALL \d+ PASS` },
			agent: func(f *fakeAgents, _ string) { f.set(agent.StateDone, "3 FAILED") },
			state: task.StateFailed, reason: "without its screen matching",
		},
		"an agent that exits": {
			spec:  func(*task.Spec) {},
			agent: func(f *fakeAgents, _ string) { f.set(agent.StateExited) },
			state: task.StateFailed, reason: "the agent exited",
		},
		"a budget that runs out": {
			spec:  func(s *task.Spec) { s.TimeoutMS = 30 },
			agent: func(*fakeAgents, string) {},
			state: task.StateFailed, reason: "budget ran out",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeAgents(t)
			f.onPrompt = c.agent
			s := spec("t")
			c.spec(&s)
			w := &fakeWork{exit: c.exit}
			out := newRunner(f, w).Execute(t.Context(), attemptOf(s), func(task.Progress) {})
			if out.State != c.state || !strings.Contains(out.Reason, c.reason) {
				t.Fatalf("outcome %s %q, want %s %q", out.State, out.Reason, c.state, c.reason)
			}
			if s.Until == task.UntilCommand {
				if out.Result.ExitCode == nil || *out.Result.ExitCode != c.exit || len(out.Result.Output) != 1 {
					t.Fatalf("command result: %+v", out.Result)
				}
			}
		})
	}
}

func TestRunnerResumesWithoutPromptingAgain(t *testing.T) {
	f := newFakeAgents(t)
	f.set(agent.StateWorking)
	a := attemptOf(spec("long"))
	a.Resume = true
	a.Task.AgentID, a.Task.Path, a.Task.BaseCommit, a.Task.Phase = "p1", f.dir, "abc123", task.PhasePrompted
	done := make(chan task.Outcome, 1)
	go func() { done <- newRunner(f, &fakeWork{}).Execute(t.Context(), a, func(task.Progress) {}) }()
	f.set(agent.StateIdle) // the turn ends after the restart
	out := <-done
	prompts, _, starts := f.lists()
	if out.State != task.StateSucceeded || len(prompts) != 0 || len(starts) != 0 {
		t.Fatalf("resumed: %+v, prompts %q, starts %d", out, prompts, len(starts))
	}
}

// After a restart a resumed task has no completion count to wait past, so
// it waits for the agent to be ready again; once someone answers a blocked
// agent, the moment between the answer and the work can read as idle, and
// must not end the task.
func TestRunnerResumedAndAnsweredWaitsForTheTurnToEnd(t *testing.T) {
	f := newFakeAgents(t)
	f.set(agent.StateBlocked)
	a := attemptOf(spec("ask"))
	a.Resume = true
	a.Task.AgentID, a.Task.Path, a.Task.Phase = "p1", f.dir, task.PhasePrompted
	pl := &progressLog{}
	done := make(chan task.Outcome, 1)
	go func() { done <- newRunner(f, &fakeWork{}).Execute(t.Context(), a, pl.add) }()
	waitFor(t, "the task to show blocked", func() bool { return slices.Contains(pl.states(), task.StateBlocked) })
	f.set(agent.StateIdle) // answered: the prompt line shows before the work does
	waitFor(t, "the task to run again", func() bool {
		st := pl.states()
		return len(st) > 0 && st[len(st)-1] == task.StateRunning
	})
	select {
	case out := <-done:
		t.Fatalf("ended between the answer and the work: %+v", out)
	case <-time.After(100 * time.Millisecond):
	}
	f.set(agent.StateWorking)
	f.set(agent.StateDone)
	if out := <-done; out.State != task.StateSucceeded {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestRunnerCancelledStopsTheAgentButInterruptedDoesNot(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		t.Run(fmt.Sprint("cancelled=", cancelled), func(t *testing.T) {
			f := newFakeAgents(t)
			ctx, cancel := context.WithCancelCause(t.Context())
			f.onPrompt = func(*fakeAgents, string) {
				if cancelled {
					cancel(fmt.Errorf("%w: by a test", task.ErrCancelled))
				} else {
					cancel(context.Canceled)
				}
			}
			out := newRunner(f, &fakeWork{}).Execute(ctx, attemptOf(spec("t")), func(task.Progress) {})
			_, stopped, _ := f.lists()
			if cancelled && (out.State != task.StateCancelled || out.Reason != "by a test" || len(stopped) != 1) {
				t.Fatalf("cancelled: %+v, stopped %v", out, stopped)
			}
			if !cancelled && (out.State != "" || len(stopped) != 0) {
				t.Fatalf("interrupted: %+v, stopped %v", out, stopped)
			}
		})
	}
}

func TestRunnerRetriesInTheWorktreeItMade(t *testing.T) {
	f := newFakeAgents(t)
	f.onPrompt = func(f *fakeAgents, _ string) { f.set(agent.StateDone) }
	s := spec("again")
	s.Isolate = true
	a := attemptOf(s)
	a.Task.Attempt, a.Task.AgentID, a.Task.EnvironmentID = 2, "old", "wt-hive-plan1-again"
	out := newRunner(f, &fakeWork{}).Execute(t.Context(), a, func(task.Progress) {})
	_, stopped, starts := f.lists()
	if out.State != task.StateSucceeded || len(starts) != 1 || starts[0].Worktree != "" || starts[0].EnvironmentID != "wt-hive-plan1-again" {
		t.Fatalf("retry: %+v %+v", out, starts)
	}
	if len(stopped) != 2 || stopped[0] != "old" {
		t.Fatalf("the earlier attempt's agent makes way: %v", stopped)
	}
}

func TestRunnerFailsWhenTheAgentDoesNotStart(t *testing.T) {
	f := &failingAgents{fakeAgents: newFakeAgents(t)}
	r := task.NewRunner(task.RunnerConfig{}, f, &fakeWork{})
	out := r.Execute(t.Context(), attemptOf(spec("t")), func(task.Progress) {})
	if out.State != task.StateFailed || !strings.Contains(out.Reason, "no manifest") {
		t.Fatalf("outcome %+v", out)
	}
}

type failingAgents struct{ *fakeAgents }

func (failingAgents) Start(context.Context, task.StartRequest) (task.Started, error) {
	return task.Started{}, errors.New("no manifest \"nope\"")
}
