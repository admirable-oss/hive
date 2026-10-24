package workflow

import (
	"slices"
	"time"
)

// schema is the run and state file format this build writes (and the
// newest it reads).
const schema = 1

// Version is the workflow file format this build reads.
const Version = 1

// Bounds on workflows and on what runs keep, so runs of any length stay
// small in memory, on disk and on the wire.
const (
	MaxFileBytes    = 1 << 20
	MaxSteps        = 200
	MaxTriggers     = 50
	MaxInputs       = 100
	MaxRetries      = 10
	MaxRepeat       = 20
	MaxForeach      = 500
	MaxQueued       = 20
	MaxLog          = 32
	MaxOutputLines  = 60
	MaxOutputsBytes = 64 << 10
	MaxInputBytes   = 64 << 10
	MaxPromptBytes  = 64 << 10
	maxLineBytes    = 1 << 10
	// keptResults is how many of a step's results keep their output lines;
	// older ones keep their outcome and outputs.
	keptResults = 20
)

// Timing defaults.
const (
	DefaultBackoff  = 5 * time.Second
	MaxBackoff      = 10 * time.Minute
	DefaultMaxRuns  = 4
	DefaultKeep     = 200
	DefaultKeepAge  = 30 * 24 * time.Hour
	DefaultInterval = 10 * time.Second
	// MinEvery is the shortest `every`.
	MinEvery = time.Minute
)

// EnvOutput names the file a command writes its outputs to; EnvRun,
// EnvStep and EnvWorkflow name the run, step and workflow.
const (
	EnvOutput   = "HIVE_OUTPUT"
	EnvRun      = "HIVE_WORKFLOW_RUN"
	EnvStep     = "HIVE_WORKFLOW_STEP"
	EnvWorkflow = "HIVE_WORKFLOW"
)

// Concurrency says what a trigger does while a run of its workflow goes
// on.
type Concurrency string

const (
	ConcurrencyQueue    Concurrency = "queue"    // run after it (the default)
	ConcurrencyParallel Concurrency = "parallel" // run now too
	ConcurrencySkip     Concurrency = "skip"     // do not run
)

// Concurrencies lists the values concurrency may take.
var Concurrencies = []Concurrency{ConcurrencyQueue, ConcurrencyParallel, ConcurrencySkip}

// StepKind is what a step runs.
type StepKind string

const (
	KindAgent   StepKind = "agent"   // a task for an agent
	KindCommand StepKind = "command" // a command
	KindAction  StepKind = "action"  // a plugin's action
)

// AnyAgent, as a step's agent, is every installed kind.
const AnyAgent = "any"

// Failure policies: what a failed step does to its run.
const (
	OnFailureFail     = "fail"     // the run fails (the default)
	OnFailureContinue = "continue" // the run goes on as if it succeeded
)

// Spec is a workflow as written, checked and filled in: every step's
// needs are explicit.
type Spec struct {
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// TimeoutMS bounds a run: when it passes, what is left is cancelled.
	TimeoutMS   int64       `json:"timeout_ms,omitempty"`
	Concurrency Concurrency `json:"concurrency,omitempty"`
	// Environment is where steps run unless they say.
	Environment string        `json:"environment,omitempty"`
	Inputs      []InputSpec   `json:"inputs,omitempty"`
	Triggers    []TriggerSpec `json:"triggers,omitempty"`
	Steps       []StepSpec    `json:"steps"`
	// Outputs are evaluated when a run ends.
	Outputs map[string]string `json:"outputs,omitempty"`
}

// Timeout is the run's budget; 0 is none.
func (s Spec) Timeout() time.Duration { return time.Duration(s.TimeoutMS) * time.Millisecond }

// Step returns the step with id, or nil.
func (s *Spec) Step(id string) *StepSpec {
	for i := range s.Steps {
		if s.Steps[i].ID == id {
			return &s.Steps[i]
		}
	}
	return nil
}

