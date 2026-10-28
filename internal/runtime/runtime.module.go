// Package runtime is the daemon's composition root. It builds every domain
// module, wires them together through their small interfaces, mounts them on
// one protocol router, and owns the socket server and agent lifetimes.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/shim"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/workflow"
)

type Module struct {
	Service      *Server
	Environments environment.Service
	Processes    process.Service
	Terminals    terminal.Service
	Panes        *pane.Service
	Events       *event.Bus
	Agents       *agent.Service
	Tasks        *task.Service
	Branches     *branch.Service
	Workflows    *workflow.Service
	Insights     *insights.Service
}

// NewModule assembles the daemon. Dependencies flow strictly downward:
// runtime → (agent, pane) → process → (environment, terminal → shim) →
// protocol/jsonfile.
func NewModule(cfg Config) *Module {
	root := cfg.root()
	log := logging.OrDiscard(cfg.Logger)
	cfg.Logger = log
	bus := event.NewBus(log.With("module", "event"))

	procsRef, termsRef, locator := &lateProcesses{}, &lateTerminals{}, &paneLocator{}
	agentLog := log.With("module", "agent")
	agents, warnings, err := agent.NewService(agent.Config{UserDir: cfg.AgentManifestDir, Logger: agentLog}, procsRef, termsRef, locator, bus)
	if err != nil {
		panic(err) // the built-in manifests are broken: a bug the tests catch
	}
	for _, w := range warnings {
		agentLog.Warn("agent manifest", "warning", w)
	}

	termCfg := cfg.Terminal
	termCfg.Logger = log.With("module", "terminal")
	termCfg.Watcher = agents
	procCfg := process.Config{
		BaseDir:   root,
		StopGrace: cfg.StopGrace,
		Logger:    log.With("module", "process"),
		Events:    bus,
		LaunchEnv: cfg.launchEnv(),
		// Rotation applies to terminal agents' logs, which the shim writes.
		LogMaxBytes: cfg.ProcessLogMaxBytes,
		LogGraphics: cfg.Images.LogGraphics,
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
	var keep *keeper         // built after the panes, which it reads; it supplies their recovery
	var plugins *plugin.Host // set below; panes open views through it
	panes := pane.NewModule(pane.Config{
		BaseDir:       root,
		Size:          termCfg.DefaultSize,
		Shell:         cfg.Shell,
		Logger:        log.With("module", "pane"),
		RecoverLayout: func(envID string) (pane.State, bool) { return keep.layoutFor(envID) },
		OpenView: func(view string, at pane.ViewTarget) error {
			if plugins == nil {
				return errors.New("plugins do not run in this daemon")
			}
			id, name, _ := strings.Cut(view, "/")
			return plugins.OpenView(id, name, plugin.Context{Environment: at.Environment, Tab: at.Tab, Pane: at.Pane})
		},
	}, procs, terms, guardedEnvs, bus).Service
	guardedEnvs.panes = panes
	procsRef.Service, termsRef.Service, locator.panes = procs, terms, panes
	keep = newKeeper(cfg, guardedEnvs, panes, procs, terms, agents, bus, log.With("module", "session"))
	keep.load()

	router := protocol.NewRouter()
	server := NewServer(cfg, router, procs)
	server.Go(tracker.run)
	server.Go(func(ctx context.Context) { closeFinishedPopups(ctx, bus, panes, log) })
	server.Go(agents.Run)
	server.Go(func(ctx context.Context) { markSeen(ctx, bus, agents, agentLog) })
	if p := cfg.Persist; p != nil && p.Restore {
		server.Go(func(ctx context.Context) {
			if _, err := keep.restore(ctx, p.ResumeAgents); err != nil && ctx.Err() == nil {
				log.Warn("session restore failed", "err", err)
			}
		})
	}
	server.Go(keep.run)
	server.Go(keep.runGC)
	server.OnShutdown(keep.save)

	meta := metadata.NewStore(nil)
	server.Go(func(ctx context.Context) { expireMetadata(ctx, meta, bus, log) })
	plugins = newPluginHost(cfg, log.With("module", "plugin"), router, bus, func(ctx context.Context) []string {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		envs, err := guardedEnvs.List(ctx)
		if err != nil {
			return nil
		}
		dirs := make([]string, 0, len(envs))
		for _, e := range envs {
			if e.Path != "" {
				dirs = append(dirs, e.Path)
			}
		}
		return dirs
	})
	server.Go(plugins.Run)
	server.Go(func(ctx context.Context) { forwardEvents(ctx, bus, plugins) })
	wts := &worktrees{git: git.New(), envs: guardedEnvs, dir: cfg.worktreeDir(), events: bus, log: gitLog}
	branchLog := log.With("module", "branch")
	branches := newBranches(cfg, guardedEnvs, procs, agents, wts, bus, branchLog)
	wts.branches = branches
	server.Go(branches.Run)
	server.Go(func(ctx context.Context) { feedBranches(ctx, branches, guardedEnvs, wts, bus, branchLog) })
	ops := &agentOps{agents: agents, panes: panes, envs: guardedEnvs, worktrees: wts, procs: procs, pattern: cfg.Branches.agentPattern()}
	insightTasks := &insightsTasks{}
	ins := newInsights(cfg, agents, insightTasks, bus, log.With("module", "insights"))
	server.Go(ins.Run)
	server.Go(func(ctx context.Context) { feedInsights(ctx, ins, agents, insightTasks, bus) })
	tasks := newTasks(cfg, ops, agents, bus, ins.Hold, log.With("module", "task"))
	insightTasks.svc = tasks.svc
	server.Go(tasks.run)
	workflows := newWorkflows(cfg, tasks.svc, guardedEnvs, agents, plugins, bus, log.With("module", "workflow"))
	server.Go(workflows.Run)
	server.Go(func(ctx context.Context) { feedWorkflows(ctx, workflows, bus) })
	obs := newObservers(cfg, log.With("module", "observer"), bus, guardedEnvs, panes, observer.Sources{
		Agents: agents, Terminals: terms, Metadata: meta.List, Plans: tasks.svc, Insights: ins,
		Plugins: func(context.Context) []observer.Plugin { return observedPlugins(plugins.Status()) },
		Info: func() observer.Info {
			snap := server.Snapshot()
			return observer.Info{Version: snap.Version, Session: snap.Session, StartedAt: snap.StartedAt, AgentsSurviveRestart: snap.AgentsSurviveRestart}
		},
	})
	server.Go(obs.run)

	Register(router, server)
	environment.Register(router, guardedEnvs)
	process.Register(router, procs)
	terminal.Register(router, terms)
	imageDir := ""
	if cfg.Images.Keep > 0 {
		imageDir = filepath.Join(root, "images")
	}
	terminal.RegisterImages(router, terms, terminal.ImageConfig{
		MaxBytes: cfg.Images.MaxBytes, MaxPixels: cfg.Images.MaxPixels, Dir: imageDir, Keep: cfg.Images.Keep,
		Input: func(processID string) terminal.ImageInput {
			if m := agents.ManifestOf(processID); m != nil {
				return terminal.ImageInput(m.Input.Image)
			}
			return terminal.ImageInputNone
		},
	})
	event.Register(router, bus)
	pane.Register(router, panes)
	agent.Register(router, agents)
	registerWorktrees(router, wts)
	registerAgentOps(router, ops)
	task.Register(router, tasks.svc)
	branch.Register(router, branches)
	workflow.Register(router, workflows)
	registerWorkflowTriggers(router, plugins, workflows)
	insights.Register(router, ins)
	registerSession(router, keep)
	registerMetadata(router, meta, metadataTargets{panes: panes, envs: guardedEnvs}, bus)
	registerPlugins(router, plugins, plugin.NewStore(cfg.pluginDir()))
	registerDebug(router, server)
	registerObservers(router, obs, guardedEnvs, panes)
	registerAPI(router, server, guardedEnvs, panes, procs, agents) // last: it describes the others

	return &Module{
		Service:      server,
		Environments: guardedEnvs,
		Processes:    procs,
		Terminals:    terms,
		Panes:        panes,
		Events:       bus,
		Agents:       agents,
		Tasks:        tasks.svc,
		Branches:     branches,
		Workflows:    workflows,
		Insights:     ins,
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
