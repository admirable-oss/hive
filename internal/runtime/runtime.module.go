// Package runtime is the daemon's composition root. It builds every domain
// module, wires them together through their small interfaces, mounts them on
// one protocol router, and owns the socket server and agent lifetimes.
package runtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/pane"
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
	Panes        *pane.Service
	Events       *event.Bus
}

// NewModule assembles the daemon. Dependencies flow strictly downward:
// runtime → pane → process → (environment, terminal → shim) → protocol/jsonfile.
func NewModule(cfg Config) *Module {
	root := cfg.root()
	log := logging.OrDiscard(cfg.Logger)
	cfg.Logger = log
	bus := event.NewBus(log.With("module", "event"))

	termCfg := cfg.Terminal
	termCfg.Logger = log.With("module", "terminal")
	termCfg.Activity = func(processID string) {
		bus.Publish(event.ProcessOutput, map[string]string{"id": processID})
	}
	procCfg := process.Config{
		BaseDir:   root,
		StopGrace: cfg.StopGrace,
		Logger:    log.With("module", "process"),
		Events:    bus,
		LaunchEnv: cfg.launchEnv(),
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
	gitLog := log.With("module", "git")
	tracker := newGitTracker(git.New(), envs, bus, cfg.GitInterval, gitLog)
	guardedEnvs := &envGuard{Service: envs, procs: procs, events: bus, git: tracker}
	panes := pane.NewModule(pane.Config{
		BaseDir: root,
		Size:    termCfg.DefaultSize,
		Shell:   cfg.Shell,
		Logger:  log.With("module", "pane"),
	}, procs, terms, guardedEnvs, bus).Service
	guardedEnvs.panes = panes

	router := protocol.NewRouter()
	server := NewServer(cfg, router, procs)
	server.Go(tracker.run)
	server.Go(func(ctx context.Context) { closeFinishedPopups(ctx, bus, panes, log) })

	Register(router, server)
	environment.Register(router, guardedEnvs)
	process.Register(router, procs)
	terminal.Register(router, terms)
	event.Register(router, bus)
	pane.Register(router, panes)
	registerWorktrees(router, &worktrees{git: git.New(), envs: guardedEnvs, dir: cfg.worktreeDir()})

	return &Module{
		Service:      server,
		Environments: guardedEnvs,
		Processes:    procs,
		Terminals:    terms,
		Panes:        panes,
		Events:       bus,
	}
}

// closeFinishedPopups closes a popup pane once its command exits. Exits
// are learned from the event bus; whenever events were lost, and once at
// start for popups that ended while no daemon ran, every popup is checked.
func closeFinishedPopups(ctx context.Context, bus *event.Bus, panes *pane.Service, log *slog.Logger) {
	sub := bus.Subscribe(event.DefaultBuffer, event.ProcessExited)
	defer sub.Close()
	panes.ReapPopups(ctx)
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if ev.Type == event.Lost {
			panes.ReapPopups(ctx)
			continue
		}
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			log.Debug("undecodable process event", "err", err)
			continue
		}
		panes.ProcessExited(ctx, p.ID)
	}
}
