package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/project"
)

// newProjects builds the project service over the daemon's environments,
// panes and agents. Trust is kept in <root>/projects.
func newProjects(cfg Config, envs environment.Service, panes *pane.Service, agents *agent.Service, ops *agentOps) *project.Service {
	return project.NewService(project.Config{
		Dir:       filepath.Join(cfg.root(), "projects"),
		Workspace: &projectWorkspace{envs: envs, panes: panes, agents: agents, ops: ops},
		Tracked:   gitTracks,
	})
}

// gitTracks says whether git tracks name in dir.
func gitTracks(dir, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--error-unmatch", "--", name).Run() == nil
}

// projectWorkspace is the project service's view of the daemon.
type projectWorkspace struct {
	envs   environment.Service
	panes  *pane.Service
	agents *agent.Service
	ops    *agentOps
}

func (w *projectWorkspace) State(ctx context.Context) (project.State, error) {
	envs, err := w.envs.List(ctx)
	if err != nil {
		return project.State{}, err
	}
	st := project.State{Tabs: map[string][]project.TabState{}, Installed: workflowAgents{agents: w.agents}.Installed()}
	for _, m := range w.agents.Manifests() {
		st.Known = append(st.Known, m.ID)
	}
	for _, e := range envs {
		es := project.EnvState{ID: e.ID, Root: e.Path, Vars: e.Env}
		if e.Worktree != nil {
			es.WorktreeRepo, es.Branch = e.Worktree.Repo, e.Worktree.Branch
		}
		st.Environments = append(st.Environments, es)
		tabs, err := w.panes.Tabs(ctx, e.ID)
		if err != nil {
			return project.State{}, err
		}
		for _, t := range tabs {
			st.Tabs[e.ID] = append(st.Tabs[e.ID], project.TabState{ID: t.ID, Name: t.Name})
		}
	}
	agents, err := w.agents.List(ctx, false)
	if err != nil {
		return project.State{}, err
	}
	for _, a := range agents {
		if a.PaneID == "" {
			continue // its tab was closed: nothing is left to start again
		}
		st.Agents = append(st.Agents, project.AgentState{
			ID: a.ID, Name: a.Name, Kind: a.Kind, EnvironmentID: a.EnvironmentID, TabID: a.TabID,
			Exited: a.State == agent.StateExited,
		})
	}
	return st, nil
}

func (w *projectWorkspace) CreateEnvironment(ctx context.Context, id, root string, vars map[string]string) error {
	_, err := w.envs.Create(ctx, environment.CreateRequest{ID: id, Root: root, Env: vars})
	return err
}

func (w *projectWorkspace) SetVars(ctx context.Context, id string, vars map[string]string) error {
	_, err := w.envs.Update(ctx, environment.UpdateRequest{ID: id, Set: vars})
	return err
}

func (w *projectWorkspace) CreateTab(ctx context.Context, envID string, t project.Tab) error {
	_, err := w.panes.Apply(ctx, pane.LayoutSpec{
		Version: pane.SpecVersion,
		Environments: []pane.EnvironmentSpec{{
			ID:   envID,
			Tabs: []pane.TabSpec{{Name: t.Name, Layout: tabLayout(t)}},
		}},
	})
	return err
}

// tabLayout shares a tab evenly between its panes, in a row or a column.
func tabLayout(t project.Tab) *pane.NodeSpec {
	split := layout.Horizontal
	if t.Split == project.SplitVertical {
		split = layout.Vertical
	}
	var node func(ps []project.Pane) *pane.NodeSpec
	node = func(ps []project.Pane) *pane.NodeSpec {
		p := ps[0]
		leaf := &pane.NodeSpec{Pane: &pane.Spec{Name: p.Name, Command: p.Command, Cwd: filepath.FromSlash(p.Cwd)}}
		if len(ps) == 1 {
			return leaf
		}
		return &pane.NodeSpec{Split: split, Ratio: 1 / float64(len(ps)), First: leaf, Second: node(ps[1:])}
	}
	return node(t.Panes)
}

func (w *projectWorkspace) StartAgent(ctx context.Context, envID string, a project.Agent, kind, in string) error {
	req := AgentStartRequest{
		Kind: kind, EnvironmentID: envID, Worktree: a.Worktree, Isolate: a.Isolate, Base: a.Base,
		Name: a.Name, Args: a.Args,
	}
	if in != "" {
		// The worktree an earlier hive up made, which hive down kept.
		req.EnvironmentID, req.Worktree, req.Isolate, req.Base = in, "", false, ""
	}
	res, err := w.ops.start(ctx, req, true)
	if err != nil || in != "" || res.Environment.Worktree == nil {
		return err
	}
	_, err = w.envs.Update(ctx, environment.UpdateRequest{ID: res.Environment.ID, Set: map[string]string{project.EnvProjectAgent: a.Name}})
	return err
}

func (w *projectWorkspace) RestartAgent(ctx context.Context, id string, a project.Agent) error {
	ag, err := w.agents.Get(ctx, id)
	if err != nil {
		return err
	}
	var m *agent.Manifest
	for _, x := range w.agents.Manifests() {
		if x.ID == ag.Kind {
			m = x
		}
	}
	if m == nil || len(m.Start.Command) == 0 {
		return fmt.Errorf("%w: no agent manifest %q to start it with", agent.ErrInvalid, ag.Kind)
	}
	command := append(append([]string{}, m.Start.Command...), a.Args...)
	_, err = w.panes.Respawn(ctx, ag.PaneID, pane.Spec{
		Name: a.Name, Command: command, Env: map[string]string{agent.EnvAgent: m.ID},
	})
	return err
}

func (w *projectWorkspace) CloseTab(ctx context.Context, id string) error {
	return w.panes.CloseTab(ctx, id)
}