// Input returns the input called name, or nil.
func (s *Spec) Input(name string) *InputSpec {
	for i := range s.Inputs {
		if s.Inputs[i].Name == name {
			return &s.Inputs[i]
		}
	}
	return nil
}

// InputSpec declares one input.
type InputSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// TriggerSpec is one [[on]] table: exactly one of Event, Schedule, Every,
// Git and Webhook.
type TriggerSpec struct {
	// Event is a Hive event type, a prefix ending in ".", or an alias
	// (agent.done, agent.blocked, agent.idle, agent.exited); Match holds
	// dotted paths into its data and the values they must have (* globs).
	Event string            `json:"event,omitempty"`
	Match map[string]string `json:"match,omitempty"`
	// Schedule is a cron line; Every an interval in milliseconds.
	Schedule string `json:"schedule,omitempty"`
	EveryMS  int64  `json:"every_ms,omitempty"`
	// Git is push or branch, in Repo (every repository Hive has an
	// environment in when empty), for Branches (globs; all when empty)
	// and, for push, Remote (any when empty).
	Git      string   `json:"git,omitempty"`
	Repo     string   `json:"repo,omitempty"`
	Branches []string `json:"branches,omitempty"`
	Remote   string   `json:"remote,omitempty"`
	// Webhook is the name workflow.fire is called with.
	Webhook string `json:"webhook,omitempty"`
	// Inputs are the runs' inputs, as templates over trigger.
	Inputs map[string]string `json:"inputs,omitempty"`
}

// Trigger types, as runs record them.
const (
	TriggerManual   = "manual"
	TriggerRerun    = "rerun"
	TriggerEvent    = "event"
	TriggerSchedule = "schedule"
	TriggerEvery    = "every"
	TriggerGit      = "git"
	TriggerWebhook  = "webhook"
)

// Git trigger kinds.
const (
	GitPush   = "push"
	GitBranch = "branch"
)

// Type is the trigger's type.
func (t TriggerSpec) Type() string {
	switch {
	case t.Event != "":
		return TriggerEvent
	case t.Schedule != "":
		return TriggerSchedule
	case t.EveryMS > 0:
		return TriggerEvery
	case t.Git != "":
		return TriggerGit
	case t.Webhook != "":
		return TriggerWebhook
	}
	return ""
}

// Every is the interval of an every trigger.
func (t TriggerSpec) Every() time.Duration { return time.Duration(t.EveryMS) * time.Millisecond }

// StepSpec is one [[step]].
type StepSpec struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Needs lists the steps that must finish first (filled in: a step
	// that does not say needs the one before it).
	Needs []string `json:"needs"`
	// If is the condition it runs on (default success()).
	If string `json:"if,omitempty"`

	// An agent step: kinds in order of preference ("any": every installed
	// one), and its task.
	Agent       []string `json:"agent,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	Environment string   `json:"environment,omitempty"`
	Worktree    string   `json:"worktree,omitempty"`
	Isolate     bool     `json:"isolate,omitempty"`
	Base        string   `json:"base,omitempty"`
	Until       string   `json:"until,omitempty"`
	Match       string   `json:"match,omitempty"`
	Check       []string `json:"check,omitempty"`
	Machine     string   `json:"machine,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	KeepAgent   bool     `json:"keep_agent,omitempty"`
	NoSummary   bool     `json:"no_summary,omitempty"`

	// A command: an argv, or a shell line.
	Run   []string          `json:"run,omitempty"`
	Shell string            `json:"shell,omitempty"`
	Dir   string            `json:"dir,omitempty"`
	Env   map[string]string `json:"env,omitempty"`

	// An action: plugin/action, and what it is given.
	Action string            `json:"action,omitempty"`
	With   map[string]string `json:"with,omitempty"`

	TimeoutMS int64  `json:"timeout_ms,omitempty"`
	Retries   int    `json:"retries,omitempty"`
	BackoffMS int64  `json:"backoff_ms,omitempty"`
	OnFailure string `json:"on_failure,omitempty"`

	// Loops: a step runs once per item of Foreach (or of the list
	// ForeachExpr gives), or again until Repeat's condition holds.
	Foreach     []string `json:"foreach,omitempty"`
	ForeachExpr string   `json:"foreach_expr,omitempty"`
	Repeat      *Repeat  `json:"repeat,omitempty"`
}

