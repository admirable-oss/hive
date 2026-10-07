package process

import "context"

// Command is what a Runner launches outside a PTY.
type Command struct {
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
