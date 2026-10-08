// Package runtime is the daemon's composition root. It builds every domain
// module, wires them together through their small interfaces, mounts them on
// one protocol router, and owns the socket server and agent lifetimes.
package runtime

import (
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
)

type Module struct {
	Service      *Server
	Environments environment.Service
	Processes    process.Service
	Terminals    terminal.Service
}

// NewModule assembles the daemon. Dependencies flow strictly downward:
// runtime → process → (environment, terminal) → protocol/jsonfile.
func NewModule(cfg Config) *Module {
	root := cfg.root()
	log := logging.OrDiscard(cfg.Logger)
	cfg.Logger = log

	termCfg := cfg.Terminal
	termCfg.Logger = log.With("module", "terminal")
	envs := environment.NewModule(environment.Config{BaseDir: root}).Service
	terms := terminal.NewModule(termCfg).Service
	procs := process.NewModule(process.Config{
		BaseDir:   root,
		StopGrace: cfg.StopGrace,
		Logger:    log.With("module", "process"),
	}, envs, terms).Service
	guardedEnvs := envGuard{Service: envs, procs: procs}

	router := protocol.NewRouter()
	server := NewServer(cfg, router, procs)

	Register(router, server)
	environment.Register(router, guardedEnvs)
	process.Register(router, procs)
	terminal.Register(router, terms)

	return &Module{
		Service:      server,
		Environments: guardedEnvs,
		Processes:    procs,
		Terminals:    terms,
	}
}
