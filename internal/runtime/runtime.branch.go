package runtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/task"
)

// Branches follow the worktrees Hive makes (package branch); this file
// gives the service its ports: worktrees and environments to remove,
// agents to send review comments to, commands for gates, and the forges.

// BranchesConfig is the [branches] policy. Zero values are the defaults.
type BranchesConfig struct {
	// AgentPattern names the branch of an agent started with --isolate;
	// TaskPattern the branch of an isolated task.
	AgentPattern string
	TaskPattern  string
	// Cleanup is merged (remove a merged branch's worktree when nothing
	// uncommitted or running is in it) or never.
	Cleanup string
	// DeleteMerged also deletes a cleaned-up branch git agrees merged.
	DeleteMerged bool
	// Interval is how often branches are read again without a change
	// noticed; Keep how many closed branches are remembered.
	Interval time.Duration
	Keep     int
	Gates    []branch.Gate
	Forges   []ForgeConfig
}

// ForgeConfig routes a host's pull requests: to github or gitlab (their
// gh and glab tools), or to a command of the person's own.
type ForgeConfig struct {
	Host    string
	Type    string // github | gitlab | command
	Command []string
}

func (c BranchesConfig) agentPattern() string {
	if c.AgentPattern != "" {
		return c.AgentPattern
	}
	return branch.DefaultAgentPattern
}

func (c BranchesConfig) taskPattern() string {
	if c.TaskPattern != "" {
		return c.TaskPattern
	}
	return branch.DefaultTaskPattern
}

// forges returns the forges the configuration names.
func (c BranchesConfig) forges() *branch.Forges {
	f := branch.DefaultForges(nil)
	for _, x := range c.Forges {
		if x.Type == "command" {
			f.Register(branch.CommandForge{ForgeName: x.Host, Command: x.Command})
			f.Route(x.Host, x.Host)
			continue
		}
		f.Route(x.Host, x.Type)
	}
	return f
}

// newBranches builds the branch service over this daemon's worktrees,
// environments, processes and agents.
func newBranches(cfg Config, envs environment.Service, procs process.Service, agents *agent.Service, wts *worktrees, bus event.Publisher, log *slog.Logger) *branch.Service {
	bc := cfg.Branches
	svc := branch.NewModule(branch.ModuleConfig{
		BaseDir: cfg.root(),
		Service: branch.Config{
			Logger: log, Interval: bc.Interval, Cleanup: branch.Cleanup(bc.Cleanup),
			DeleteMerged: bc.DeleteMerged, Keep: bc.Keep, Gates: bc.Gates,
		},
	}, branch.Ports{
		Git:       git.New(),
		Workspace: branchWorkspace{wts: wts, envs: envs, procs: procs},
		Agents:    branchAgents{agents: agents},
		Commands:  branchCommands{},
		Events:    bus,
		Forges:    bc.forges(),
	}).Service
	if err := svc.Load(); err != nil {
		log.Warn("reading the branches a previous daemon followed", "err", err)
	}
	return svc
}

// feedBranches tells the branch service what changes until the daemon
// stops: first the worktree environments it does not follow yet, then
// each change to an environment's git state or an agent's work.
func feedBranches(ctx context.Context, svc *branch.Service, envs environment.Service, wts *worktrees, bus *event.Bus, log *slog.Logger) {
	sub := bus.Subscribe(event.DefaultBuffer, event.EnvironmentGit, agent.EventState)
	defer sub.Close()
	if list, err := envs.List(ctx); err == nil {
		for _, e := range list {
			if e.Worktree != nil {
				wts.track(ctx, e, e.Worktree.Repo, e.Worktree.Branch, "", nil)
			}
		}
	} else {
		log.Warn("listing environments to follow their branches", "err", err)
	}
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		switch ev.Type {
		case event.EnvironmentGit:
			var p struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(ev.Data, &p) == nil {
				svc.Touch(p.ID, false)
			}
		case agent.EventState:
			var a agent.Agent
			if json.Unmarshal(ev.Data, &a) == nil {
				svc.Touch(a.EnvironmentID, a.State == agent.StateWorking)
			}
		}
	}
}

// track tells the branch service about a worktree environment. A failure
// costs only the following, so it is logged, not returned.
func (w *worktrees) track(ctx context.Context, env environment.Environment, repo, name, base string, owner *branch.Owner) {
	if w.branches == nil || name == "" {
		return
	}
	_, err := w.branches.Track(ctx, branch.TrackRequest{
		EnvironmentID: env.ID, Path: env.Path, Repo: repo, Name: name, Base: base, Owner: owner,
	})
	if err != nil && w.log != nil {
		w.log.Warn("following a worktree's branch", "branch", name, "path", env.Path, "err", err)
	}
}

// ownerOf says what an agent started in a worktree works for: itself, or
// the task whose variables it was started with.
func ownerOf(a agent.Agent, env map[string]string) *branch.Owner {
	return &branch.Owner{Agent: a.ID, Kind: a.Kind, Plan: env[task.EnvPlan], Task: env[task.EnvTask]}
}

// branchWorkspace removes worktrees and finds what runs in them.
type branchWorkspace struct {
	wts   *worktrees
	envs  environment.Service
	procs process.Service
}

func (w branchWorkspace) RemoveWorktree(ctx context.Context, path string, force bool) error {
	return w.wts.Remove(ctx, WorktreeRemoveRequest{Path: path, Force: force})
}

func (w branchWorkspace) Busy(ctx context.Context, path string) (bool, error) {
	envs, err := w.envs.List(ctx)
	if err != nil {
		return false, err
	}
	for _, e := range envs {
		if !samePath(e.Path, path) {
			continue
		}
		procs, err := w.procs.List(ctx, e.ID)
		if err != nil {
			return false, err
		}
		for _, p := range procs {
			if p.Active() {
				return true, nil
			}
		}
	}
	return false, nil
}

// branchAgents reaches agents for review comments.
type branchAgents struct {
	agents *agent.Service
}

func infoOf(a agent.Agent) branch.AgentInfo {
	return branch.AgentInfo{ID: a.ID, Kind: a.Kind, State: string(a.State), EnvironmentID: a.EnvironmentID}
}

func (b branchAgents) Get(ctx context.Context, id string) (branch.AgentInfo, error) {
	a, err := b.agents.Get(ctx, id)
	if err != nil {
		return branch.AgentInfo{}, err
	}
	return infoOf(a), nil
}

func (b branchAgents) In(ctx context.Context, envID string) ([]branch.AgentInfo, error) {
	list, err := b.agents.List(ctx, false)
	if err != nil {
		return nil, err
	}
	var out []branch.AgentInfo
	for _, a := range list {
		if a.EnvironmentID == envID {
			out = append(out, infoOf(a))
		}
	}
	return out, nil
}

func (b branchAgents) Prompt(ctx context.Context, id, text string) error {
	_, err := b.agents.Prompt(ctx, id, agent.PromptRequest{Text: text})
	return err
}

// branchCommands runs gates as task checks run: in the worktree, with
// everything they start stopped on a timeout.
type branchCommands struct{}

func (branchCommands) Run(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error) {
	return runCheck(ctx, dir, argv, env)
}

// branchName names the branch of an agent started with --isolate.
func branchName(pattern, kind, name, envID string) (string, error) {
	return branch.Expand(pattern, branch.PatternVars{Kind: kind, Name: strings.TrimSpace(name), Env: envID}, time.Now())
}
