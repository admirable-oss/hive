package client

import (
	"context"
	"errors"
	"net"
)

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
	// Capabilities are offered to the daemon in the hello; nil offers all
	// this build supports. Tests set fewer to act as an older client.
	Capabilities []string
	// Dial connects to the daemon instead of SocketPath (a remote daemon,
	// through SSH).
	Dial func(ctx context.Context) (net.Conn, error)
	// Remote marks a slow link: frames come compressed, and the client
	// reports its round trip (and FrameBudget, bytes per second, 0 for
	// none) so the daemon paces what it sends.
	Remote      bool
	FrameBudget int
	// ImageBudget paces image uploads to a remote daemon, in bytes per
	// second (0: no limit).
	ImageBudget int
}

func (c Config) Validate() error {
	if c.SocketPath == "" && c.Dial == nil {
		return ErrInvalidSocketPath
	}
	return nil
}