// Repeat runs a step again until a condition holds.
type Repeat struct {
	Max   int    `json:"max"`
	Until string `json:"until"`
}

// Kind is what the step runs.
func (s StepSpec) Kind() StepKind {
	switch {
	case len(s.Agent) > 0:
		return KindAgent
	case s.Action != "":
		return KindAction
	}
	return KindCommand
}

// Timeout is the step's per-attempt budget; 0 is none.
func (s StepSpec) Timeout() time.Duration { return time.Duration(s.TimeoutMS) * time.Millisecond }

// Backoff is how long the first retry waits.
func (s StepSpec) Backoff() time.Duration {
	if s.BackoffMS > 0 {
		return time.Duration(s.BackoffMS) * time.Millisecond
	}
	return DefaultBackoff
}

// Continue reports whether a failure of the step lets its run go on.
func (s StepSpec) Continue() bool { return s.OnFailure == OnFailureContinue }

// --- Runs ---

// State is where a run is in its life.
//
//	queued → running → succeeded | failed | cancelled
type State string

const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Finished reports whether the run has ended.
func (s State) Finished() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

// StepState is where a step is in its run.
type StepState string

const (
	StepWaiting   StepState = "waiting"   // its needs have not finished
	StepRunning   StepState = "running"   // an attempt runs
	StepRetrying  StepState = "retrying"  // an attempt failed; the next waits its backoff
	StepSucceeded StepState = "succeeded" // done, and it succeeded
	StepFailed    StepState = "failed"    // done, and it failed
	StepSkipped   StepState = "skipped"   // its condition was false
	StepCancelled StepState = "cancelled" // its run was cancelled
)

// Finished reports whether the step has ended.
func (s StepState) Finished() bool {
	switch s {
	case StepSucceeded, StepFailed, StepSkipped, StepCancelled:
		return true
	}
	return false
}

// Outcomes, as expressions and results see them.
const (
	OutcomeSuccess   = "success"
	OutcomeFailure   = "failure"
	OutcomeSkipped   = "skipped"
	OutcomeCancelled = "cancelled"
)

// Outcome is the step's outcome; "" while it is not finished.
func (s StepState) Outcome() string {
	switch s {
	case StepSucceeded:
		return OutcomeSuccess
	case StepFailed:
		return OutcomeFailure
	case StepSkipped:
		return OutcomeSkipped
	case StepCancelled:
		return OutcomeCancelled
	}
	return ""
}

