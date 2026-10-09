// Package runtime is the daemon's composition root. It builds every domain
// module, wires them together through their small interfaces, mounts them on
// one protocol router, and owns the socket server and agent lifetimes.
package runtime

import (
	"path/filepath"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/shim"
	"github.com/admirable-oss/hive/internal/terminal"
)

type Module struct {
	Service      *Server
	Environments environment.Service
	Processes    process.Service
	Terminals    terminal.Service
	Events       *event.Bus
}

// NewModule assembles the daemon. Dependencies flow strictly downward:
// runtime → process → (environment, terminal → shim) → protocol/jsonfile.
func NewModule(cfg Config) *Module {
	root := cfg.root()
	log := logging.OrDiscard(cfg.Logger)
	cfg.Logger = log
	bus := event.NewBus(log.With("module", "event"))

	termCfg := cfg.Terminal
	termCfg.Logger = log.With("module", "terminal")
	procCfg := process.Config{
		BaseDir:   root,
		StopGrace: cfg.StopGrace,
		Logger:    log.With("module", "process"),
		Events:    bus,
	}
	if cfg.Shim != nil {
		// Agents run under shims: they survive the daemon and are adopted
		// by the next one.
		launcher := &shim.Launcher{
			Exe:             cfg.Shim.Exe,
			Args:            cfg.Shim.Args,
			Env:             cfg.Shim.Env,
			RunDir:          filepath.Join(root, "run"),
			DefaultSize:     termCfg.DefaultSize,
			ScrollbackBytes: termCfg.ScrollbackBytes,
			StopGrace:       termCfg.StopGrace,
			Logger:          log.With("module", "shim"),
		}
		termCfg.Factory = launcher
		procCfg.Runner = shimRunner{launcher}
		procCfg.Adopter = shimRunner{launcher}
	}
	envs := environment.NewModule(environment.Config{BaseDir: root}).Service
	terms := terminal.NewModule(termCfg).Service
	procs := process.NewModule(procCfg, envs, terms).Service
	guardedEnvs := envGuard{Service: envs, procs: procs, events: bus}

	router := protocol.NewRouter()
	server := NewServer(cfg, router, procs)

	Register(router, server)
	environment.Register(router, guardedEnvs)
	process.Register(router, procs)
	terminal.Register(router, terms)
	event.Register(router, bus)

	return &Module{
		Service:      server,
		Environments: guardedEnvs,
		Processes:    procs,
		Terminals:    terms,
		Events:       bus,
	}
}
