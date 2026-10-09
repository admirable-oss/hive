package config

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Config is the effective configuration. Zero values are never meaningful;
// always start from Defaults.
type Config struct {
	Daemon    Daemon
	Log       Log
	Process   Process
	Terminal  Terminal
	Git       Git
	Worktrees Worktrees
}

type Daemon struct {
	// Autostart lets client commands start the daemon when it is not running.
	Autostart bool
	// ShutdownTimeout bounds how long a stop waits for agents to exit.
	ShutdownTimeout time.Duration
}

type Log struct {
	Level      string // debug | info | warn | error
	Format     string // text | json
	MaxSizeMB  int
	MaxBackups int
}

type Process struct {
	// StopGrace is the SIGTERM → SIGKILL delay when stopping an agent.
	StopGrace time.Duration
}

type Terminal struct {
	// DefaultWidth and DefaultHeight size a new agent terminal when the
	// caller does not.
	DefaultWidth  int
	DefaultHeight int
	// ScrollbackMB bounds each agent's scrollback (lines that scrolled off
	// its screen). 0 keeps none.
	ScrollbackMB int
	// Shell is the command line a new pane runs when none is given, split
	// on spaces. Empty means $SHELL as a login shell.
	Shell string
}

type Git struct {
	// RefreshInterval is how often environments' git status is re-read,
	// besides changes noticed in the repository itself.
	RefreshInterval time.Duration
}

type Worktrees struct {
	// Directory holds worktrees created by `hive worktree create`, as
	// <directory>/<repo>/<branch>. Empty means <HIVE_HOME>/worktrees.
	Directory string
}

// Defaults returns the built-in configuration.
func Defaults() Config {
	return Config{
		Daemon:   Daemon{Autostart: true, ShutdownTimeout: 15 * time.Second},
		Log:      Log{Level: "info", Format: "text", MaxSizeMB: 10, MaxBackups: 3},
		Process:  Process{StopGrace: 3 * time.Second},
		Terminal: Terminal{DefaultWidth: 220, DefaultHeight: 50, ScrollbackMB: 10},
		Git:      Git{RefreshInterval: 5 * time.Second},
	}
}

// ShellArgv is Terminal.Shell as an argv (nil for the default).
func (c Config) ShellArgv() []string {
	if c.Terminal.Shell == "" {
		return nil
	}
	return strings.Fields(c.Terminal.Shell)
}

// WorktreeDir resolves Worktrees.Directory: "~/" is the home directory,
// empty is base/worktrees.
func (c Config) WorktreeDir(home, base string) string {
	d := c.Worktrees.Directory
	switch {
	case d == "":
		return filepath.Join(base, "worktrees")
	case d == "~":
		return home
	case strings.HasPrefix(d, "~/"):
		return filepath.Join(home, d[2:])
	}
	return d
}

// LoggingConfig converts the [log] section for package logging. The level
// and format are validated during loading, so parsing cannot fail here.
func (c Config) LoggingConfig(path string, stderr bool) logging.Config {
	level, _ := logging.ParseLevel(c.Log.Level)
	format, _ := logging.ParseFormat(c.Log.Format)
	return logging.Config{
		Level:        level,
		Format:       format,
		Path:         path,
		MaxSizeBytes: int64(c.Log.MaxSizeMB) << 20,
		MaxBackups:   c.Log.MaxBackups,
		Stderr:       stderr,
	}
}