// Run is one run of a workflow: what it ran from, and how it went.
type Run struct {
	Schema int    `json:"schema,omitempty"`
	ID     string `json:"id"`
	// Workflow names it; Path is its file ("" for text given in a
	// request), Digest the SHA-256 of Source, the text it was parsed from.
	Workflow string `json:"workflow"`
	Path     string `json:"path,omitempty"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
	Spec     Spec   `json:"spec"`
	// Inputs are every declared input's value, defaults included.
	Inputs  map[string]string `json:"inputs"`
	Trigger Trigger           `json:"trigger"`
	// Dir is the directory steps run in unless they say.
	Dir   string `json:"dir,omitempty"`
	State State  `json:"state"`
	// Rev increases with every change, so a waiter can tell what it saw.
	Rev       uint64            `json:"rev"`
	Steps     []StepRun         `json:"steps"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	StartedAt time.Time         `json:"started_at,omitzero"`
	EndedAt   time.Time         `json:"ended_at,omitzero"`
	// Cancelled says someone cancelled the run, or its budget ran out;
	// Reason says why it is in its state.
	Cancelled bool   `json:"cancelled,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Step returns the run's step with id, or nil.
func (r *Run) Step(id string) *StepRun {
	for i := range r.Steps {
		if r.Steps[i].ID == id {
			return &r.Steps[i]
		}
	}
	return nil
}

// Trigger is what started a run.
type Trigger struct {
	Type string `json:"type"`
	// Index is the [[on]] table's position, from 0, for a trigger of the
	// workflow's own.
	Index int       `json:"index,omitempty"`
	Time  time.Time `json:"time"`
	// By says who asked, for manual runs when the client said.
	By string `json:"by,omitempty"`
	// From is the run a rerun repeats.
	From string `json:"from,omitempty"`
	// Data is what expressions read as trigger (with type).
	Data map[string]any `json:"data,omitempty"`
}

// StepRun is one step of a run.
type StepRun struct {
	ID    string    `json:"id"`
	Kind  StepKind  `json:"kind"`
	State StepState `json:"state"`
	// Attempt counts the attempts of the current iteration (1 for the
	// first); Attempts every attempt, across iterations.
	Attempt  int `json:"attempt,omitempty"`
	Attempts int `json:"attempts,omitempty"`
	// Iteration is the current item (foreach) or round (repeat), from 0;
	// Items are foreach's items, resolved when the step started.
	Iteration int      `json:"iteration,omitempty"`
	Items     []string `json:"items,omitempty"`
	// Current is the attempt running or last run.
	Current *Result `json:"current,omitempty"`
	// Results holds each finished iteration's last attempt.
	Results []Result `json:"results,omitempty"`
	// NextAt is when a retrying step tries again.
	NextAt    time.Time `json:"next_at,omitzero"`
	StartedAt time.Time `json:"started_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	// Reason says, in a few words, why it is in its state.
	Reason string `json:"reason,omitempty"`
	// Log is what happened to it, oldest first, at most MaxLog entries.
	Log []LogEntry `json:"log,omitempty"`
}

// Last is the step's latest finished result, or nil.
func (s *StepRun) Last() *Result {
	if len(s.Results) == 0 {
		return nil
	}
	return &s.Results[len(s.Results)-1]
}

