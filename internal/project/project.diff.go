package project

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Change is one way the workspace differs from a project file.
type Change struct {
	// Kind is missing (the file declares it, and it does not run), extra
	// (it runs, and the file does not declare it) or changed.
	Kind        string `json:"kind"`
	Environment string `json:"environment"`
	// What is environment, vars, tab or agent.
	What   string `json:"what"`
	Name   string `json:"name,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// The kinds of changes.
const (
	ChangeMissing = "missing"
	ChangeExtra   = "extra"
	ChangeChanged = "changed"
)

// Diff is how the workspace differs from a project file.
type Diff struct {
	Root    string   `json:"root"`
	Changes []Change `json:"changes"`
	// Problems are the plan's: what hive up could not do as things are.
	Problems []string `json:"problems,omitempty"`
}

// DiffState compares st with spec: what hive up would make, and what runs
// that the file does not say or says otherwise.
func DiffState(spec Spec, local Local, vars map[string]string, st State) Diff {
	plan := PlanUp(spec, local, vars, st)
	d := Diff{Root: plan.Root, Changes: []Change{}, Problems: plan.Problems}
	for _, a := range plan.Actions {
		c := Change{Environment: a.Environment, Name: a.Name}
		switch a.Op {
		case OpCreateEnvironment:
			c.Kind, c.What, c.Name = ChangeMissing, "environment", ""
		case OpSetVars:
			c.Kind, c.What, c.Detail = ChangeChanged, "vars", strings.Join(a.Vars, ", ")
		case OpCreateTab:
			c.Kind, c.What = ChangeMissing, "tab"
		case OpStartAgent:
			c.Kind, c.What = ChangeMissing, "agent"
		case OpRestartAgent:
			c.Kind, c.What, c.Detail = ChangeChanged, "agent", "exited"
		}
		d.Changes = append(d.Changes, c)
	}
	for _, id := range plan.Plugins {
		d.Changes = append(d.Changes, Change{Kind: ChangeMissing, What: "plugin", Name: id})
	}
	for _, e := range spec.Environments {
		have, ok := st.environment(e.ID)
		if !ok || have.WorktreeRepo != "" || !samePath(have.Root, e.Root) {
			continue
		}
		agentTabs := st.agentTabs(e)
		for _, t := range st.Tabs[e.ID] {
			if agentTabs[t.ID] {
				continue
			}
			i := slices.IndexFunc(e.Tabs, func(x Tab) bool { return x.Name == t.Name })
			if i < 0 {
				d.Changes = append(d.Changes, Change{Kind: ChangeExtra, Environment: e.ID, What: "tab", Name: t.Name, Detail: commandsText(t.Panes)})
				continue
			}
			if detail := tabDifference(e.Tabs[i], t, e.Root); detail != "" {
				d.Changes = append(d.Changes, Change{Kind: ChangeChanged, Environment: e.ID, What: "tab", Name: t.Name, Detail: detail})
			}
		}
		for _, a := range st.Agents {
			if !st.inProject(e, a.EnvironmentID) || slices.ContainsFunc(e.Agents, func(x Agent) bool { return x.Name == a.Name }) {
				continue
			}
			d.Changes = append(d.Changes, Change{Kind: ChangeExtra, Environment: a.EnvironmentID, What: "agent", Name: a.Name, Detail: a.Kind})
		}
	}
	return d
}

// SaveText is the project file with the running tabs written back: tabs
// opened since are added, and declared tabs take the panes they have now.
// Nothing is removed. The changes say what was written.
func SaveText(spec Spec, st State) ([]byte, []Change) {
	f := spec.file
	f.Environment = slices.Clone(f.Environment)
	var changes []Change
	for i, e := range spec.Environments {
		have, ok := st.environment(e.ID)
		if !ok || have.WorktreeRepo != "" || !samePath(have.Root, e.Root) {
			continue
		}
		tabs := &f.Tab
		if i > 0 {
			tabs = &f.Environment[i-1].Tab
		}
		*tabs = slices.Clone(*tabs)
		agentTabs := st.agentTabs(e)
		for _, t := range st.Tabs[e.ID] {
			if agentTabs[t.ID] || len(t.Panes) == 0 {
				continue
			}
			ft := fileTabOf(t, e.Root)
			j := slices.IndexFunc(*tabs, func(x fileTab) bool { return x.Name == t.Name })
			switch {
			case j < 0:
				*tabs = append(*tabs, ft)
				changes = append(changes, Change{Kind: ChangeExtra, Environment: e.ID, What: "tab", Name: t.Name, Detail: commandsText(t.Panes)})
			case tabDifference(e.Tabs[slices.IndexFunc(e.Tabs, func(x Tab) bool { return x.Name == t.Name })], t, e.Root) != "":
				(*tabs)[j] = ft
				changes = append(changes, Change{Kind: ChangeChanged, Environment: e.ID, What: "tab", Name: t.Name, Detail: commandsText(t.Panes)})
			}
		}
	}
	return Render(f), changes
}

// fileTabOf writes a running tab as a project file does: its panes in a
// row or a column, their directories relative to the environment's.
func fileTabOf(t TabState, root string) fileTab {
	ft := fileTab{Name: t.Name}
	if t.Split == SplitVertical && len(t.Panes) > 1 {
		ft.Split = string(SplitVertical)
	}
	for _, p := range t.Panes {
		fp := filePane{Name: p.Name}
		if len(p.Command) > 0 {
			fp.Command = slices.Clone(p.Command)
		}
		if cwd := relCwd(p.Cwd, root); cwd != "" && cwd != "." {
			fp.Cwd = cwd
		}
		ft.Pane = append(ft.Pane, fp)
	}
	return ft
}

// relCwd is a pane's directory as a project file writes it: relative and
// inside the environment, or "" when it is elsewhere.
func relCwd(cwd, root string) string {
	if cwd == "" {
		return ""
	}
	if filepath.IsAbs(cwd) {
		rel, err := filepath.Rel(root, cwd)
		if err != nil {
			return ""
		}
		cwd = rel
	}
	p, err := localPath(cwd)
	if err != nil {
		return ""
	}
	return p
}

// tabDifference says how a running tab differs from its declaration, or
// "".
func tabDifference(want Tab, have TabState, root string) string {
	if len(have.Panes) == 0 {
		return "" // not known: nothing to compare
	}
	if len(want.Panes) != len(have.Panes) {
		return fmt.Sprintf("%d panes, declared %d", len(have.Panes), len(want.Panes))
	}
	if len(want.Panes) > 1 && want.Split != have.Split {
		return fmt.Sprintf("split %s, declared %s", have.Split, want.Split)
	}
	for i, w := range want.Panes {
		h := have.Panes[i]
		switch {
		case !slices.Equal(w.Command, unwrap(h.Command)):
			return fmt.Sprintf("pane %d runs %s, declared %s", i+1, cmdOrShell(unwrap(h.Command)), cmdOrShell(w.Command))
		case w.Cwd != "" && w.Cwd != relCwd(h.Cwd, root):
			return fmt.Sprintf("pane %d is in %s, declared %s", i+1, cmpEmpty(h.Cwd, "."), w.Cwd)
		}
	}
	return ""
}

// unwrap is a pane's command without the devcontainer exec around it.
func unwrap(argv []string) []string {
	if len(argv) >= 4 && argv[0] == "devcontainer" && argv[1] == "exec" && argv[2] == "--workspace-folder" {
		inner := argv[4:]
		if slices.Equal(inner, containerShellArgv) {
			return nil
		}
		return inner
	}
	return argv
}

// containerShellArgv is the shell a devcontainer pane runs without a
// command (runtime's containerShell).
var containerShellArgv = []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash -l || exec sh -l"}

// ContainerShell is the command a devcontainer's pane runs when it has
// none.
func ContainerShell() []string { return slices.Clone(containerShellArgv) }

func cmdOrShell(argv []string) string {
	if len(argv) == 0 {
		return "a shell"
	}
	return CommandLine(argv)
}

func cmpEmpty(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func commandsText(ps []Pane) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = cmdOrShell(unwrap(p.Command))
	}
	return strings.Join(out, " | ")
}

// agentTabs are the tabs agents of e's environment and worktrees run in.
func (st State) agentTabs(e Environment) map[string]bool {
	out := map[string]bool{}
	for _, a := range st.Agents {
		if st.inProject(e, a.EnvironmentID) {
			out[a.TabID] = true
		}
	}
	return out
}

// inProject says whether envID is e's environment or a worktree of it.
func (st State) inProject(e Environment, envID string) bool {
	if envID == e.ID {
		return true
	}
	x, ok := st.environment(envID)
	return ok && x.WorktreeRepo != "" && samePath(x.WorktreeRepo, e.Root)
}
