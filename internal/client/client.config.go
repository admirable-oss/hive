package client

import "errors"

var (
	ErrInvalidSocketPath = errors.New("client: socket path is required")
	// ErrUnavailable wraps dial failures that mean no daemon is listening
	// (no socket file, or a stale one). Callers may start a daemon and retry.
	ErrUnavailable = errors.New("the hive daemon is not running")
)

type Config struct {
	SocketPath string
}

func (c Config) Validate() error {
	if c.SocketPath == "" {
		return ErrInvalidSocketPath
	}
	return nil
}
