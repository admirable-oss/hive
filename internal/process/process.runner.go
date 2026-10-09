package process

import "context"

// Command is what a Runner launches outside a PTY.
type Command struct {
	// ID is the process ID, for runners that name their sessions.
	ID         string
	Path       string
	Args       []string
	WorkingDir string
	StdoutPath string
	StderrPath string
}

// Handle controls one launched process, PTY-backed or not.
type Handle interface {
	PID() int
	// Wait blocks until the process exits.
	Wait() error
	// Kill asks the process (and everything it spawned) to stop. It does not
	// block; Wait reports when it is gone.
	Kill() error
}

// Runner launches plain (non-PTY) processes.
type Runner interface {
	Start(ctx context.Context, cmd Command) (Handle, error)
}

// Adopter re-attaches to plain processes that outlived an earlier daemon
// (they run under shims). An error with an ExitCode() method means the
// process finished while no daemon was watching.
type Adopter interface {
	Adopt(ctx context.Context, p Process) (Handle, error)
}

// Handles may also implement these to cooperate with a daemon restart:
//
//	Release() // the final state is recorded; free what keeps the process (its shim)
//	Detach()  // the daemon is going away but the process keeps running
type (
	releaser interface{ Release() }
	detacher interface{ Detach() }
)
