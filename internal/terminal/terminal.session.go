package terminal

import "context"

// Session is a running process with a terminal attached. The PTY is an
// implementation detail behind this contract.
type Session interface {
	// Write sends input to the process, as if typed on a keyboard.
	Write(b []byte) (int, error)
	// Resize changes the terminal dimensions, e.g. after a window resize.
	Resize(size Size) error
	// Wait blocks until the process exits and returns its error.
	Wait() error
	// Pid returns the OS PID of the running process.
	Pid() int
	// Close stops the process and releases the terminal.
	Close() error
	// Subscribe attaches a listener to the live output stream. It returns a
	// channel of output chunks (closed when the session ends), a copy of the
	// recent output history, and a detach function.
	Subscribe() (<-chan []byte, []byte, func())
}

// Factory opens a new terminal session for the given command.
type Factory interface {
	Open(ctx context.Context, cmd Command) (Session, error)
}
