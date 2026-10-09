package pane

import "errors"

var (
	ErrTabNotFound  = errors.New("tab not found")
	ErrPaneNotFound = errors.New("pane not found")
	ErrInvalid      = errors.New("invalid request")
	ErrNotRunning   = errors.New("the pane's process is not running")
)
