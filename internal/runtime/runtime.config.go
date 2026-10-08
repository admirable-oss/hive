package runtime

import (
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
)

var (
	ErrInvalidSocketPath = errors.New("runtime: socket path is required")
	ErrAlreadyRunning    = errors.New("runtime: another hive daemon is already running")
)

type Config struct {
	SocketPath string
	// BaseDir is the storage root. Empty means the socket's directory, so a
	// socket at ~/.hive/hive.sock keeps its data under ~/.hive.
	BaseDir string
	// Listener opens the socket. Nil means the real network stack.
	Listener ListenerFactory
	// Logger receives the daemon's structured logs. Nil discards them.
	Logger *slog.Logger
	// Terminal tunes agent PTYs (its Logger is ignored; Logger is used).
	Terminal terminal.Config
	// StopGrace is the SIGTERM → SIGKILL delay for plain processes.
	StopGrace time.Duration
}

func (c Config) Validate() error {
	if c.SocketPath == "" {
		return ErrInvalidSocketPath
	}
	return nil
}

func (c Config) root() string {
	if c.BaseDir != "" {
		return c.BaseDir
	}
	return filepath.Dir(c.SocketPath)
}

func (c Config) listener() ListenerFactory {
	if c.Listener != nil {
		return c.Listener
	}
	return NetListenerFactory{}
}
