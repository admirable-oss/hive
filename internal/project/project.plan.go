package project

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// State is what runs, as plans compare it with a project file.
type State struct {
	Environments []EnvState
	// Tabs are each environment's, by its ID.
	Tabs   map[string][]TabState
	Agents []AgentState
	// Known are the agent kinds with manifests; Installed, those whose
	// programs are found.
	Known     []string
	Installed []string
	// Plugins are the installed plugins' IDs.
	Plugins []string
	// Toolchain finds an environment's toolchain (nil: none).
	Toolchain func(kind Toolchain, dir, root string) Resolved
}

// EnvState is an environment that exists.
type EnvState struct {
	ID   string
	Root string
	Vars map[string]string
	// WorktreeRepo and Branch are a worktree environment's repository and
	// branch.
	WorktreeRepo string
	Branch       string
}

// TabState is a tab that exists: its panes in order, and how the first
// split divides it.
type TabState struct {
	ID    string
	Name  string
	Split Split
	Panes []Pane
}

// AgentState is an agent that has a pane.
type AgentState struct {
	ID            string
	Name          string
	Kind          string
	EnvironmentID string
	TabID         string
	Exited        bool
}

// PlanUp is what makes st match spec. vars are the values the caller's
// shell has for the names the project declares; local holds the person's
// own. Values that are needed and missing are problems.
func PlanUp(spec Spec, local Local, vars map[string]string, st State) Plan {
	plan := Plan{Root: spec.Environments[0].Root}
	for _, id := range spec.Plugins {
		if !slices.Contains(st.Plugins, id) {
			plan.Plugins = append(plan.Plugins, id)
		}
	}
	for i := range spec.Environments {
		e := &spec.Environments[i]
		values, refs := resolveVars(*e, local, vars)
		for _, n := range e.Required {
			if values[n] == "" && refs[n] == "" {
				plan.Problems = append(plan.Problems, fmt.Sprintf("environment %s needs %s: set it in %s or in your shell", e.ID, n, LocalFileName))
			}
		}
		kind := e.Toolchain
		if local.Toolchain != "" {
			kind = local.Toolchain
		}
		tc := Resolved{Kind: ToolchainNone}
		if st.Toolchain != nil {
			tc = st.Toolchain(kind, e.Root, spec.Environments[0].Root)
		}
		if tc.Problem != "" && (len(e.Tabs) > 0 || len(e.Agents) > 0) {
			plan.Problems = append(plan.Problems, fmt.Sprintf("environment %s: %s", e.ID, tc.Problem))
		}
		starts := func(a *Action) {
			if tc.Kind != ToolchainNone && (tc.Kind != ToolchainDevcontainer || a.Op == OpCreateTab) {
				a.Toolchain, a.ToolchainCached, a.tc = tc.Kind, tc.Cached, tc
			}
			if len(refs) > 0 {
				a.Secrets, a.refs = slices.Sorted(maps.Keys(refs)), refs
			}
		}
		have, ok := st.environment(e.ID)
		switch {
		case ok && have.WorktreeRepo != "":
			plan.Problems = append(plan.Problems, fmt.Sprintf("environment %s exists as a worktree of %s: remove it or rename the project's (name = ...)", e.ID, have.WorktreeRepo))
			continue
		case ok && !samePath(have.Root, e.Root):
			plan.Problems = append(plan.Problems, fmt.Sprintf("environment %s exists at %s, not %s: remove it or rename the project's", e.ID, have.Root, e.Root))
			continue
		case !ok:
			plan.Actions = append(plan.Actions, Action{
				Op: OpCreateEnvironment, Environment: e.ID, Root: e.Root,
				Vars: slices.Sorted(maps.Keys(values)), values: values,
			})
		default:
			set := map[string]string{}
			for k, v := range values {
				if have.Vars[k] != v {
					set[k] = v
				}
			}
			if len(set) > 0 {
				plan.Actions = append(plan.Actions, Action{Op: OpSetVars, Environment: e.ID, Vars: slices.Sorted(maps.Keys(set)), values: set})
			} else {
				plan.Present++
			}
		}
		for j := range e.Tabs {
			t := &e.Tabs[j]
			if ok && slices.ContainsFunc(st.Tabs[e.ID], func(x TabState) bool { return x.Name == t.Name }) {
				plan.Present++
				continue
			}
			act := Action{Op: OpCreateTab, Environment: e.ID, Name: t.Name, Commands: commands(*t), tab: t}
			starts(&act)
			plan.Actions = append(plan.Actions, act)
		}
		for j := range e.Agents {
			a := &e.Agents[j]
			if a.Skip {
				continue
			}
			found, exists := AgentState{}, false
			if ok {
				found, exists = st.agent(*e, a.Name)
			}
			switch {
			case exists && !found.Exited:
				plan.Present++
			case exists:
				act := Action{Op: OpRestartAgent, Environment: e.ID, Name: a.Name, ID: found.ID, Kind: found.Kind, agent: a}
				starts(&act)
				plan.Actions = append(plan.Actions, act)
			default:
				kind, problem := chooseKind(*a, st)
				if problem != "" {
					plan.Problems = append(plan.Problems, fmt.Sprintf("environment %s: agent %s: %s", e.ID, a.Name, problem))
					continue
				}
				act := Action{
					Op: OpStartAgent, Environment: e.ID, Name: a.Name, Kind: kind,
					Worktree: a.Worktree, Isolate: a.Isolate, agent: a,
				}
				if a.Isolate || a.Worktree != "" {
					if w, kept := st.keptWorktree(*e, a.Name); kept {
						act.In, act.Worktree, act.Isolate = w.ID, w.Branch, false
					}
				}
				starts(&act)
				plan.Actions = append(plan.Actions, act)
			}
		}
	}
	return plan
}

