package insights

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound means no agent or budget has that name.
	ErrNotFound = errors.New("insights: not found")
	// ErrInvalid means a request is malformed.
	ErrInvalid = errors.New("insights: invalid")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
