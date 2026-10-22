package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/logging"
)

// The runner is the local Executor: it runs an attempt with an agent on
// this machine, through two ports the runtime fills in.

// Agents is what the runner needs from this machine's agents.
type Agents interface {
	// Start starts an agent; with Worktree, in a new (or the existing)
	// worktree on that branch.
	Start(ctx context.Context, req StartRequest) (Started, error)
	Prompt(ctx context.Context, id string, req agent.PromptRequest) (agent.PromptResult, error)
	Wait(ctx context.Context, id string, req agent.WaitRequest) (agent.WaitResult, error)
	// Read returns the agent's last lines, compacted.
	Read(ctx context.Context, id string, lines int) ([]string, error)
	// Stop stops the agent; its pane keeps its last screen.
	Stop(ctx context.Context, id string) error
}

// StartRequest starts a task's agent.
type StartRequest struct {
	Kind          string
	EnvironmentID string
	Worktree      string
	Base          string
	Name          string
	Args          []string
	Env           map[string]string
}

// Started is where a task's agent runs.
type Started struct {
	AgentID       string
	PaneID        string
	EnvironmentID string
	// Path is the agent's working directory.
	Path string
}

// Workspace is what the runner needs from this machine's files.
type Workspace interface {
	// Head returns the commit checked out in dir; "" outside a repository.
	Head(ctx context.Context, dir string) (string, error)
	// Changes lists the files changed in dir since base (committed or
	// not, and new files), at most limit, and how many more there are.
	Changes(ctx context.Context, dir, base string, limit int) (paths []string, more int, err error)
	// Run runs argv in dir and returns its exit status and last lines.
	Run(ctx context.Context, dir string, argv []string, env map[string]string) (exit int, tail []string, err error)
}

// Executor runs attempts on one machine.
type Executor interface {
	// Execute runs the attempt until its condition is met or it fails, and
	// reports what happens on the way through progress. When ctx ends
	// with ErrCancelled as its cause the attempt is cancelled; when it
	// ends otherwise (the daemon stops) Execute returns an Outcome without
	// a State, leaving the agent running, and a later Execute with Resume
	// picks it up.
	Execute(ctx context.Context, a Attempt, progress func(Progress)) Outcome
}

// Attempt is one try at a task.
type Attempt struct {
	PlanID   string
	PlanName string
	// Task is a snapshot: the spec, and how far the attempt got when it
	// resumes.
	Task Task
	// Resume says the attempt ran when the daemon stopped.
	Resume bool
}

// Progress is what an attempt reports on its way. Empty fields change
// nothing.
type Progress struct {
	State         State
	Phase         Phase
	AgentID       string
	PaneID        string
	EnvironmentID string
	Path          string
	BaseCommit    string
	Remote        string
	// Worked is the agent's working time so far, when positive.
	Worked time.Duration
	// Log is a line for the task's log.
	Log string
	// Result is a remote attempt's result as it stands.
	Result *Result
}

// Outcome is how an attempt ended. A zero State means it was interrupted
// and resumes later.
type Outcome struct {
	State  State
	Result *Result
	Reason string
}

// RunnerConfig tunes the runner; zero values are the defaults.
type RunnerConfig struct {
	Logger *slog.Logger
	Now    func() time.Time
	// Ready bounds how long a new agent gets to become ready for its
	// prompt (default 2 minutes).
	Ready time.Duration
	// Segment bounds one wait on an agent, so worked time is recorded
	// (and survives a restart) at least this often (default 30 seconds).
	Segment time.Duration
	// Poll is how often a match condition reads the screen (default 1
	// second).
	Poll time.Duration
	// Stall ends an attempt whose agent works without changing its screen
	// for this long (default agent.DefaultStallTimeout; negative never).
	Stall time.Duration
	// CommandTimeout bounds a condition command when the task has no time
	// budget left to bound it (default 1 hour).
	CommandTimeout time.Duration
}

// Runner runs attempts with this machine's agents.
type Runner struct {
	agents Agents
	work   Workspace
	cfg    RunnerConfig
	log    *slog.Logger
}