// Result is what one attempt did.
type Result struct {
	Outcome string `json:"outcome,omitempty"`
	Attempt int    `json:"attempt"`
	// Item is foreach's item, Index its position.
	Item  string `json:"item,omitempty"`
	Index int    `json:"index,omitempty"`
	// Resolved is what the attempt ran, expressions replaced.
	Resolved Resolved `json:"resolved"`
	// Kind is the agent kind chosen; Plan the task plan that ran it.
	Kind string `json:"kind,omitempty"`
	Plan string `json:"plan,omitempty"`
	// ExitCode is a command's or action's exit status.
	ExitCode *int `json:"exit_code,omitempty"`
	// Output is a command's or action's last lines.
	Output []string `json:"output,omitempty"`
	// Outputs are what it wrote to $HIVE_OUTPUT.
	Outputs map[string]string `json:"outputs,omitempty"`
	// Summary, Changed and Screen are an agent's.
	Summary     string    `json:"summary,omitempty"`
	Changed     []string  `json:"changed,omitempty"`
	ChangedMore int       `json:"changed_more,omitempty"`
	Screen      []string  `json:"screen,omitempty"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	EndedAt     time.Time `json:"ended_at,omitzero"`
	Reason      string    `json:"reason,omitempty"`
}

// Resolved is a step's attempt with its expressions replaced: what it
// actually ran.
type Resolved struct {
	Prompt        string            `json:"prompt,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	EnvironmentID string            `json:"environment_id,omitempty"`
	Worktree      string            `json:"worktree,omitempty"`
	Run           []string          `json:"run,omitempty"`
	Shell         string            `json:"shell,omitempty"`
	Dir           string            `json:"dir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Action        string            `json:"action,omitempty"`
	With          map[string]string `json:"with,omitempty"`
}

// LogEntry is one thing that happened to a step.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// --- Definitions ---

// Workflow is a workflow file the daemon knows: its spec (when it parses),
// where it comes from, and whether its triggers may fire.
type Workflow struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Digest      string `json:"digest,omitempty"`
	// Own says it is in the person's own workflows directory; a shared
	// file (from [workflows] paths) fires only while Trusted.
	Own     bool `json:"own,omitempty"`
	Trusted bool `json:"trusted"`
	// Error is why the file does not parse; it never runs then.
	Error    string        `json:"error,omitempty"`
	Triggers []TriggerInfo `json:"triggers,omitempty"`
	Inputs   []InputSpec   `json:"inputs,omitempty"`
	Steps    int           `json:"steps,omitempty"`
	// Spec is the parsed workflow (workflow.get).
	Spec *Spec `json:"spec,omitempty"`
	// Last is its latest run.
	Last *RunSummary `json:"last,omitempty"`
	// Next is when its next scheduled trigger fires.
	Next time.Time `json:"next,omitzero"`
	// Text is the file's text (workflow.get); Source for parse errors.
	Text   string `json:"text,omitempty"`
	source []byte
}

// TriggerInfo describes a trigger in a few words.
type TriggerInfo struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// Fires reports whether the workflow's triggers may fire.
func (w Workflow) Fires() bool { return w.Error == "" && (w.Own || w.Trusted) }

// --- Views and events ---

// RunSummary is a run without its steps' details: what lists and events
// carry.
type RunSummary struct {
	ID        string            `json:"id"`
	Workflow  string            `json:"workflow"`
	State     State             `json:"state"`
	Rev       uint64            `json:"rev"`
	Trigger   string            `json:"trigger"`
	Total     int               `json:"total"`
	Counts    map[StepState]int `json:"counts"`
	Current   []string          `json:"current,omitempty"` // the steps running
	CreatedAt time.Time         `json:"created_at"`
	StartedAt time.Time         `json:"started_at,omitzero"`
	EndedAt   time.Time         `json:"ended_at,omitzero"`
	Reason    string            `json:"reason,omitempty"`
}

// Summary returns the run's summary.
func (r *Run) Summary() RunSummary {
	s := RunSummary{
		ID: r.ID, Workflow: r.Workflow, State: r.State, Rev: r.Rev, Trigger: r.Trigger.Type,
		Total: len(r.Steps), Counts: map[StepState]int{},
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, EndedAt: r.EndedAt, Reason: r.Reason,
	}
	for _, st := range r.Steps {
		s.Counts[st.State]++
		if st.State == StepRunning || st.State == StepRetrying {
			s.Current = append(s.Current, st.ID)
		}
	}
	return s
}

// StepView is a step with its run's identity: what workflow.step events
// carry.
type StepView struct {
	Run      string `json:"run"`
	Workflow string `json:"workflow"`
	StepRun
}

// Events, published on the bus as workflow.*.
const (
	// EventRun says a run changed: its RunSummary.
	EventRun = "workflow.run"
	// EventStep says a step changed: its StepView.
	EventStep = "workflow.step"
	// EventRemoved says finished runs were forgotten: {"ids": […]}.
	EventRemoved = "workflow.removed"
	// EventChanged says workflow files were read again: {"names": […]}.
	EventChanged = "workflow.changed"
)

// clone returns a deep enough copy of r to hand out.
func (r *Run) clone() *Run {
	c := *r
	c.Steps = make([]StepRun, len(r.Steps))
	for i, s := range r.Steps {
		s.Results = slices.Clone(s.Results)
		s.Log = slices.Clone(s.Log)
		s.Items = slices.Clone(s.Items)
		if s.Current != nil {
			cur := *s.Current
			s.Current = &cur
		}
		c.Steps[i] = s
	}
	return &c
}
