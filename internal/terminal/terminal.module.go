// Package terminal runs commands inside pseudo-terminals so agents that expect
// a real TTY (interactive CLIs, prompts, colours) behave as they would for a
// human. It owns live sessions only; process lifecycle lives in package process.
package terminal

import (
	"log/slog"
	"time"
)

// Config tunes the PTY sessions the module opens. Zero values use defaults.
type Config struct {
	DefaultSize  Size
	HistoryBytes int
	StopGrace    time.Duration
	Logger       *slog.Logger
}

type Module struct {
	Service Service
}

func NewModule(cfg Config) *Module {
	return &Module{Service: NewService(PTYFactory{
		Size:         cfg.DefaultSize,
		HistoryBytes: cfg.HistoryBytes,
		StopGrace:    cfg.StopGrace,
		Logger:       cfg.Logger,
	})}
}