// NewRunner returns the local executor.
func NewRunner(cfg RunnerConfig, agents Agents, work Workspace) *Runner {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Ready <= 0 {
		cfg.Ready = 2 * time.Minute
	}
	if cfg.Segment <= 0 {
		cfg.Segment = 30 * time.Second
	}
	if cfg.Poll <= 0 {
		cfg.Poll = time.Second
	}
	switch {
	case cfg.Stall == 0:
		cfg.Stall = agent.DefaultStallTimeout
	case cfg.Stall < 0:
		cfg.Stall = 0
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = time.Hour
	}
	return &Runner{agents: agents, work: work, cfg: cfg, log: logging.OrDiscard(cfg.Logger)}
}

// SummaryPath is where an agent is asked to write its summary, relative to
// its working directory. It is left out of the task's changed files.
func SummaryPath(planID, taskID string) string {
	return filepath.Join(".hive", "tasks", planID+"-"+taskID+".md")
}

// summaryRequest is added to a prompt so the agent leaves a summary.
func summaryRequest(path string) string {
	return "\n\nWhen you have finished, write a short summary of what you did, and of anything left undone, to the file " +
		filepath.ToSlash(path) + " (relative to your working directory)."
}

// execution is one attempt in progress. Only its own goroutine touches it.
type execution struct {
	r      *Runner
	a      Attempt
	spec   Spec
	report func(Progress)

	agentID, path, base string
	worked              time.Duration
	// after is the agent's completion count before the prompt; nil after
	// a restart, when the count started again.
	after *uint64
}

// Execute runs an attempt to its end (see Executor).
func (r *Runner) Execute(ctx context.Context, a Attempt, progress func(Progress)) Outcome {
	x := &execution{
		r: r, a: a, spec: a.Task.Spec, report: progress,
		agentID: a.Task.AgentID, path: a.Task.Path, base: a.Task.BaseCommit,
		worked: time.Duration(a.Task.WorkedMS) * time.Millisecond,
	}
	o := x.run(ctx)
	if o.State == "" {
		return o // interrupted: the agent keeps working for the next daemon
	}
	if x.agentID != "" && !x.spec.KeepAgent {
		// The pane stays, showing the agent's last screen.
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := r.agents.Stop(bg, x.agentID); err != nil && !errors.Is(err, agent.ErrNotFound) {
			r.log.Debug("stopping a task's agent", "plan", a.PlanID, "task", x.spec.ID, "err", err)
		}
		cancel()
	}
	return o
}

func (x *execution) run(ctx context.Context) Outcome {
	phase := Phase("")
	if x.a.Resume && x.agentID != "" {
		phase = x.a.Task.Phase
	}
	if phase == "" || phase == PhaseStarting {
		if err := x.start(ctx); err != nil {
			return x.end(ctx, "failed", fmt.Sprintf("the agent did not start: %v", err), err)
		}
		phase = PhaseStarted
	}
	if phase == PhaseStarted {
		outcome, reason, err := x.prompt(ctx)
		if outcome != "" || err != nil {
			return x.end(ctx, outcome, reason, err)
		}
		phase = PhasePrompted
	}
	if x.spec.Until == UntilMatch {
		outcome, reason, err := x.awaitMatch(ctx)
		return x.end(ctx, outcome, reason, err)
	}
	if phase == PhasePrompted {
		until := agent.UntilDone
		if x.spec.Until == UntilIdle || x.after == nil {
			// Ready for a prompt again ends the turn too, and needs no
			// completion count from before a restart.
			until = agent.UntilIdle
		}
		outcome, reason, err := x.await(ctx, until, nil)
		if outcome != "met" || err != nil {
			return x.end(ctx, outcome, reason, err)
		}
		if x.spec.Until != UntilCommand {
			return x.end(ctx, "met", "", nil)
		}
		x.report(Progress{Phase: PhaseChecking, Log: "the agent finished; checking with " + strings.Join(x.spec.Command, " ")})
	}
	return x.check(ctx)
}

