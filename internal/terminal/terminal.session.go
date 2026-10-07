package terminal

import "context"

// Session is the interface Hive uses to interact with a running process
// that has a terminal attached. The PTY is an implementation detail.
type Session interface {
	// Read reads terminal output (from the process).
	Read(b []byte) (int, error)
	// Write sends input to the process (as if typed on a keyboard).
	Write(b []byte) (int, error)
	// Resize changes the terminal dimensions, e.g. after a window resize.
	Resize(size Size) error
	// Wait blocks until the process exits and returns its error.
	Wait() error
	// Pid returns the OS PID of the running process.
	Pid() int
	// Close terminates the session and cleans up.
	Close() error
	// Subscribe attaches a listener to the live terminal output stream.
	// Returns a channel receiving output chunks, the recent output history,
	// and a detach function.
	Subscribe() (<-chan []byte, []byte, func())
}

// Factory opens a new terminal session for the given command.
type Factory interface {
	Open(ctx context.Context, cmd Command) (Session, error)
}
