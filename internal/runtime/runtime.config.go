package runtime

import "os"
import "path/filepath"

type Config struct {
	SocketPath string
	BaseDir    string // Storage root. Defaults to ~/.hive if empty.
	Listener   ListenerFactory
}

func (c *Config) baseDir() string {
	if c.BaseDir != "" {
		return c.BaseDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hive")
}

func (c Config) Validate() error {
	if c.SocketPath == "" {
		return ErrInvalidSocketPath
	}

	if c.Listener == nil {
		return ErrMissingListener
	}

	return nil
}