// start starts the attempt's agent: in the worktree an earlier attempt
// made, or a new one, or the task's environment.
func (x *execution) start(ctx context.Context) error {
	t := x.a.Task
	if t.AgentID != "" && !x.a.Resume {
		// An earlier attempt's agent makes way.
		if err := x.r.agents.Stop(ctx, t.AgentID); err != nil && !errors.Is(err, agent.ErrNotFound) {
			x.r.log.Debug("stopping an earlier attempt's agent", "task", x.spec.ID, "err", err)
		}
	}
	x.report(Progress{Phase: PhaseStarting})
	summary := SummaryPath(x.a.PlanID, x.spec.ID)
	env := make(map[string]string, len(x.spec.Env)+3)
	for k, v := range x.spec.Env {
		env[k] = v
	}
	env[EnvPlan], env[EnvTask], env[EnvSummary] = x.a.PlanID, x.spec.ID, filepath.ToSlash(summary)
	req := StartRequest{
		Kind: x.spec.Kind, EnvironmentID: x.spec.EnvironmentID, Name: x.spec.ID,
		Args: x.spec.Args, Env: env,
	}
	if branch := x.spec.Branch(x.a.PlanID); branch != "" {
		if t.EnvironmentID != "" && t.EnvironmentID != x.spec.EnvironmentID {
			// A retry keeps working in the worktree the first attempt made.
			req.EnvironmentID = t.EnvironmentID
		} else {
			req.Worktree, req.Base = branch, x.spec.Base
		}
	}
	st, err := x.r.agents.Start(ctx, req)
	if err != nil {
		return err
	}
	x.agentID, x.path = st.AgentID, st.Path
	if x.base == "" && st.Path != "" {
		if x.base, err = x.r.work.Head(ctx, st.Path); err != nil {
			x.r.log.Debug("reading a task's base commit", "task", x.spec.ID, "err", err)
		}
	}
	x.report(Progress{
		Phase: PhaseStarted, State: StateRunning, AgentID: st.AgentID, PaneID: st.PaneID,
		EnvironmentID: st.EnvironmentID, Path: st.Path, BaseCommit: x.base,
		Log: fmt.Sprintf("started %s in %s", x.spec.Kind, cmpStr(st.Path, st.EnvironmentID)),
	})
	return nil
}

// prompt waits for the new agent to be ready, then gives it the task. It
// returns an outcome only when the attempt ends here.
func (x *execution) prompt(ctx context.Context) (outcome, reason string, err error) {
	text := x.spec.Prompt
	if !x.spec.NoSummary && x.path != "" {
		summary := filepath.Join(x.path, SummaryPath(x.a.PlanID, x.spec.ID))
		// A summary left from an earlier attempt would be read as this one's.
		_ = os.Remove(summary)
		if err := os.MkdirAll(filepath.Dir(summary), 0o755); err == nil {
			text += summaryRequest(SummaryPath(x.a.PlanID, x.spec.ID))
		}
	}
	for {
		// Ready: idle, past any dialog it opens with (a blocked agent
		// pauses only this task until someone answers it).
		res, err := x.r.agents.Wait(ctx, x.agentID, agent.WaitRequest{Until: agent.UntilIdle, Timeout: x.r.cfg.Ready})
		if err != nil {
			return "failed", "waiting for the agent: " + err.Error(), err
		}
		switch res.Outcome {
		case agent.OutcomeBlocked:
			if _, o, r, err := x.blocked(ctx); o != "" || err != nil {
				return o, r, err
			}
			continue
		case agent.OutcomeExited:
			return "exited", "the agent exited before it was prompted", nil
		case agent.OutcomeTimeout, agent.OutcomeStalled:
			return "failed", fmt.Sprintf("the agent was not ready within %s", x.r.cfg.Ready), nil
		}
		pr, err := x.r.agents.Prompt(ctx, x.agentID, agent.PromptRequest{Text: text, StartTimeout: x.r.cfg.Ready})
		if errors.Is(err, agent.ErrBlocked) {
			continue // it blocked before taking the prompt: wait, then try again
		}
		if err != nil {
			return "failed", "prompting the agent: " + err.Error(), err
		}
		switch pr.Outcome {
		case agent.OutcomeExited:
			return "exited", "the agent exited", nil
		case agent.OutcomeStalled, agent.OutcomeTimeout:
			return "stalled", "the agent did not take the prompt", nil
		}
		// Taken: it works, asks (the wait handles blocked), or is done.
		seq := pr.Agent.CompletionSeq
		if pr.Agent.State == agent.StateDone || pr.Agent.State == agent.StateIdle {
			seq-- // the turn already ended: that completion counts
		}
		x.after = &seq
		x.report(Progress{Phase: PhasePrompted, Log: "prompted"})
		return "", "", nil
	}
}

