package process

import "errors"

var (
	ErrNotFound          = errors.New("process not found")
	ErrAlreadyExists     = errors.New("process already exists")
	ErrCommandRequired   = errors.New("command is required")
	ErrNoTerminalSupport = errors.New("terminal sessions are not available")
)
