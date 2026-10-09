package environment

import "errors"

var (
	ErrNotFound      = errors.New("environment not found")
	ErrAlreadyExists = errors.New("environment already exists")
	ErrInvalidID     = errors.New("invalid environment id")
	ErrInvalidRoot   = errors.New("environment root must be an existing directory given as an absolute path")
	ErrInvalidEnv    = errors.New("invalid environment variable name")
)
