package task

import (
	"slices"
	"time"
)

// schema is the plan file format this build writes (and the newest it reads).
const schema = 1

// State is where a task is in its life.
//
//	waiting → (approval →) queued → running ⇄ blocked → succeeded | failed
//
// and any unfinished task may be cancelled. A task whose dependency did not
// succeed is skipped; retrying the dependency brings it back to waiting.
type State string

const (
	StateWaiting   State = "waiting"   // a dependency has not finished
	StateApproval  State = "approval"  // ready, and waiting for a person's yes
	StateQueued    State = "queued"    // ready, and waiting for a free slot
	StateRunning   State = "running"   // its agent works on it
	StateBlocked   State = "blocked"   // its agent waits for a decision; only this task pauses
	StateSucceeded State = "succeeded" // its completion condition was met
	StateFailed    State = "failed"    // it ended without meeting it
	StateCancelled State = "cancelled" // someone cancelled it, or its plan's budget ran out
	StateSkipped   State = "skipped"   // a dependency did not succeed
)

// States lists the task states in the order reports show them.
var States = []State{
	StateBlocked, StateApproval, StateRunning, StateQueued, StateWaiting,
	StateFailed, StateCancelled, StateSkipped, StateSucceeded,
}

// Finished reports whether s is final until someone retries the task.
func (s State) Finished() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled, StateSkipped:
		return true
	}
	return false
}

// Active reports whether a task in s holds a slot: its agent runs.
func (s State) Active() bool { return s == StateRunning || s == StateBlocked }

// Retryable reports whether a task in s may be retried.
func (s State) Retryable() bool {
	return s == StateFailed || s == StateCancelled || s == StateSkipped
}

// PlanState is where a plan is in its life. A plan runs until every task
// is finished; it succeeded when every task did.
type PlanState string

const (
	PlanRunning   PlanState = "running"
	PlanSucceeded PlanState = "succeeded"
	PlanFailed    PlanState = "failed"
	PlanCancelled PlanState = "cancelled"
)

// Finished reports whether the plan has ended.
func (s PlanState) Finished() bool { return s != PlanRunning && s != "" }

// Until is a task's completion condition: what tells Hive the task is done.
type Until string

const (
	// UntilDone (the default) is met when the agent finishes the turn the
	// prompt started.
	UntilDone Until = "done"
	// UntilIdle is met when the agent is ready for its next prompt.
	UntilIdle Until = "idle"
	// UntilMatch is met when the agent's screen matches the task's Match
	// regular expression.
	UntilMatch Until = "match"
	// UntilCommand runs the task's Command in its directory once the agent
	// finishes its turn: exit status 0 succeeds, anything else fails.
	UntilCommand Until = "command"
)

// Untils lists the completion conditions.
var Untils = []Until{UntilDone, UntilIdle, UntilMatch, UntilCommand}

