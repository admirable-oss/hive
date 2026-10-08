package process

import "errors"

var (
	ErrNotFound          = errors.New("process not found")
	ErrAlreadyExists     = errors.New("process already exists")
	ErrCommandRequired   = errors.New("command is required")
	ErrNoTerminalSupport = errors.New("terminal sessions are not available")
	ErrInvalidStream     = errors.New(`log stream must be "stdout" or "stderr"`)
	// ErrNoStderr is returned for stderr of a terminal process: a PTY merges
	// stderr into stdout, so there is no separate stream to read.
	ErrNoStderr = errors.New("terminal processes write stderr to stdout; read stdout instead")
)
