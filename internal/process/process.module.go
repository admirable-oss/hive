// Package process supervises agents: it launches them inside an environment's
// workspace (plainly or in a PTY), records their lifecycle, and stops them.
// It depends on small ports only: Store, Runner, Environments and Terminals.
package process

import (
	"log/slog"
	"path/filepath"
	"time"
)

// Config locates process storage under the Hive root directory.
type Config struct {
	BaseDir string
	// StopGrace is the SIGTERM → SIGKILL delay for plain processes. Zero
	// means pgroup.Grace. PTY processes take theirs from the terminal module.
	StopGrace time.Duration
	Logger    *slog.Logger
	// Runner launches plain processes; nil runs them in the daemon's process
	// (they then die with it). Adopter re-attaches to them after a restart.
	Runner  Runner
	Adopter Adopter
	Events  Events
	// LaunchEnv builds every process's environment; nil inherits the
	// daemon's unfiltered (tests only).
	LaunchEnv *LaunchEnv
	// BootTime reports when the host booted, so recovery can tell agents
	// that went down with the host; nil means platform.BootTime.
	BootTime func() (time.Time, error)
	// LogMaxBytes rotates each terminal process's output log past this
	// size, keeping one older file. Zero lets logs grow.
	LogMaxBytes int64
	// LogGraphics keeps image data (Kitty graphics) in output logs.
	LogGraphics bool
}

type Module struct {
	Service Service
}

func NewModule(cfg Config, envs Environments, terms Terminals) *Module {
	store := NewFilesystemStore(filepath.Join(cfg.BaseDir, "environments"))
	runner := cfg.Runner
	if runner == nil {
		runner = NewExecRunner(cfg.StopGrace)
	}
	opts := []Option{WithLogger(cfg.Logger)}
	if cfg.LogGraphics {
		opts = append(opts, WithLogGraphics())
	}
	if cfg.LogMaxBytes > 0 {
		opts = append(opts, WithLogLimit(cfg.LogMaxBytes))
	}
	if cfg.BootTime != nil {
		opts = append(opts, WithBootTime(cfg.BootTime))
	}
	if cfg.Adopter != nil {
		opts = append(opts, WithAdopter(cfg.Adopter))
	}
	if cfg.LaunchEnv != nil {
		opts = append(opts, WithLaunchEnv(*cfg.LaunchEnv))
	}
	if cfg.Events != nil {
		opts = append(opts, WithEvents(cfg.Events))
	}
	return &Module{Service: NewService(store, envs, runner, terms, opts...)}
}
