package runtime

import (
	"errors"
	"path/filepath"
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
