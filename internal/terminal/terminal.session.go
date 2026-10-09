package terminal

import (
	"context"

	"github.com/admirable-oss/hive/internal/vt"
)

// Session is a running process with a terminal attached. The PTY and the
// emulator are implementation details behind this contract; a session may
// live in this process or in a shim (package shim).
type Session interface {
	// Write sends input to the process, as if typed on a keyboard.
	Write(b []byte) (int, error)
	// Resize changes the terminal dimensions.
	Resize(size Size) error
	// Size returns the current dimensions.
	Size() Size
	// Wait blocks until the process exits and returns its error.
	Wait() error
	// Pid returns the OS PID of the running process.
	Pid() int
	// Close stops the process and releases the terminal.
	Close() error
	// Snapshot returns the current screen.
	Snapshot(ctx context.Context) (*vt.Screen, error)
	// Scrollback returns up to n of the newest lines that scrolled off the
	// top of the screen, oldest first, as plain text or with ANSI styling.
	Scrollback(ctx context.Context, n int, ansi bool) ([]string, error)
	// Frames calls emit with the screen as frames: a keyframe first, then
	// changes. It returns once the session's output has ended (after a
	// final frame), when ctx ends, or when emit fails. A slow emit gets
	// fewer, larger frames, never a gap: each frame is computed from the
	// screen as it is when the previous one was delivered.
	Frames(ctx context.Context, emit func(*vt.Frame) error) error
}

// Factory opens a new terminal session for the given command.
type Factory interface {
	Open(ctx context.Context, cmd Command) (Session, error)
}

// Adopter is implemented by factories whose sessions outlive the daemon: it
// reconnects to the session with the given ID after a daemon restart.
type Adopter interface {
	Adopt(ctx context.Context, id string) (Session, error)
}
