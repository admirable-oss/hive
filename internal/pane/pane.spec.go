package pane

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/process"
)

// LayoutSpec is a declarative workspace: environments, their tabs and each
// tab's split tree with what every pane runs. `hive layout export` writes
// one; `hive layout apply` builds it.
//
//	{"version": 1, "environments": [{
//	   "id": "api", "root": "/home/me/api", "env": {"PORT": "8080"},
//	   "tabs": [{"name": "agents", "layout": {
//	     "split": "horizontal", "ratio": 0.6,
//	     "first":  {"pane": {"name": "claude", "command": ["claude"]}},
//	     "second": {"pane": {"name": "tests", "command": ["npm", "test", "--", "--watch"]}}}}]}]}
type LayoutSpec struct {
	Version      int               `json:"version"`
	Environments []EnvironmentSpec `json:"environments"`
}

// EnvironmentSpec is one environment of a LayoutSpec. Root is omitted for
// environments with a Hive-managed workspace.
type EnvironmentSpec struct {
	ID   string            `json:"id"`
	Root string            `json:"root,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	Tabs []TabSpec         `json:"tabs,omitempty"`
}

// TabSpec is one tab of a LayoutSpec.
type TabSpec struct {
	Name   string    `json:"name,omitempty"`
	Layout *NodeSpec `json:"layout"`
}

// NodeSpec is a pane (Pane set) or a split of two nodes.
type NodeSpec struct {
	Pane   *Spec              `json:"pane,omitempty"`
	Split  layout.Orientation `json:"split,omitempty"`
	Ratio  float64            `json:"ratio,omitempty"`
	First  *NodeSpec          `json:"first,omitempty"`
	Second *NodeSpec          `json:"second,omitempty"`
}

// SpecVersion is the LayoutSpec format this build reads and writes.
const SpecVersion = 1

// maxSpecPanes bounds one apply.
const maxSpecPanes = 256

// ApplyResult reports what Apply created.
type ApplyResult struct {
	CreatedEnvironments []string `json:"created_environments,omitempty"`
	Tabs                []Tab    `json:"tabs"`
	Panes               int      `json:"panes"`
}

// Validate checks a spec before anything is created.
func (sp *LayoutSpec) Validate() error {
	if sp.Version != SpecVersion {
		return fmt.Errorf("%w: layout version %d (this build reads %d)", ErrInvalid, sp.Version, SpecVersion)
	}
	panes := 0
	seen := map[string]bool{}
	for _, e := range sp.Environments {
		if !environment.ValidID(e.ID) {
			return fmt.Errorf("%w: environment id %q", ErrInvalid, e.ID)
		}
		if seen[e.ID] {
			return fmt.Errorf("%w: environment %q appears twice", ErrInvalid, e.ID)
		}
		seen[e.ID] = true
		if e.Root != "" && !filepath.IsAbs(e.Root) {
			return fmt.Errorf("%w: environment %q root must be absolute", ErrInvalid, e.ID)
		}
		for i, t := range e.Tabs {
			n, err := t.Layout.count()
			if err != nil {
				return fmt.Errorf("environment %q tab %d: %w", e.ID, i+1, err)
			}
			panes += n
		}
	}
	if panes > maxSpecPanes {
		return fmt.Errorf("%w: %d panes (limit %d)", ErrInvalid, panes, maxSpecPanes)
	}
	return nil
}

// count validates a node and returns its number of panes.
func (n *NodeSpec) count() (int, error) {
	switch {
	case n == nil:
		return 0, fmt.Errorf("%w: missing layout node", ErrInvalid)
	case n.Pane != nil:
		if n.Split != "" || n.First != nil || n.Second != nil {
			return 0, fmt.Errorf("%w: a node is either a pane or a split", ErrInvalid)
		}
		return 1, nil
	case n.Split != layout.Horizontal && n.Split != layout.Vertical:
		return 0, fmt.Errorf("%w: split %q (want horizontal or vertical)", ErrInvalid, n.Split)
	case n.Ratio < 0 || n.Ratio >= 1:
		return 0, fmt.Errorf("%w: ratio %v (want 0 < ratio < 1, or omit it)", ErrInvalid, n.Ratio)
	}
	a, err := n.First.count()
	if err != nil {
		return 0, err
	}
	b, err := n.Second.count()
	return a + b, err
}

// Apply builds a spec: missing environments are created, and every tab is
// added (existing tabs are left alone). If a pane fails to start, the tabs
// this apply created are closed again.
func (s *Service) Apply(ctx context.Context, sp LayoutSpec) (ApplyResult, error) {
	if err := sp.Validate(); err != nil {
		return ApplyResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var res ApplyResult
	rollback := func(cause error) (ApplyResult, error) {
		for _, t := range res.Tabs {
			s.closeTabLocked(ctx, t.ID)
		}
		return ApplyResult{}, cause
	}
	for _, e := range sp.Environments {
		env, err := s.envs.Get(ctx, e.ID)
		if errors.Is(err, environment.ErrNotFound) {
			env, err = s.envs.Create(ctx, environment.CreateRequest{ID: e.ID, Root: e.Root, Env: e.Env})
			if err == nil {
				res.CreatedEnvironments = append(res.CreatedEnvironments, e.ID)
			}
		}
		if err != nil {
			return rollback(fmt.Errorf("environment %q: %w", e.ID, err))
		}
		for _, ts := range e.Tabs {
			tab, n, err := s.applyTabLocked(ctx, env, ts)
			if err != nil {
				return rollback(fmt.Errorf("environment %q tab %q: %w", e.ID, ts.Name, err))
			}
			res.Tabs = append(res.Tabs, tab)
			res.Panes += n
		}
	}
	return res, nil
}

// applyTabLocked creates one tab from its spec, starting every pane at the
// size the layout gives it.
func (s *Service) applyTabLocked(ctx context.Context, env environment.Environment, ts TabSpec) (Tab, int, error) {
	st, _, err := s.stateLocked(ctx, env.ID)
	if err != nil {
		return Tab{}, 0, err
	}
	tab := Tab{
		ID: s.idSource("t"), EnvironmentID: env.ID, Name: ts.Name,
		Width: int(s.cfg.Size.Width), Height: int(s.cfg.Size.Height), CreatedAt: s.nowFn(),
	}
	if tab.Name == "" {
		tab.Name = fmt.Sprintf("tab %d", len(st.Tabs)+1)
	}
	specs := map[string]Spec{}
	var build func(*NodeSpec) *layout.Node
	build = func(n *NodeSpec) *layout.Node {
		if n.Pane != nil {
			id := s.idSource("p")
			specs[id] = *n.Pane
			return layout.Leaf(id)
		}
		return &layout.Node{Split: n.Split, Ratio: n.Ratio, First: build(n.First), Second: build(n.Second)}
	}
	tab.Layout = build(ts.Layout)
	tab.Layout.Normalize()
	if err := tab.Layout.Validate(); err != nil {
		return Tab{}, 0, err
	}
	geometry := layout.Geometry(tab.Layout, area(&tab), "")
	var panes []Pane
	for _, id := range tab.Layout.Panes() {
		spec := specs[id]
		proc, err := s.startLocked(ctx, env, tab.ID, id, spec, geometry[id])
		if err != nil {
			for _, p := range panes {
				_ = s.procs.Stop(ctx, p.ProcessID)
			}
			return Tab{}, 0, err
		}
		panes = append(panes, Pane{ID: id, TabID: tab.ID, EnvironmentID: env.ID, ProcessID: proc.ID, Name: spec.Name, CreatedAt: s.nowFn()})
	}
	tab.Focused = panes[0].ID
	st.Tabs = append(st.Tabs, tab)
	st.Panes = append(st.Panes, panes...)
	st.ActiveTab = tab.ID
	if err := s.saveLocked(ctx, env.ID); err != nil {
		return Tab{}, 0, err
	}
	s.events.Publish(EventTabCreated, tab)
	for _, p := range panes {
		s.events.Publish(EventPaneCreated, p)
	}
	return tab, len(panes), nil
}

// closeTabLocked closes a tab during a rollback, ignoring errors.
func (s *Service) closeTabLocked(ctx context.Context, tabID string) {
	st, t, err := s.tabLocked(ctx, tabID)
	if err != nil {
		return
	}
	for _, p := range slices.Clone(st.Panes) {
		if p.TabID == t.ID {
			s.stopLocked(ctx, p)
			st.Panes = slices.DeleteFunc(st.Panes, func(x Pane) bool { return x.ID == p.ID })
		}
	}
	envID := t.EnvironmentID
	s.removeTabLocked(st, tabID)
	_ = s.saveLocked(ctx, envID)
}

// Export describes the current workspace of the given environments (all
// when none are given) as a LayoutSpec. Popups are not exported: they live
// as long as their command.
func (s *Service) Export(ctx context.Context, envIDs []string) (LayoutSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(envIDs) == 0 {
		envs, err := s.envs.List(ctx)
		if err != nil {
			return LayoutSpec{}, err
		}
		for _, e := range envs {
			envIDs = append(envIDs, e.ID)
		}
	}
	sp := LayoutSpec{Version: SpecVersion}
	shell := s.shell()
	for _, id := range envIDs {
		st, env, err := s.stateLocked(ctx, id)
		if err != nil {
			return LayoutSpec{}, err
		}
		es := EnvironmentSpec{ID: env.ID, Env: maps.Clone(env.Env)}
		if !env.Managed {
			es.Root = env.Path
		}
		panes := map[string]Pane{}
		for _, p := range st.Panes {
			panes[p.ID] = p
		}
		for _, t := range st.Tabs {
			if t.Layout == nil {
				continue // only popups
			}
			es.Tabs = append(es.Tabs, TabSpec{Name: t.Name, Layout: s.exportNodeLocked(ctx, t.Layout, panes, env, shell)})
		}
		sp.Environments = append(sp.Environments, es)
	}
	return sp, nil
}

func (s *Service) exportNodeLocked(ctx context.Context, n *layout.Node, panes map[string]Pane, env environment.Environment, shell []string) *NodeSpec {
	if !n.IsLeaf() {
		return &NodeSpec{
			Split: n.Split, Ratio: n.Ratio,
			First:  s.exportNodeLocked(ctx, n.First, panes, env, shell),
			Second: s.exportNodeLocked(ctx, n.Second, panes, env, shell),
		}
	}
	p := panes[n.Pane]
	spec := Spec{Name: p.Name}
	if proc, err := s.procs.Get(ctx, p.ProcessID); err == nil {
		spec.Command, spec.Cwd, spec.Env = exportProcess(proc, env, shell)
	}
	return &NodeSpec{Pane: &spec}
}

// exportProcess returns how to start proc again: its command (omitted when
// it is the default shell), its directory relative to the environment's,
// and the variables it was given (minus the ones Hive sets per pane).
func exportProcess(proc process.Process, env environment.Environment, shell []string) ([]string, string, map[string]string) {
	argv := append([]string{proc.Command}, proc.Args...)
	if slices.Equal(argv, shell) {
		argv = nil
	}
	cwd := ""
	if proc.WorkingDir != env.Path {
		if rel, err := filepath.Rel(env.Path, proc.WorkingDir); err == nil && !strings.HasPrefix(rel, "..") {
			cwd = rel
		} else {
			cwd = proc.WorkingDir
		}
	}
	vars := maps.Clone(proc.Env)
	delete(vars, "HIVE_PANE_ID")
	delete(vars, "HIVE_TAB_ID")
	if len(vars) == 0 {
		vars = nil
	}
	return argv, cwd, vars
}
