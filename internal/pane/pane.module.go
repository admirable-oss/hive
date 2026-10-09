// Package pane is the workspace model inside an environment: tabs, each a
// tiling layout (package layout) of panes, plus floating popups. A pane
// shows one terminal process; the process itself is supervised by package
// process, and its screen lives in package terminal (in a shim).
//
// Tabs and panes are kept per environment in environments/<id>/layout.json,
// so a daemon restart finds the same layout around the same (adopted)
// agents. The pane API (split, focus, resize, zoom, swap, move, close,
// input, send-keys, run, read, wait-output) and layout export/apply are
// what scripts and agents use to drive a workspace.
package pane

import (
	"log/slog"
	"path/filepath"

	"github.com/admirable-oss/hive/internal/terminal"
)

// Config tunes the pane service.
type Config struct {
	// BaseDir is the storage root; layouts go under BaseDir/environments.
	BaseDir string
	// Size is a new tab's area until a client reports its own.
	Size terminal.Size
	// Shell runs in panes created without a command (argv). Empty means
	// $SHELL as a login shell, or /bin/sh.
	Shell  []string
	Logger *slog.Logger
	// WorkingDir reads a process's working directory, for new panes that
	// start where the pane they come from is. Nil means
	// platform.ProcessCwd; tests replace it.
	WorkingDir func(pid int) (string, error)
}

type Module struct {
	Service *Service
}

func NewModule(cfg Config, procs Processes, terms Terminals, envs Environments, events Events) *Module {
	store := NewFilesystemStore(filepath.Join(cfg.BaseDir, "environments"))
	return &Module{Service: NewService(cfg, store, procs, terms, envs, events)}
}
