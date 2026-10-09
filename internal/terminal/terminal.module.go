// Package terminal runs commands inside pseudo-terminals so agents that expect
// a real TTY (interactive CLIs, prompts, colours) behave as they would for a
// human. Each session emulates its terminal (package vt), so any number of
// clients can view it, join late and still see the exact screen.
//
// The daemon's Service tracks live sessions and the clients viewing them; it
// owns sessions but not processes, whose lifecycle lives in package process.
package terminal

import (
	"log/slog"
	"time"
)

// Config tunes the PTY sessions the module opens in this process. Zero
// values use defaults.
type Config struct {
	DefaultSize     Size
	ScrollbackBytes int
	StopGrace       time.Duration
	Logger          *slog.Logger
	// Factory replaces the in-process PTY factory (the daemon passes the
	// shim launcher, whose sessions outlive it).
	Factory Factory
	// Activity, when set, is told that a session's screen changed, at most
	// once per ActivityInterval per session (default 1s). The daemon turns
	// it into process.output events.
	Activity         func(processID string)
	ActivityInterval time.Duration
}

type Module struct {
	Service Service
}

func NewModule(cfg Config) *Module {
	factory := cfg.Factory
	if factory == nil {
		factory = PTYFactory{
			Size:            cfg.DefaultSize,
			ScrollbackBytes: cfg.ScrollbackBytes,
			StopGrace:       cfg.StopGrace,
			Logger:          cfg.Logger,
		}
	}
	var opts []Option
	if cfg.Activity != nil {
		opts = append(opts, WithActivity(cfg.Activity, cfg.ActivityInterval))
	}
	return &Module{Service: NewService(factory, opts...)}
}