// await waits for the agent to reach until, counting its working time
// against the task's budget. poll, when set, is checked at least every
// Poll; it ends the wait as met when it reports true.
func (x *execution) await(ctx context.Context, until agent.Until, poll func(context.Context) bool) (outcome, reason string, err error) {
	budget := x.spec.Timeout()
	segment := x.r.cfg.Segment
	if poll != nil {
		segment = x.r.cfg.Poll
	}
	for {
		if poll != nil && poll(ctx) {
			return "met", "", nil
		}
		wait := segment
		if budget > 0 {
			left := budget - x.worked
			if left <= 0 {
				return "timeout", fmt.Sprintf("its %s budget ran out", budget), nil
			}
			wait = min(wait, left)
		}
		req := agent.WaitRequest{Until: until, Timeout: wait, Stall: x.r.cfg.Stall}
		if until == agent.UntilDone {
			req.After = x.after
		}
		began := x.r.cfg.Now()
		res, err := x.r.agents.Wait(ctx, x.agentID, req)
		x.worked += x.r.cfg.Now().Sub(began)
		x.report(Progress{Worked: x.worked})
		if err != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			if errors.Is(err, agent.ErrNotFound) {
				return "exited", "the agent is gone", nil
			}
			return "failed", "waiting for the agent: " + err.Error(), err
		}
		switch res.Outcome {
		case agent.OutcomeReached:
			if poll != nil && !poll(ctx) {
				return "failed", "the agent finished without its screen matching " + x.spec.Match, nil
			}
			return "met", "", nil
		case agent.OutcomeBlocked:
			answered, o, r, err := x.blocked(ctx)
			if o != "" || err != nil {
				return o, r, err
			}
			if poll == nil && until == agent.UntilIdle && x.spec.Until != UntilIdle {
				// Resumed after a restart, there was no completion count to
				// wait past; now there is. An answered agent is mid-turn, and
				// the moment between the answer and the work can read as
				// idle: wait for the turn to end instead.
				seq := answered.CompletionSeq
				if answered.State == agent.StateDone {
					seq-- // it already ended: that completion counts
				}
				until, x.after = agent.UntilDone, &seq
			}
		case agent.OutcomeExited:
			return "exited", "the agent exited", nil
		case agent.OutcomeStalled:
			return "stalled", fmt.Sprintf("the agent's screen did not change for %s", x.r.cfg.Stall), nil
		}
	}
}

// blocked waits, without counting it as work, until a person answers the
// agent, and returns the agent as it was then. It returns an outcome only
// when the attempt ends here.
func (x *execution) blocked(ctx context.Context) (answered agent.Agent, outcome, reason string, err error) {
	x.report(Progress{State: StateBlocked, Log: "the agent waits for a decision"})
	res, err := x.r.agents.Wait(ctx, x.agentID, agent.WaitRequest{Until: agent.UntilUnblocked})
	if err != nil {
		if ctx.Err() != nil {
			return agent.Agent{}, "", "", ctx.Err()
		}
		return agent.Agent{}, "failed", "waiting for the agent: " + err.Error(), err
	}
	if res.Outcome == agent.OutcomeExited || res.Agent.State == agent.StateExited {
		return agent.Agent{}, "exited", "the agent exited while blocked", nil
	}
	x.report(Progress{State: StateRunning, Log: "the agent continues"})
	return res.Agent, "", "", nil
}

// awaitMatch waits for the agent's screen to match the task's pattern.
func (x *execution) awaitMatch(ctx context.Context) (outcome, reason string, err error) {
	re, err := regexp.Compile(x.spec.Match)
	if err != nil {
		return "failed", "match: " + err.Error(), nil
	}
	poll := func(ctx context.Context) bool {
		lines, err := x.r.agents.Read(ctx, x.agentID, MaxScreenLines*4)
		return err == nil && matchLines(re, lines)
	}
	return x.await(ctx, agent.UntilIdle, poll)
}

