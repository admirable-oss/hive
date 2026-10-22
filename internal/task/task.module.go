// Package task runs plans: sets of tasks for coding agents, with
// dependencies, limits, results and approvals.
//
// A task is a prompt for an agent kind, run in an environment or a fresh
// worktree, with a completion condition (the agent's turn ends, it is idle,
// its screen matches, or a command succeeds) and a time budget. A plan is
// tasks whose needs form a DAG; it runs within its own limits, the
// daemon's per-kind caps and the machines' capacity, and a task may wait
// for a person's approval before it starts.
//
// The Service owns every plan's state. It places tasks on machines and
// hands each attempt to that machine's Executor: the Runner here for this
// machine, which drives an agent through the Agents and Workspace ports,
// or (built by the runtime) a client of another machine's daemon, which
// runs the task with its own Runner. Plans are saved as they change, so a
// daemon restart resumes them: agents run under shims and keep working.
package task

import "path/filepath"

// ModuleConfig wires the package.
type ModuleConfig struct {
	// BaseDir is the Hive root; plans are kept in <BaseDir>/tasks.
	BaseDir string
	Service Config
}

// Module is the package's wiring.
type Module struct {
	Service *Service
}

// NewModule builds the service over a filesystem store.
func NewModule(cfg ModuleConfig, machines Machines, events Events) *Module {
	store := NewFilesystemStore(Dir(cfg.BaseDir))
	return &Module{Service: NewService(cfg.Service, store, machines, events)}
}

// Dir is where plans are kept under a Hive root.
func Dir(base string) string { return filepath.Join(base, "tasks") }