// Spec is what a task asks for. In a plan file each [[task]] table is one,
// with the plan's [defaults] filled in.
type Spec struct {
	// ID names the task within its plan, and in needs.
	ID string `toml:"id" json:"id" desc:"the task's name within its plan (letters, digits, '.', '_', '-')"`
	// Title is a one-line description for people.
	Title string `toml:"title" json:"title,omitempty" desc:"a one-line description"`
	// Prompt is what the agent is asked to do.
	Prompt string `toml:"prompt" json:"prompt" desc:"what the agent is asked to do"`
	// Kind is the agent's manifest ID: claude, codex, …
	Kind string `toml:"kind" json:"kind,omitempty" desc:"the agent: claude, codex, … (see agent.manifests)"`
	// EnvironmentID is the environment the task runs in (or makes a
	// worktree of) on its machine.
	EnvironmentID string `toml:"environment" json:"environment_id,omitempty" desc:"the environment to run in, or to make a worktree of"`
	// Worktree, when set, is a branch: the agent works in a new worktree
	// on it, so tasks running in parallel never share a checkout.
	Worktree string `toml:"worktree" json:"worktree,omitempty" desc:"a branch: run in a new worktree on it"`
	// Isolate runs the task in a new worktree on the branch
	// hive/<plan>/<task> when Worktree does not name one.
	Isolate bool `toml:"isolate" json:"isolate,omitempty" desc:"run in a new worktree on hive/<plan>/<task> unless worktree names a branch"`
	// Base is where a new worktree branch starts (default HEAD).
	Base string `toml:"base" json:"base,omitempty" desc:"where a new worktree branch starts (default HEAD)"`
	// Args are added to the agent's start command; Env to its environment.
	Args []string          `toml:"args" json:"args,omitempty" desc:"extra command-line arguments for the agent"`
	Env  map[string]string `toml:"env" json:"env,omitempty" desc:"extra environment variables for the agent"`
	// Needs lists the tasks that must succeed before this one starts.
	Needs []string `toml:"needs" json:"needs,omitempty" desc:"tasks that must succeed first"`
	// Until is the completion condition (default done).
	Until Until `toml:"until" json:"until,omitempty" desc:"done (default), idle, match or command"`
	// Match is the regular expression UntilMatch looks for.
	Match string `toml:"match" json:"match,omitempty" desc:"with until=match: a regular expression the agent's screen must match"`
	// Command is what UntilCommand runs, as an argument vector.
	Command []string `toml:"command" json:"command,omitempty" desc:"with until=command: the command whose exit status decides"`
	// TimeoutMS bounds the time the task's agent works on it, not counting
	// time blocked on a person; 0 is no bound.
	TimeoutMS int64 `toml:"-" json:"timeout_ms,omitempty" desc:"how long the agent may work on it, in milliseconds (0: no bound)"`
	// Approve stops the task before it starts until a person approves it.
	Approve bool `toml:"approve" json:"approve,omitempty" desc:"wait for a person's approval before starting"`
	// Machine places the task on a saved machine ("local" is this one).
	// Labels place it on any machine with all of them. Neither: anywhere,
	// this machine first.
	Machine string   `toml:"machine" json:"machine,omitempty" desc:"the saved machine to run on (local: this one)"`
	Labels  []string `toml:"labels" json:"labels,omitempty" desc:"run on a machine that has all of these labels"`
	// Retries is how many more times a failed attempt is tried by itself.
	Retries int `toml:"retries" json:"retries,omitempty" desc:"how many times a failed attempt is retried automatically"`
	// NoSummary leaves out the request to write a summary (see
	// EnvSummary), for agents that are not asked in words.
	NoSummary bool `toml:"no_summary" json:"no_summary,omitempty" desc:"do not ask the agent to write a summary"`
	// KeepAgent leaves the agent running after the task ends; by default
	// it is stopped, and its pane keeps its last screen.
	KeepAgent bool `toml:"keep_agent" json:"keep_agent,omitempty" desc:"leave the agent running when the task ends"`
	// Issue is the issue the task works on (tracker:key): its pull
	// request names it, and CloseIssue closes it when its branch merges.
	Issue      string `toml:"issue" json:"issue,omitempty" desc:"the issue it works on, as tracker:key (linear:HIV-42, github:owner/repo#42)"`
	CloseIssue bool   `toml:"close_issue" json:"close_issue,omitempty" desc:"close the issue when the task's branch merges"`
}

// Timeout is the task's time budget; 0 is none.
func (s Spec) Timeout() time.Duration { return time.Duration(s.TimeoutMS) * time.Millisecond }

// Branch is the worktree branch the task runs on; empty when it runs in
// its environment's own directory.
func (s Spec) Branch(planID string) string {
	switch {
	case s.Worktree != "":
		return s.Worktree
	case s.Isolate:
		return "hive/" + planID + "/" + s.ID
	}
	return ""
}

// PlanSpec is a plan as written: its tasks and its limits.
type PlanSpec struct {
	// Name says what the plan is for.
	Name string `json:"name,omitempty" desc:"what the plan is for"`
	// MaxParallel caps how many of its tasks run at once (0: no cap
	// beyond the machines' capacity).
	MaxParallel int `json:"max_parallel,omitempty" desc:"how many of its tasks may run at once (0: as many as the machines allow)"`
	// Kinds caps how many of its tasks run at once per agent kind.
	Kinds map[string]int `json:"kinds,omitempty" desc:"per agent kind, how many of its tasks may run at once"`
	// TimeoutMS bounds the whole plan: when it passes, unfinished tasks
	// are cancelled. 0 is no bound.
	TimeoutMS int64 `json:"timeout_ms,omitempty" desc:"the plan's time budget in milliseconds; unfinished tasks are cancelled when it runs out (0: none)"`
	// Defaults fill in what a task leaves out.
	Defaults *Spec  `json:"defaults,omitempty" desc:"values for what a task leaves out (id, prompt and needs are never taken from here)"`
	Tasks    []Spec `json:"tasks" desc:"the tasks, in any order; needs orders them"`
}

// Timeout is the plan's time budget; 0 is none.
func (p PlanSpec) Timeout() time.Duration { return time.Duration(p.TimeoutMS) * time.Millisecond }

