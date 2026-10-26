package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/remote"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/terminal"
)

// Tasks run through the ports package task declares; this file fills
// them in: this machine's agents and files, and the saved machines, whose
// daemons run the tasks placed on them with their own runners.

// TasksConfig is the [tasks] policy, and how saved machines are reached.
// Zero values are the defaults.
type TasksConfig struct {
	// Capacity caps the tasks running on this machine at once (default
	// 8). Labels are what tasks select it by.
	Capacity int
	Labels   []string
	// Keep is how many finished plans are kept (default task.DefaultKeep).
	Keep int
	// Kinds caps running tasks per agent kind, across plans.
	Kinds map[string]int
	// Machines is the saved machines' file; empty places every task here.
	Machines string
	// SSH is the ssh command saved machines are reached with, and
	// ControlDir where shared connections live (as for --machine).
	SSH        []string
	ControlDir string
}

// defaultTaskCapacity is how many tasks run here at once by default.
const defaultTaskCapacity = 8

// tasks is the task service with the machines it runs on.
type tasks struct {
	svc      *task.Service
	machines *taskMachines
}

// newTasks builds the task service over this machine's agents and the
// saved machines. It reads the plans a previous daemon left when it runs.
func newTasks(cfg Config, ops *agentOps, agents *agent.Service, events task.Events, hold func(*task.Plan, *task.Task) string, log *slog.Logger) *tasks {
	runner := task.NewRunner(task.RunnerConfig{Logger: log}, taskAgents{ops: ops, agents: agents}, taskWorkspace{git: git.New()})
	machines := newTaskMachines(cfg.Tasks, runner, log)
	svc := task.NewModule(task.ModuleConfig{
		BaseDir: cfg.root(),
		Service: task.Config{
			Logger: log, Keep: cfg.Tasks.Keep, Kinds: cfg.Tasks.Kinds, Hold: hold,
			BranchName: func(plan, id, kind string) (string, error) {
				return branch.Expand(cfg.Branches.taskPattern(), branch.PatternVars{Plan: plan, Task: id, Kind: kind, Name: id}, time.Now())
			},
		},
	}, machines, events).Service
	return &tasks{svc: svc, machines: machines}
}

// run schedules tasks until the daemon stops, then closes the
// connections to other machines.
func (t *tasks) run(ctx context.Context) {
	defer t.machines.Close()
	t.svc.Run(ctx)
}

// taskAgents runs tasks' agents on this machine.
type taskAgents struct {
	ops    *agentOps
	agents *agent.Service
}

func (a taskAgents) Start(ctx context.Context, req task.StartRequest) (task.Started, error) {
	res, err := a.ops.start(ctx, AgentStartRequest{
		Kind: req.Kind, EnvironmentID: req.EnvironmentID, Worktree: req.Worktree, Base: req.Base,
		Name: req.Name, Args: req.Args, Env: req.Env,
	}, true)
	if err != nil {
		return task.Started{}, err
	}
	return task.Started{
		AgentID: res.Agent.ID, PaneID: res.Pane.ID,
		EnvironmentID: res.Environment.ID, Path: res.Environment.Path,
	}, nil
}

func (a taskAgents) Prompt(ctx context.Context, id string, req agent.PromptRequest) (agent.PromptResult, error) {
	return a.agents.Prompt(ctx, id, req)
}

func (a taskAgents) Wait(ctx context.Context, id string, req agent.WaitRequest) (agent.WaitResult, error) {
	return a.agents.Wait(ctx, id, req)
}

func (a taskAgents) Read(ctx context.Context, id string, lines int) ([]string, error) {
	// Agents leave blank rows between their output and input box; read
	// more than wanted so the compacted tail is full.
	raw, err := a.agents.Read(ctx, id, terminal.ReadRequest{Source: terminal.SourceRecentUnwrapped, Lines: min(lines*3, terminal.MaxReadLines)})
	if err != nil {
		return nil, err
	}
	return agent.Tail(agent.Compact(raw), lines), nil
}

