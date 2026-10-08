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
}

type Module struct {
	Service Service
}

func NewModule(cfg Config, envs Environments, terms Terminals) *Module {
	store := NewFilesystemStore(filepath.Join(cfg.BaseDir, "environments"))
	return &Module{Service: NewService(store, envs, NewExecRunner(cfg.StopGrace), terms, WithLogger(cfg.Logger))}
}