// Plan is a running or finished plan.
type Plan struct {
	Schema int       `json:"schema,omitempty"`
	ID     string    `json:"id"`
	Name   string    `json:"name,omitempty"`
	State  PlanState `json:"state"`
	// Rev increases with every change, so a waiter can tell what it saw.
	Rev         uint64         `json:"rev"`
	MaxParallel int            `json:"max_parallel,omitempty"`
	Kinds       map[string]int `json:"kinds,omitempty"`
	TimeoutMS   int64          `json:"timeout_ms,omitempty"`
	Tasks       []Task         `json:"tasks"`
	CreatedAt   time.Time      `json:"created_at"`
	EndedAt     time.Time      `json:"ended_at,omitzero"`
	// Cancelled says someone cancelled the plan, or its budget ran out;
	// Reason says which.
	Cancelled bool   `json:"cancelled,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Task is one task of a plan: what it asks for and how it went.
type Task struct {
	Spec  Spec  `json:"spec"`
	State State `json:"state"`
	// Attempt counts the times it started (1 for the first). Retried is
	// what Attempt was when a person last retried it: automatic retries
	// count from there.
	Attempt int `json:"attempt,omitempty"`
	Retried int `json:"retried,omitempty"`
	// Machine is where its current or last attempt ran.
	Machine string `json:"machine,omitempty"`
	// Where its agent runs.
	AgentID       string `json:"agent_id,omitempty"`
	PaneID        string `json:"pane_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Path          string `json:"path,omitempty"`
	// BaseCommit is the commit the task's directory had when its first
	// attempt started: its changed files are counted from there.
	BaseCommit string `json:"base_commit,omitempty"`
	// Phase records how far the attempt got, so a daemon restart picks it
	// up without prompting twice. Remote is the plan that runs it on its
	// machine.
	Phase  Phase  `json:"phase,omitempty"`
	Remote string `json:"remote,omitempty"`
	// WorkedMS is how long the agent has worked on the current attempt,
	// not counting time blocked; it outlives a daemon restart.
	WorkedMS  int64     `json:"worked_ms,omitempty"`
	ReadyAt   time.Time `json:"ready_at,omitzero"`
	StartedAt time.Time `json:"started_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	Approval  *Approval `json:"approval,omitempty"`
	Result    *Result   `json:"result,omitempty"`
	// Reason says, in a few words, why it is in its state.
	Reason string `json:"reason,omitempty"`
	// Log is what happened to it, oldest first, at most MaxLog entries.
	Log []LogEntry `json:"log,omitempty"`
}

// Phase is how far an attempt got.
type Phase string

const (
	PhaseStarting Phase = "starting" // the agent is being started
	PhaseStarted  Phase = "started"  // the agent runs, not yet prompted
	PhasePrompted Phase = "prompted" // the prompt went in
	PhaseChecking Phase = "checking" // the turn ended; the condition is being checked
)

// Approval is a person's decision on a task that needs one.
type Approval struct {
	RequestedAt time.Time `json:"requested_at"`
	DecidedAt   time.Time `json:"decided_at,omitzero"`
	Approved    bool      `json:"approved,omitempty"`
	// By says who decided, when the client said.
	By   string `json:"by,omitempty"`
	Note string `json:"note,omitempty"`
}

// Pending reports whether the approval waits for a decision.
func (a *Approval) Pending() bool { return a != nil && a.DecidedAt.IsZero() }

// Result is what an attempt produced.
type Result struct {
	// Outcome is how the attempt ended, in a word: met, timeout, stalled,
	// exited, failed, cancelled, unreachable.
	Outcome string `json:"outcome"`
	// ExitCode is the condition command's exit status (until=command), or
	// the agent's when it exited.
	ExitCode *int `json:"exit_code,omitempty"`
	// Screen is the agent's last lines, at most MaxScreenLines.
	Screen []string `json:"screen,omitempty"`
	// Changed lists the files changed since the task's base, at most
	// MaxChanged; ChangedMore counts the rest.
	Changed     []string `json:"changed,omitempty"`
	ChangedMore int      `json:"changed_more,omitempty"`
	// Summary is what the agent wrote to its summary file, at most
	// MaxSummaryBytes.
	Summary string `json:"summary,omitempty"`
	// Output is the condition command's last lines, at most MaxScreenLines.
	Output []string `json:"output,omitempty"`
}

// LogEntry is one thing that happened to a task.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// Bounds on what a plan keeps, so plans of any length stay small in
// memory, on disk and on the wire.
const (
	MaxTasks        = 500
	MaxLog          = 64
	MaxScreenLines  = 60
	MaxChanged      = 200
	MaxSummaryBytes = 16 << 10
	MaxPromptBytes  = 64 << 10
	maxLineBytes    = 1 << 10
)

// EnvSummary names the variable that tells an agent where to write its
// summary; EnvPlan and EnvTask name its plan and task.
const (
	EnvSummary = "HIVE_TASK_SUMMARY"
	EnvPlan    = "HIVE_PLAN_ID"
	EnvTask    = "HIVE_TASK_ID"
)

// Events, published on the bus as task.*.
const (
	// EventPlan says a plan changed: its Summary.
	EventPlan = "task.plan"
	// EventTask says a task changed: its TaskView.
	EventTask = "task.updated"
	// EventApproval says a task waits for a person: its TaskView. Clients
	// notify on it.
	EventApproval = "task.approval"
	// EventRemoved says finished plans were forgotten: {"ids": […]}.
	EventRemoved = "task.removed"
)

// Summary is a plan without its tasks' details: what lists and events carry.
type Summary struct {
	ID        string        `json:"id"`
	Name      string        `json:"name,omitempty"`
	State     PlanState     `json:"state"`
	Rev       uint64        `json:"rev"`
	Total     int           `json:"total"`
	Counts    map[State]int `json:"counts"`
	CreatedAt time.Time     `json:"created_at"`
	EndedAt   time.Time     `json:"ended_at,omitzero"`
	Reason    string        `json:"reason,omitempty"`
	// Attention lists the tasks waiting for a person: approvals and
	// blocked agents.
	Attention []TaskView `json:"attention,omitempty"`
}

// TaskView is a task without its result and log.
type TaskView struct {
	PlanID string `json:"plan_id"`
	// Rev is the plan's revision the view is from.
	Rev     uint64 `json:"rev"`
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Kind    string `json:"kind,omitempty"`
	State   State  `json:"state"`
	Attempt int    `json:"attempt,omitempty"`
	Machine string `json:"machine,omitempty"`
	// Environment is the one it runs in, or makes its worktree of.
	Environment string    `json:"environment,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	PaneID      string    `json:"pane_id,omitempty"`
	Needs       []string  `json:"needs,omitempty"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	EndedAt     time.Time `json:"ended_at,omitzero"`
	Reason      string    `json:"reason,omitempty"`
}

// View returns t without its result and log.
func (t *Task) View(p *Plan) TaskView {
	return TaskView{
		PlanID: p.ID, Rev: p.Rev, ID: t.Spec.ID, Title: t.Spec.Title, Kind: t.Spec.Kind,
		State: t.State, Attempt: t.Attempt, Machine: t.Machine, Environment: t.Spec.EnvironmentID,
		AgentID: t.AgentID, PaneID: t.PaneID, Needs: t.Spec.Needs,
		StartedAt: t.StartedAt, EndedAt: t.EndedAt, Reason: t.Reason,
	}
}

// Summary returns the plan's summary.
func (p *Plan) Summary() Summary {
	s := Summary{
		ID: p.ID, Name: p.Name, State: p.State, Rev: p.Rev, Total: len(p.Tasks),
		Counts: make(map[State]int, 4), CreatedAt: p.CreatedAt, EndedAt: p.EndedAt, Reason: p.Reason,
	}
	for i := range p.Tasks {
		t := &p.Tasks[i]
		s.Counts[t.State]++
		if t.State == StateApproval || t.State == StateBlocked {
			s.Attention = append(s.Attention, t.View(p))
		}
	}
	return s
}

// Task returns the plan's task called id, nil when there is none.
func (p *Plan) Task(id string) *Task {
	if i := slices.IndexFunc(p.Tasks, func(t Task) bool { return t.Spec.ID == id }); i >= 0 {
		return &p.Tasks[i]
	}
	return nil
}

// Machine is a place tasks run: this machine or a saved one.
type Machine struct {
	Name string `json:"name"`
	// Labels are what tasks' labels select on.
	Labels []string `json:"labels,omitempty"`
	// Capacity caps the tasks running there at once, across plans.
	Capacity int `json:"capacity"`
	// Local is this machine.
	Local bool `json:"local,omitempty"`
}

// LocalMachine is the name of this machine in placements.
const LocalMachine = "local"

// Has reports whether m has every label.
func (m Machine) Has(labels []string) bool {
	for _, l := range labels {
		if !slices.Contains(m.Labels, l) {
			return false
		}
	}
	return true
}