func (a taskAgents) Stop(ctx context.Context, id string) error {
	_, err := a.ops.Stop(ctx, id)
	return err
}

// taskWorkspace reads tasks' directories on this machine and runs their
// condition commands.
type taskWorkspace struct {
	git *git.Git
}

// summaryDir is where agents write their summaries, which are not changes.
var summaryDir = ".hive" + string(filepath.Separator)

func (w taskWorkspace) Head(ctx context.Context, dir string) (string, error) {
	h, err := w.git.Head(ctx, dir)
	if errors.Is(err, git.ErrNotRepository) {
		return "", nil
	}
	return h, err
}

func (w taskWorkspace) Changes(ctx context.Context, dir, base string, limit int) ([]string, int, error) {
	paths, err := w.git.Changed(ctx, dir, base)
	if err != nil {
		return nil, 0, err
	}
	out := paths[:0]
	for _, p := range paths {
		if !strings.HasPrefix(filepath.FromSlash(p), summaryDir) {
			out = append(out, p)
		}
	}
	if len(out) > limit {
		return out[:limit], len(out) - limit, nil
	}
	return out, 0, nil
}

// commandOutputBytes bounds what a condition command's output keeps: its
// last lines are all a task shows.
const commandOutputBytes = 64 << 10

func (w taskWorkspace) Run(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error) {
	return runCheck(ctx, dir, argv, env)
}

// runCheck runs a check (a task's condition, a branch's gate) in dir and
// returns its exit status and its last lines of output. One that runs
// past ctx is stopped with everything it started.
func runCheck(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error) {
	if len(argv) == 0 || dir == "" {
		return -1, nil, errors.New("no command, or no directory to run it in")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out := &tailBuffer{max: commandOutputBytes}
	cmd.Stdout, cmd.Stderr = out, out
	pgroup.Configure(cmd)
	// A check that runs over its time is stopped with everything it
	// started (test runners fork).
	cmd.Cancel = func() error { return pgroup.TerminateAfter(cmd.Process.Pid, time.Second) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return -1, nil, err
	}
	if err := pgroup.Started(cmd); err != nil {
		_ = pgroup.TerminateAfter(cmd.Process.Pid, 0)
		_ = cmd.Wait()
		return -1, nil, err
	}
	err := cmd.Wait()
	pgroup.Release(cmd.Process.Pid)
	lines := agent.Tail(agent.Compact(strings.Split(strings.ToValidUTF8(out.String(), "�"), "\n")), task.MaxScreenLines)
	if ctx.Err() != nil {
		return -1, lines, ctx.Err()
	}
	if _, exited := errors.AsType[*exec.ExitError](err); exited {
		err = nil // the exit status says it
	}
	return cmd.ProcessState.ExitCode(), lines, err
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		return n, nil
	}
	if over := len(b.buf) + len(p) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// taskMachines lists where tasks may run and hands out their executors:
// the runner for this machine, a client of its daemon for a saved one.
type taskMachines struct {
	local    task.Machine
	runner   task.Executor
	registry *remote.Registry
	cfg      TasksConfig
	log      *slog.Logger

	mu      sync.Mutex
	remotes map[string]*remoteTasks
}

func newTaskMachines(cfg TasksConfig, runner task.Executor, log *slog.Logger) *taskMachines {
	capacity := cfg.Capacity
	if capacity <= 0 {
		capacity = defaultTaskCapacity
	}
	m := &taskMachines{
		local:  task.Machine{Name: task.LocalMachine, Labels: cfg.Labels, Capacity: capacity, Local: true},
		runner: runner, cfg: cfg, log: log, remotes: map[string]*remoteTasks{},
	}
	if cfg.Machines != "" {
		m.registry = remote.NewRegistry(cfg.Machines)
	}
	return m
}

// Machines lists this machine, then the enabled saved machines whose
// identity holds.
func (m *taskMachines) Machines(context.Context) ([]task.Machine, error) {
	out := []task.Machine{m.local}
	if m.registry == nil {
		return out, nil
	}
	saved, err := m.registry.List()
	for _, x := range saved {
		if x.Disabled || x.IdentityChanged || x.Name == task.LocalMachine {
			continue
		}
		out = append(out, task.Machine{Name: x.Name, Labels: x.Labels, Capacity: x.TaskCapacity()})
	}
	return out, err
}

// Executor returns the runner for this machine, or a client of a saved
// machine's daemon (one per machine, kept while its address holds).
func (m *taskMachines) Executor(name string) (task.Executor, error) {
	if name == task.LocalMachine {
		return m.runner, nil
	}
	if m.registry == nil {
		return nil, fmt.Errorf("%w: no saved machines here (%s)", task.ErrUnreachable, name)
	}
	x, err := m.registry.Get(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", task.ErrUnreachable, err)
	}
	if x.Disabled || x.IdentityChanged {
		return nil, fmt.Errorf("%w: machine %s is %s", task.ErrUnreachable, name, x.State())
	}
	t := x.Target(m.cfg.ControlDir)
	t.SSH = m.cfg.SSH
	key := strings.Join([]string{t.Host, t.Session, t.HivePath, t.HostKeyAlias}, "\x00")
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.remotes[name]; r != nil {
		if r.key == key {
			return r, nil
		}
		_ = r.conn.Close() // it moved
	}
	c := client.NewService(client.Config{Name: "hive-task", Dial: t.Dial, Remote: true})
	r := &remoteTasks{machine: name, key: key, conn: c, tasks: client.NewTasks(c), log: m.log.With("machine", name)}
	m.remotes[name] = r
	return r, nil
}

// Close closes the connections to saved machines.
func (m *taskMachines) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, r := range m.remotes {
		_ = r.conn.Close()
		delete(m.remotes, name)
	}
}

