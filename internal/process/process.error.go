package process

import "errors"

var (
	ErrNotFound      = errors.New("process not found")
	ErrAlreadyExists = errors.New("process already exists")
	ErrInvalidID     = errors.New("invalid process id")
)
