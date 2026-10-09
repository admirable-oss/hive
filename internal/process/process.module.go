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
	if cfg.Adopter != nil {
		opts = append(opts, WithAdopter(cfg.Adopter))
	}
	if cfg.Events != nil {
		opts = append(opts, WithEvents(cfg.Events))
	}
	return &Module{Service: NewService(store, envs, runner, terms, opts...)}
}
