package runtime

import (
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/session"
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
	// Shim runs every agent under its own shim process, so agents outlive
	// the daemon (see package shim). Nil runs agents inside the daemon,
	// which tests use.
	Shim *ShimConfig

	// Session is the name of the session this daemon serves (see package
	// session); agents see it as HIVE_SESSION. Empty means the default.
	Session string
	// Home is the directory holding every session (HIVE_HOME for agents,
	// so the hive they run finds this daemon). Empty leaves it unset.
	Home string
	// Bin is the hive binary agents can call back (HIVE_BIN). Empty means
	// the shim executable, or none.
	Bin string
	// Shell runs in panes created without a command. Empty means $SHELL.
	Shell []string
	// WorktreeDir holds the worktrees `worktree.create` makes, as
	// <WorktreeDir>/<repo>/<branch>. Empty means <root>/worktrees.
	WorktreeDir string
	// GitInterval is how often environments' git status is refreshed
	// besides filesystem notifications. Zero means 5s.
	GitInterval time.Duration
	// AgentManifestDir holds the user's agent manifests, which add to or
	// replace the built-in ones (~/.config/hive/agent-detection). Empty
	// means only the built-in ones.
	AgentManifestDir string
	// InheritEnv lets agents inherit the daemon's environment unfiltered,
	// without the HIVE_* variables. Only tests set it.
	InheritEnv bool
}

// ShimConfig says how to start a shim: Exe Args… <dir>, with Env added to
// the daemon's environment.
type ShimConfig struct {
	Exe  string
	Args []string
	Env  []string
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

func (c Config) worktreeDir() string {
	if c.WorktreeDir != "" {
		return c.WorktreeDir
	}
	return filepath.Join(c.root(), "worktrees")
}

func (c Config) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	if c.Shim != nil {
		return c.Shim.Exe
	}
	return ""
}

// launchEnv is what every agent's environment is built from: the daemon's
// (minus outer multiplexer and terminal variables) and the variables that
// let an agent drive its own session.
func (c Config) launchEnv() *process.LaunchEnv {
	if c.InheritEnv {
		return nil
	}
	vars := map[string]string{
		"HIVE_SOCKET_PATH": c.SocketPath,
		"HIVE_SESSION":     session.Normalize(c.Session),
	}
	if bin := c.bin(); bin != "" {
		vars["HIVE_BIN"] = bin
	}
	if c.Home != "" {
		vars["HIVE_HOME"] = c.Home
	}
	l := process.DaemonLaunchEnv(vars)
	return &l
}

func (c Config) listener() ListenerFactory {
	if c.Listener != nil {
		return c.Listener
	}
	return NetListenerFactory{}
}
