package terminal

import "errors"

var (
	ErrSessionNotFound = errors.New("terminal session not found")
	ErrSessionExists   = errors.New("terminal session already exists")
	ErrClosed          = errors.New("terminal session closed")
)
