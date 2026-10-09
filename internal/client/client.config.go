package client

import "errors"

var (
	ErrInvalidSocketPath = errors.New("client: socket path is required")
	// ErrUnavailable wraps dial failures that mean no daemon is listening
	// (no socket file, or a stale one). Callers may start a daemon and retry.
	ErrUnavailable = errors.New("the hive daemon is not running")
	// ErrDaemonTooOld is returned for features an older daemon (protocol 1
	// only) does not have; `hive daemon restart` fixes it.
	ErrDaemonTooOld = errors.New("the running hive daemon is older than this CLI; restart it with `hive daemon restart`")
)

type Config struct {
	SocketPath string
	// Name identifies this client to the daemon (e.g. "cli", "tui").
	Name string
}

func (c Config) Validate() error {
	if c.SocketPath == "" {
		return ErrInvalidSocketPath
	}
	return nil
}