// matchLines reports whether re matches the lines as one text.
func matchLines(re *regexp.Regexp, lines []string) bool {
	return re.MatchString(strings.Join(lines, "\n"))
}

// check runs the task's command and ends the attempt by its exit status.
func (x *execution) check(ctx context.Context) Outcome {
	timeout := x.r.cfg.CommandTimeout
	if b := x.spec.Timeout(); b > 0 {
		timeout = max(b-x.worked, time.Second)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := map[string]string{EnvPlan: x.a.PlanID, EnvTask: x.spec.ID}
	for k, v := range x.spec.Env {
		env[k] = v
	}
	exit, tail, err := x.r.work.Run(cctx, x.path, x.spec.Command, env)
	if ctx.Err() != nil {
		return x.end(ctx, "", "", ctx.Err())
	}
	res := x.result(ctx)
	res.ExitCode, res.Output = &exit, clip(tail, MaxScreenLines)
	switch {
	case err != nil && cctx.Err() != nil:
		res.Outcome = "timeout"
		return Outcome{State: StateFailed, Result: res, Reason: fmt.Sprintf("%s ran longer than %s", x.spec.Command[0], timeout)}
	case err != nil:
		res.Outcome = "failed"
		return Outcome{State: StateFailed, Result: res, Reason: "the check did not run: " + err.Error()}
	case exit != 0:
		res.Outcome = "failed"
		return Outcome{State: StateFailed, Result: res, Reason: fmt.Sprintf("%s exited %d", x.spec.Command[0], exit)}
	}
	res.Outcome = "met"
	return Outcome{State: StateSucceeded, Result: res}
}

// end turns how the attempt went into its outcome. A context that ended
// for a reason other than a cancellation interrupts it instead.
func (x *execution) end(ctx context.Context, outcome, reason string, err error) Outcome {
	if ctx.Err() != nil {
		if !errors.Is(context.Cause(ctx), ErrCancelled) {
			return Outcome{} // the daemon stops: resume later
		}
		res := x.result(ctx)
		res.Outcome = "cancelled"
		return Outcome{State: StateCancelled, Result: res, Reason: causeText(ctx)}
	}
	if err != nil && reason == "" {
		reason = err.Error()
	}
	res := x.result(ctx)
	res.Outcome = outcome
	if outcome == "met" {
		return Outcome{State: StateSucceeded, Result: res}
	}
	return Outcome{State: StateFailed, Result: res, Reason: reason}
}

// causeText says why a cancelled context ended.
func causeText(ctx context.Context) string {
	if c := context.Cause(ctx); c != nil && c.Error() != ErrCancelled.Error() {
		return strings.TrimPrefix(c.Error(), ErrCancelled.Error()+": ")
	}
	return "cancelled"
}

// collectTimeout bounds gathering an attempt's result.
const collectTimeout = 15 * time.Second

// result gathers what the attempt left: the agent's screen, the files it
// changed and its summary. It works on after the attempt's context ends.
func (x *execution) result(ctx context.Context) *Result {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), collectTimeout)
	defer cancel()
	res := &Result{}
	if x.agentID != "" {
		if lines, err := x.r.agents.Read(ctx, x.agentID, MaxScreenLines); err == nil {
			res.Screen = clip(lines, MaxScreenLines)
		}
	}
	if x.path == "" {
		return res
	}
	if x.base != "" {
		changed, more, err := x.r.work.Changes(ctx, x.path, x.base, MaxChanged)
		if err != nil {
			x.r.log.Debug("listing a task's changes", "task", x.spec.ID, "err", err)
		}
		res.Changed, res.ChangedMore = changed, more
	}
	if !x.spec.NoSummary {
		res.Summary = readSummary(filepath.Join(x.path, SummaryPath(x.a.PlanID, x.spec.ID)))
	}
	return res
}

// readSummary reads at most MaxSummaryBytes of an agent's summary.
func readSummary(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, MaxSummaryBytes))
	return strings.ToValidUTF8(strings.TrimSpace(string(data)), "")
}

// clip keeps the last n lines, each at most maxLineBytes.
func clip(lines []string, n int) []string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if len(l) > maxLineBytes {
			l = strings.ToValidUTF8(l[:maxLineBytes], "")
		}
		out[i] = l
	}
	return out
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