// PlanDown is what stops a project: the agents and tabs it declares. Its
// environments, and the worktrees its agents work in, stay.
func PlanDown(spec Spec, st State) Plan {
	plan := Plan{Root: spec.Environments[0].Root, Down: true}
	for _, e := range spec.Environments {
		have, ok := st.environment(e.ID)
		if !ok || have.WorktreeRepo != "" || !samePath(have.Root, e.Root) {
			continue
		}
		for _, a := range e.Agents {
			if found, exists := st.agent(e, a.Name); exists {
				plan.Actions = append(plan.Actions, Action{Op: OpStopAgent, Environment: found.EnvironmentID, Name: a.Name, ID: found.ID, Kind: found.Kind, tabID: found.TabID})
			}
		}
		for _, t := range st.Tabs[e.ID] {
			if slices.ContainsFunc(e.Tabs, func(x Tab) bool { return x.Name == t.Name }) {
				plan.Actions = append(plan.Actions, Action{Op: OpCloseTab, Environment: e.ID, Name: t.Name, ID: t.ID})
			}
		}
	}
	return plan
}

// resolveVars are an environment's values and secret references. Later
// sources win: the project's settings, its secrets, the caller's shell
// (for declared names), the person's secrets, then hive.local.toml's
// values. Values go to the environment; secrets are read when panes start.
func resolveVars(e Environment, local Local, vars map[string]string) (values, refs map[string]string) {
	values, refs = maps.Clone(e.Vars), map[string]string{}
	if values == nil {
		values = map[string]string{}
	}
	secret := func(m map[string]string) {
		for k, r := range m {
			refs[k] = r
			delete(values, k)
		}
	}
	value := func(k, v string) {
		values[k] = v
		delete(refs, k)
	}
	secret(e.Secrets)
	for _, n := range append(slices.Clone(e.Required), e.Optional...) {
		if v, ok := vars[n]; ok && v != "" {
			value(n, v)
		}
	}
	secret(local.Secrets)
	for k, v := range local.Vars {
		value(k, v)
	}
	return values, refs
}

func chooseKind(a Agent, st State) (string, string) {
	for _, k := range a.Kinds {
		if !slices.Contains(st.Known, k) {
			return "", fmt.Sprintf("no agent manifest %q (see hive agent manifests)", k)
		}
	}
	for _, k := range a.Kinds {
		if slices.Contains(st.Installed, k) {
			return k, ""
		}
	}
	if len(a.Kinds) == 1 {
		return "", fmt.Sprintf("%s is not installed (its program is not on the daemon's PATH)", a.Kinds[0])
	}
	return "", fmt.Sprintf("none of %s is installed (their programs are not on the daemon's PATH)", strings.Join(a.Kinds, ", "))
}

func commands(t Tab) []string {
	out := make([]string, len(t.Panes))
	for i, p := range t.Panes {
		out[i] = CommandLine(p.Command)
	}
	return out
}

// CommandLine shows argv as a POSIX shell would read it.
func CommandLine(argv []string) string {
	out := make([]string, len(argv))
	for i, s := range argv {
		if s != "" && strings.IndexFunc(s, unsafeInShell) < 0 {
			out[i] = s
			continue
		}
		out[i] = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}

func unsafeInShell(r rune) bool {
	safe := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)
	return !safe
}

// keptWorktree is the worktree environment of e that hive up started the
// agent name in before.
func (st State) keptWorktree(e Environment, name string) (EnvState, bool) {
	for _, x := range st.Environments {
		if x.WorktreeRepo != "" && samePath(x.WorktreeRepo, e.Root) && x.Vars[EnvProjectAgent] == name {
			return x, true
		}
	}
	return EnvState{}, false
}

func (st State) environment(id string) (EnvState, bool) {
	for _, e := range st.Environments {
		if e.ID == id {
			return e, true
		}
	}
	return EnvState{}, false
}

// agent finds a project's agent by name: in its environment, or in a
// worktree of it, where isolate and worktree start agents. A running one
// wins over one that exited.
func (st State) agent(e Environment, name string) (AgentState, bool) {
	envs := map[string]bool{e.ID: true}
	for _, x := range st.Environments {
		if x.WorktreeRepo != "" && samePath(x.WorktreeRepo, e.Root) {
			envs[x.ID] = true
		}
	}
	var found AgentState
	exists := false
	for _, a := range st.Agents {
		if a.Name != name || !envs[a.EnvironmentID] {
			continue
		}
		if !exists || found.Exited && !a.Exited {
			found, exists = a, true
		}
	}
	return found, exists
}

// samePath says whether a and b are one directory, through symlinks (git
// reports a repository by its resolved path: /private/var on macOS for
// /var).
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