// remoteTasks runs attempts on a saved machine: its daemon runs each as a
// plan of one task, with its own runner, so the agent, its worktree, its
// files and its check are all there. Its progress is mirrored here.
type remoteTasks struct {
	machine string
	key     string
	conn    client.Client
	tasks   client.Tasks
	log     *slog.Logger
}

const (
	// remotePoll bounds one wait on a remote plan.
	remotePoll = 30 * time.Second
	// remoteUnreachable is how long a machine may stay unreachable before
	// the task running there fails. Its agent keeps working there.
	remoteUnreachable = 5 * time.Minute
)

func (r *remoteTasks) Execute(ctx context.Context, a task.Attempt, progress func(task.Progress)) task.Outcome {
	id := ""
	if a.Resume {
		id = a.Task.Remote
	}
	since := time.Now()
	if id == "" {
		spec := remoteSpec(a)
		var plan task.Plan
		err := r.retry(ctx, func(ctx context.Context) (err error) {
			plan, err = r.tasks.Run(ctx, task.PlanSpec{Name: a.PlanName + " · " + spec.ID + " (from " + a.PlanID + ")", Tasks: []task.Spec{spec}})
			return err
		})
		if err != nil {
			return r.stopped(ctx, "", err, progress)
		}
		id = plan.ID
		since = plan.CreatedAt
		progress(task.Progress{Remote: id, Log: fmt.Sprintf("running on %s as its plan %s", r.machine, id)})
	}
	var rev uint64
	for {
		var res task.WaitResult
		err := r.retry(ctx, func(ctx context.Context) (err error) {
			res, err = r.tasks.Wait(ctx, id, client.TaskWaitOptions{Until: task.WaitChange, After: rev, Timeout: remotePoll})
			return err
		})
		if err != nil {
			return r.stopped(ctx, id, err, progress)
		}
		if len(res.Plan.Tasks) != 1 {
			return task.Outcome{State: task.StateFailed, Reason: fmt.Sprintf("plan %s on %s is not one task", id, r.machine)}
		}
		rev = res.Plan.Rev
		t := res.Plan.Tasks[0]
		pr := task.Progress{
			AgentID: t.AgentID, PaneID: t.PaneID, EnvironmentID: t.EnvironmentID, Path: t.Path,
			BaseCommit: t.BaseCommit, Worked: time.Duration(t.WorkedMS) * time.Millisecond,
		}
		if t.State.Active() {
			pr.State = t.State
		}
		progress(pr)
		for _, e := range t.Log {
			if e.Time.After(since) {
				progress(task.Progress{Log: r.machine + ": " + e.Message})
				since = e.Time
			}
		}
		if res.Plan.State.Finished() {
			state := t.State
			if state == task.StateSkipped {
				state = task.StateFailed
			}
			return task.Outcome{State: state, Result: t.Result, Reason: t.Reason}
		}
	}
}

