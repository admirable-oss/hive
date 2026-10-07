package runtime

import "errors"

var (
	ErrInvalidSocketPath = errors.New(
		"runtime: socket path is required",
	)

	ErrMissingListener = errors.New(
		"runtime: listener factory is required",
	)
)
