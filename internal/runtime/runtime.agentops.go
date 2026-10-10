package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
)

// agentOps are the agent operations that span packages: starting an agent
// (in a new worktree, in a new tab) and acting on its pane. They live in
// the runtime, like worktrees, because they compose agents, panes and
// environments.
type agentOps struct {
	agents    *agent.Service
	panes     *pane.Service
	envs      environment.Service
	worktrees *worktrees
	procs     process.Service
}

// AgentStartRequest starts an agent by its manifest.
type AgentStartRequest struct {
	// Kind is the manifest ID: claude, codex, … (see agent.manifests).
	Kind          string `json:"kind"`
	EnvironmentID string `json:"environment_id"`
	// Worktree, when set, is a branch: the agent starts in a new worktree
	// of the environment's repository on that branch, with its own
	// environment, so agents working in parallel never share a checkout.
	Worktree string `json:"worktree,omitempty"`
	// Base is where a new worktree branch starts (default HEAD).
	Base string `json:"base,omitempty"`
	// Name names the tab and the pane (default the manifest's ID).
	Name string `json:"name,omitempty"`
	// Args are added to the manifest's start command.
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// AgentStarted is what agent.start returns.
type AgentStarted struct {
	Agent agent.Agent `json:"agent"`
	Tab   pane.Tab    `json:"tab"`
	Pane  pane.Pane   `json:"pane"`
	// Environment is the one it runs in: new with Worktree.
	Environment environment.Environment `json:"environment"`
}

var errNoPane = errors.New("the agent has no pane")

func (o *agentOps) Start(ctx context.Context, req AgentStartRequest) (AgentStarted, error) {
	var m *agent.Manifest
	for _, x := range o.agents.Manifests() {
		if x.ID == req.Kind {
			m = x
		}
	}
	switch {
	case m == nil:
		return AgentStarted{}, fmt.Errorf("%w: no agent manifest %q (see hive agent manifests)", agent.ErrInvalid, req.Kind)
	case len(m.Start.Command) == 0:
		return AgentStarted{}, fmt.Errorf("%w: manifest %q has no start command", agent.ErrInvalid, req.Kind)
	case req.EnvironmentID == "":
		return AgentStarted{}, fmt.Errorf("%w: environment_id is required", agent.ErrInvalid)
	}
	env, err := o.envs.Get(ctx, req.EnvironmentID)
	if err != nil {
		return AgentStarted{}, err
	}
	if req.Worktree != "" {
		if env.Path == "" {
			return AgentStarted{}, fmt.Errorf("%w: environment %q has no directory to make a worktree of", errInvalidRequest, env.ID)
		}
		repo := env.Path
		if env.Worktree != nil {
			repo = env.Worktree.Repo
		}
		if env, err = o.worktrees.Create(ctx, WorktreeCreateRequest{Repo: repo, Branch: req.Worktree, Base: req.Base}); err != nil {
			return AgentStarted{}, err
		}
	}
	name := req.Name
	if name == "" {
		name = m.ID
	}
	vars := map[string]string{agent.EnvAgent: m.ID}
	for k, v := range req.Env {
		vars[k] = v
	}
	command := append(append([]string{}, m.Start.Command...), req.Args...)
	tab, p, err := o.panes.CreateTab(ctx, pane.CreateTabRequest{
		EnvironmentID: env.ID, Name: name,
		Pane: pane.Spec{Name: name, Command: command, Env: vars},
	})
	if err != nil {
		return AgentStarted{}, err
	}
	a, err := o.agents.Get(ctx, p.ProcessID)
	if err != nil {
		return AgentStarted{}, err
	}
	return AgentStarted{Agent: a, Tab: tab, Pane: p, Environment: env}, nil
}

// paneOf returns an agent's pane.
func (o *agentOps) paneOf(ctx context.Context, id string) (string, error) {
	a, err := o.agents.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if a.PaneID == "" {
		return "", fmt.Errorf("%w: %q", errNoPane, id)
	}
	return a.PaneID, nil
}

func (o *agentOps) Rename(ctx context.Context, id, name string) (agent.Agent, error) {
	paneID, err := o.paneOf(ctx, id)
	if err != nil {
		return agent.Agent{}, err
	}
	if _, err := o.panes.Rename(ctx, paneID, name); err != nil {
		return agent.Agent{}, err
	}
	return o.agents.Get(ctx, paneID)
}

func (o *agentOps) Focus(ctx context.Context, id string) (agent.Agent, error) {
	paneID, err := o.paneOf(ctx, id)
	if err != nil {
		return agent.Agent{}, err
	}
	if _, err := o.panes.Focus(ctx, paneID); err != nil {
		return agent.Agent{}, err
	}
	return o.agents.Get(ctx, paneID)
}

// Stop stops an agent's process; its pane stays, showing the last screen.
func (o *agentOps) Stop(ctx context.Context, id string) (agent.Agent, error) {
	a, err := o.agents.Get(ctx, id)
	if err != nil {
		return agent.Agent{}, err
	}
	if err := o.procs.Stop(ctx, a.ID); err != nil {
		return agent.Agent{}, err
	}
	return o.agents.Get(ctx, a.ID)
}

type agentRenameParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type agentIDParams struct {
	ID string `json:"id"`
}

func registerAgentOps(r *protocol.Router, o *agentOps) {
	r.MustRegister("agent.start", protocol.Method(func(ctx context.Context, p AgentStartRequest) (AgentStarted, error) {
		res, err := o.Start(ctx, p)
		return res, agentOpsError(err)
	}))
	r.MustRegister("agent.rename", protocol.Method(func(ctx context.Context, p agentRenameParams) (agent.Agent, error) {
		a, err := o.Rename(ctx, p.ID, p.Name)
		return a, agentOpsError(err)
	}))
	r.MustRegister("agent.focus", protocol.Method(func(ctx context.Context, p agentIDParams) (agent.Agent, error) {
		a, err := o.Focus(ctx, p.ID)
		return a, agentOpsError(err)
	}))
	r.MustRegister("agent.stop", protocol.Method(func(ctx context.Context, p agentIDParams) (agent.Agent, error) {
		a, err := o.Stop(ctx, p.ID)
		return a, agentOpsError(err)
	}))
}

func agentOpsError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, agent.ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, agent.ErrInvalid), errors.Is(err, errNoPane):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	var pe *protocol.Error
	if e := worktreeWireError(err); errors.As(e, &pe) {
		return e
	}
	return pane.WireError(err)
}