// remoteSpec is the task as the machine runs it: alone, already approved
// and placed, on the branch this plan names.
func remoteSpec(a task.Attempt) task.Spec {
	s := a.Task.Spec
	s.Worktree, s.Isolate = s.Branch(a.PlanID), false
	s.Machine, s.Labels, s.Needs, s.Approve, s.Retries = "", nil, nil, false, 0
	return s
}

// stopped ends an attempt whose context ended or whose machine failed it.
// A cancelled attempt cancels the remote plan too; an interrupted one
// leaves it running, to be picked up by the next daemon.
func (r *remoteTasks) stopped(ctx context.Context, id string, err error, progress func(task.Progress)) task.Outcome {
	if ctx.Err() == nil {
		reason := err.Error()
		if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeUnknownMethod {
			reason = fmt.Sprintf("the hive on %s does not run tasks; update it", r.machine)
		}
		return task.Outcome{State: task.StateFailed, Reason: reason, Result: &task.Result{Outcome: "unreachable"}}
	}
	cause := context.Cause(ctx)
	switch {
	case !errors.Is(cause, task.ErrCancelled):
		return task.Outcome{} // the daemon stops: the remote plan goes on
	case id == "":
		return task.Outcome{State: task.StateCancelled, Reason: "cancelled", Result: &task.Result{Outcome: "cancelled"}}
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if _, err := r.tasks.Cancel(bg, id, ""); err != nil && !isCode(err, protocol.ErrorCodeConflict) {
		r.log.Warn("cancelling a task on its machine", "plan", id, "err", err)
	}
	out := task.Outcome{State: task.StateCancelled, Reason: strings.TrimPrefix(cause.Error(), task.ErrCancelled.Error()+": "), Result: &task.Result{Outcome: "cancelled"}}
	if res, err := r.tasks.Wait(bg, id, client.TaskWaitOptions{Timeout: 30 * time.Second}); err == nil && len(res.Plan.Tasks) == 1 && res.Plan.Tasks[0].Result != nil {
		out.Result = res.Plan.Tasks[0].Result
	}
	progress(task.Progress{Log: "cancelled on " + r.machine})
	return out
}

func isCode(err error, code string) bool {
	pe, ok := errors.AsType[*protocol.Error](err)
	return ok && pe.Code == code
}

// retry calls f until it succeeds, fails for good (the machine answered
// with an error), or the machine stays unreachable for remoteUnreachable.
func (r *remoteTasks) retry(ctx context.Context, f func(context.Context) error) error {
	var since time.Time
	backoff := time.Second
	for {
		err := f(ctx)
		if err == nil || ctx.Err() != nil {
			return err
		}
		if _, answered := errors.AsType[*protocol.Error](err); answered {
			return err
		}
		if since.IsZero() {
			since = time.Now()
			r.log.Warn("machine unreachable; trying again", "err", err)
		}
		if time.Since(since) > remoteUnreachable {
			return fmt.Errorf("%w: %s for %s: %w", task.ErrUnreachable, r.machine, remoteUnreachable, err)
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
