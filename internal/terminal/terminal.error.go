package terminal

import "errors"

var (
	ErrSessionNotFound = errors.New("terminal session not found")
	ErrSessionExists   = errors.New("terminal session already exists")
	ErrClosed          = errors.New("terminal session closed")
	ErrViewNotFound    = errors.New("terminal view not found")
	// ErrNotAdoptable means the factory's sessions die with the daemon, so
	// there is nothing to reconnect to.
	ErrNotAdoptable = errors.New("terminal sessions of this kind cannot be re-attached after a restart")
	// ErrUnresponsive means the process hosting a session (a shim) is alive
	// but does not answer, so the agent is presumed running: try again
	// later rather than count it gone.
	ErrUnresponsive = errors.New("terminal session host is alive but does not answer")
)
