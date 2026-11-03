package runtime

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/project"
	"github.com/admirable-oss/hive/internal/workflow"
)

// newProjects builds the project service over the daemon's environments,
// panes, agents and plugins. Trust is kept in <root>/projects, and
// toolchains' environments in <root>/projects/toolchains. changed runs
// when a project's trust changes, which changes the workflows it brings.
func newProjects(cfg Config, envs environment.Service, panes *pane.Service, agents *agent.Service, ops *agentOps, plugins *plugin.Host, changed func()) *project.Service {
	dir := filepath.Join(cfg.root(), "projects")
	var envCommand []string
	if exe, err := os.Executable(); err == nil {
		envCommand = []string{exe, project.EnvCommand}
	}
	return project.NewService(project.Config{
		Dir:        dir,
		Workspace:  &projectWorkspace{envs: envs, panes: panes, agents: agents, ops: ops, plugins: plugins},
		Tracked:    gitTracks,
		Toolchains: project.NewToolchains(project.ToolchainConfig{Dir: filepath.Join(dir, "toolchains"), EnvCommand: envCommand}),
		Secrets:    project.ToolStores{},
		Changed:    changed,
	})
}

// projectGates are a trusted project's gates, as branch gates for its
// repository.
func projectGates(projects *project.Service, repo string) []branch.Gate {
	gates, root := projects.Gates(repo)
	out := make([]branch.Gate, 0, len(gates))
	for _, g := range gates {
		out = append(out, branch.Gate{Name: g.Name, Command: g.Command, TimeoutMS: g.TimeoutMS, Repo: root})
	}
	return out
}

// projectWorkflows are the workflow files trusted projects bring.
func projectWorkflows(projects *project.Service) []workflow.ProjectFile {
	if projects == nil {
		return nil
	}
	files := projects.WorkflowFiles()
	out := make([]workflow.ProjectFile, 0, len(files))
	for _, f := range files {
		out = append(out, workflow.ProjectFile{Path: f.Path, Digest: f.Digest, Project: f.Project})
	}
	return out
}

// gitTracks says whether git tracks name in dir.
func gitTracks(dir, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--error-unmatch", "--", name).Run() == nil
}

// projectWorkspace is the project service's view of the daemon.
type projectWorkspace struct {
	envs    environment.Service
	panes   *pane.Service
	agents  *agent.Service
	ops     *agentOps
	plugins *plugin.Host
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
	if w.plugins != nil {
		for _, p := range w.plugins.Status() {
			st.Plugins = append(st.Plugins, p.ID)
		}
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
		// What each tab runs, as layout.export says (tabs of only popups
		// are left out there).
		layouts := map[string][]pane.TabSpec{}
		if spec, err := w.panes.Export(ctx, []string{e.ID}); err == nil && len(spec.Environments) == 1 {
			for _, ts := range spec.Environments[0].Tabs {
				layouts[ts.Name] = append(layouts[ts.Name], ts)
			}
		}
		for _, t := range tabs {
			ts := project.TabState{ID: t.ID, Name: t.Name}
			if specs := layouts[t.Name]; len(specs) > 0 {
				ts.Split, ts.Panes = flatten(specs[0].Layout)
				layouts[t.Name] = specs[1:]
			}
			st.Tabs[e.ID] = append(st.Tabs[e.ID], ts)
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

func (w *projectWorkspace) CreateTab(ctx context.Context, envID string, t project.Tab, env map[string]string, wrap []string) error {
	_, err := w.panes.Apply(ctx, pane.LayoutSpec{
		Version: pane.SpecVersion,
		Environments: []pane.EnvironmentSpec{{
			ID:   envID,
			Tabs: []pane.TabSpec{{Name: t.Name, Layout: tabLayout(t, env, wrap)}},
		}},
	})
	return err
}

// tabLayout shares a tab evenly between its panes, in a row or a column.
// Each pane gets env, and runs through wrap when it is set.
func tabLayout(t project.Tab, env map[string]string, wrap []string) *pane.NodeSpec {
	split := layout.Horizontal
	if t.Split == project.SplitVertical {
		split = layout.Vertical
	}
	var node func(ps []project.Pane) *pane.NodeSpec
	node = func(ps []project.Pane) *pane.NodeSpec {
		p := ps[0]
		command := p.Command
		if len(wrap) > 0 {
			command = append(slices.Clone(wrap), cmpSlice(command, project.ContainerShell())...)
		}
		var vars map[string]string
		if len(env) > 0 {
			vars = maps.Clone(env)
		}
		leaf := &pane.NodeSpec{Pane: &pane.Spec{Name: p.Name, Command: command, Cwd: filepath.FromSlash(p.Cwd), Env: vars}}
		if len(ps) == 1 {
			return leaf
		}
		return &pane.NodeSpec{Split: split, Ratio: 1 / float64(len(ps)), First: leaf, Second: node(ps[1:])}
	}
	return node(t.Panes)
}

func (w *projectWorkspace) StartAgent(ctx context.Context, envID string, a project.Agent, kind, in string, env map[string]string) error {
	req := AgentStartRequest{
		Kind: kind, EnvironmentID: envID, Worktree: a.Worktree, Isolate: a.Isolate, Base: a.Base,
		Name: a.Name, Args: a.Args, Env: env,
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

func (w *projectWorkspace) RestartAgent(ctx context.Context, id string, a project.Agent, env map[string]string) error {
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
	vars := maps.Clone(env)
	if vars == nil {
		vars = map[string]string{}
	}
	vars[agent.EnvAgent] = m.ID
	_, err = w.panes.Respawn(ctx, ag.PaneID, pane.Spec{Name: a.Name, Command: command, Env: vars})
	return err
}

func (w *projectWorkspace) CloseTab(ctx context.Context, id string) error {
	return w.panes.CloseTab(ctx, id)
}

// flatten is a tab's panes in order, and how its first split divides it.
func flatten(n *pane.NodeSpec) (project.Split, []project.Pane) {
	split := project.SplitHorizontal
	if n != nil && n.Pane == nil && n.Split == layout.Vertical {
		split = project.SplitVertical
	}
	var panes []project.Pane
	var walk func(*pane.NodeSpec)
	walk = func(n *pane.NodeSpec) {
		switch {
		case n == nil:
		case n.Pane != nil:
			panes = append(panes, project.Pane{Name: n.Pane.Name, Command: n.Pane.Command, Cwd: filepath.ToSlash(n.Pane.Cwd)})
		default:
			walk(n.First)
			walk(n.Second)
		}
	}
	walk(n)
	return split, panes
}

func cmpSlice(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}
