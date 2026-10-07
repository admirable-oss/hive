package environment

import "errors"

var (
	ErrNotFound      = errors.New("environment not found")
	ErrAlreadyExists = errors.New("environment already exists")
	ErrInvalidID     = errors.New("invalid environment id")
)
