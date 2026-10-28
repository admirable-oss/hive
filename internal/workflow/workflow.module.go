// Package workflow runs workflows: reusable steps (agents' tasks, commands
// and plugin actions) with inputs, outputs, conditions and loops, started
// by triggers (Hive events, schedules, git pushes and branches, webhooks)
// or on request.
//
// Workflows are TOML files: the person's own (<home>/workflows), and
// shared ones ([workflows] paths), which fire only once trusted at their
// current text. The Service keeps every run's state: it records the
// workflow's text, the inputs and the trigger, runs steps as their needs
// finish, retries with backoff, enforces budgets, and saves runs as they
// change, so a daemon restart resumes them. Agent steps are tasks of
// package task, whose agents outlive the daemon; commands interrupted by a
// restart run again.
package workflow

import (
	"context"
	"path/filepath"

	"github.com/admirable-oss/hive/internal/task"
)

// Tasks runs agent steps as plans of one task (package task's service).
type Tasks interface {
	Submit(ctx context.Context, spec task.PlanSpec) (task.Plan, error)
	Wait(ctx context.Context, req task.WaitRequest) (task.WaitResult, error)
	Cancel(ctx context.Context, id, taskID string) (task.Plan, error)
}

// Commands runs command steps: argv in dir with env added, its exit
// status and last lines of output. One that runs past ctx is stopped with
// everything it started.
type Commands interface {
	Run(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error)
}

// Actions runs plugins' actions.
type Actions interface {
	Action(ctx context.Context, req ActionRequest) (ActionResult, error)
}

// ActionRequest is an action step's attempt.
type ActionRequest struct {
	Plugin, Action string
	// With is what the step gives the action; Output the file it may write
	// outputs to.
	With   map[string]string
	Output string
	// Workflow, Run and Step say where it comes from; Environment where it
	// runs, when the step names one.
	Workflow, Run, Step string
	Environment         string
}

// ActionResult is how an action ended.
type ActionResult struct {
	ExitCode int
	Output   string
}

// Steps runs plugins' steps (plugin API 2): typed inputs in, typed
// outputs back.
type Steps interface {
	Step(ctx context.Context, req StepRequest) (StepResult, error)
	// Check checks a workflow's use of a step before it runs: the step
	// exists and its inputs that are not templates fit their types.
	Check(plugin, step string, with map[string]string) error
}

// StepRequest is a plugin step's attempt: With are its inputs as the
// workflow resolved them (text, read back by the step's types).
type StepRequest struct {
	Plugin, Step      string
	With              map[string]string
	Workflow, Run, ID string
	Environment       string
}

// StepResult is what a plugin step answered: its outputs (as text) and a
// summary.
type StepResult struct {
	Outputs map[string]string
	Output  string
}

// Workspace is what steps and triggers read of the machine's
// environments and repositories.
type Workspace interface {
	// EnvironmentDir is an environment's directory.
	EnvironmentDir(ctx context.Context, id string) (string, error)
	// EnvironmentFor returns an environment for dir, creating one when no
	// environment has that directory.
	EnvironmentFor(ctx context.Context, dir string) (string, error)
	// Repos lists the repositories Hive has environments in (their main
	// worktrees' roots).
	Repos(ctx context.Context) ([]string, error)
	// RepoRoot is the main worktree of the repository dir is in, "" when
	// it is in none.
	RepoRoot(ctx context.Context, dir string) (string, error)
	// Refs reads a repository's local and remote-tracking branches:
	// refs/heads/… and refs/remotes/… with their commits.
	Refs(ctx context.Context, repo string) (map[string]string, error)
}

// Agents says which agent kinds this machine can start, in the order
// `hive agent manifests` lists them.
type Agents interface {
	Installed() []string
}

// Events publishes workflow.* events.
type Events interface {
	Publish(typ string, data any)
}

// Ports are everything the service needs from the rest of the daemon.
type Ports struct {
	Tasks     Tasks
	Commands  Commands
	Actions   Actions
	Steps     Steps
	Workspace Workspace
	Agents    Agents
	Events    Events
}

// ModuleConfig wires the package.
type ModuleConfig struct {
	// BaseDir is the session's root: runs are kept in <BaseDir>/workflows.
	BaseDir string
	Service Config
}

// Module is the package's wiring.
type Module struct {
	Service *Service
}

// NewModule builds the service over a filesystem store.
func NewModule(cfg ModuleConfig, ports Ports) *Module {
	return &Module{Service: NewService(cfg.Service, NewFilesystemStore(Dir(cfg.BaseDir)), ports)}
}

// Dir is where workflows' state is kept under a Hive root, and where the
// default session's own workflow files are.
func Dir(base string) string { return filepath.Join(base, "workflows") }
