// Package core is the desktop app's engine, without any window: it keeps
// a connection to a Hive daemon, a view model of its workspace for the
// frontend, a stream of screen updates per visible pane, and notifications
// for agents that need someone. The Wails layer (package main) only binds
// it to windows, the tray and the system.
//
// Everything goes through the Go SDK (pkg/hiveapi): the app is a client of
// the daemon like the CLI and the TUI, and never manages processes itself.
package core

import (
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Connection states.
const (
	// StateConnecting: looking for the daemon, or starting it.
	StateConnecting = "connecting"
	// StateConnected: the daemon answers and events flow.
	StateConnected = "connected"
	// StateOffline: no daemon, and none could be started; it retries.
	StateOffline = "offline"
	// StateOutdated: the daemon is older than this app understands.
	StateOutdated = "outdated"
)

// Connection is how the app is connected, as the frontend shows it.
type Connection struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	// Version and APILevel are the daemon's, once connected.
	Version  string `json:"version,omitempty"`
	APILevel int    `json:"api_level,omitempty"`
	Session  string `json:"session"`
}

// Workspace is what the frontend shows: environments, tabs, panes and
// agents. It leaves out what screens do not need and secrets may hide in:
// environment variables, command lines and arguments.
type Workspace struct {
	Environments []Environment `json:"environments"`
	Tabs         []Tab         `json:"tabs"`
	Panes        []Pane        `json:"panes"`
	Agents       []Agent       `json:"agents"`
	// State is the most urgent agent state of all (blocked, working, …).
	State string `json:"state"`
	// Counts are agents by state.
	Counts map[string]int `json:"counts"`
	// Plans are the running plans (hive task), then the latest finished.
	Plans []Plan `json:"plans,omitempty"`
	// Branches are the active branches of agents' worktrees (hive branch).
	Branches []Branch `json:"branches,omitempty"`
	// Workflows are the latest workflow runs (hive workflow), newest first.
	Workflows []WorkflowRun `json:"workflows,omitempty"`
}

// Plan is a plan of tasks for agents and its progress.
type Plan struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	State string `json:"state"`
	Total int    `json:"total"`
	Done  int    `json:"done"`
	// Counts are its tasks by state.
	Counts  map[string]int `json:"counts"`
	Created time.Time      `json:"created"`
	Ended   time.Time      `json:"ended,omitzero"`
	Reason  string         `json:"reason,omitempty"`
	// Waiting are its tasks that wait for someone: an approval, or an
	// agent blocked on a decision.
	Waiting []PlanTask `json:"waiting"`
	// Machine is the saved machine it runs on ("" for this computer).
	Machine string `json:"machine,omitempty"`
}

// PlanTask is a task of a plan.
type PlanTask struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Kind   string `json:"kind"`
	State  string `json:"state"`
	PaneID string `json:"pane_id,omitempty"`
}

// shownFinishedPlans is how many finished plans the app shows.
const shownFinishedPlans = 5

// plansOf turns plan summaries (newest first) into the view model: every
// running plan and the latest finished ones.
func plansOf(list []hiveapi.PlanSummary) []Plan {
	out := []Plan{}
	finished := 0
	for _, s := range list {
		if s.State != hiveapi.PlanRunning {
			if finished == shownFinishedPlans {
				continue
			}
			finished++
		}
		p := Plan{
			ID: s.ID, Name: s.Name, State: string(s.State), Total: s.Total, Done: s.Counts[hiveapi.TaskSucceeded],
			Counts: make(map[string]int, len(s.Counts)), Created: s.CreatedAt, Ended: s.EndedAt, Reason: s.Reason,
			Waiting: []PlanTask{},
		}
		for k, v := range s.Counts {
			p.Counts[string(k)] = v
		}
		for _, t := range s.Attention {
			p.Waiting = append(p.Waiting, PlanTask{ID: t.ID, Title: t.Title, Kind: t.Kind, State: string(t.State), PaneID: t.PaneID})
		}
		out = append(out, p)
	}
	return out
}

// Environment is a place agents work in.
type Environment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty,omitempty"`
	State  string `json:"state,omitempty"`
	// Worktree is set for an environment made for a git worktree.
	Worktree bool `json:"worktree,omitempty"`
	// Machine is the saved machine it is on ("" for this computer).
	Machine string `json:"machine,omitempty"`
}

// Tab is a tab of an environment.
type Tab struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environment_id"`
	Name          string `json:"name"`
	Active        bool   `json:"active,omitempty"`
	Focused       string `json:"focused,omitempty"`
	Zoomed        string `json:"zoomed,omitempty"`
	// Width and Height are its area in cells; pane rects are within it.
	Width  int    `json:"width"`
	Height int    `json:"height"`
	State  string `json:"state,omitempty"`
}

// Pane is a terminal in a tab.
type Pane struct {
	ID            string `json:"id"`
	TabID         string `json:"tab_id"`
	EnvironmentID string `json:"environment_id"`
	ProcessID     string `json:"process_id"`
	Name          string `json:"name,omitempty"`
	// Rect is its place in the tab, in cells; nil when hidden (another
	// pane is zoomed).
	Rect    *Rect `json:"rect,omitempty"`
	Focused bool  `json:"focused,omitempty"`
	Zoomed  bool  `json:"zoomed,omitempty"`
	Popup   bool  `json:"popup,omitempty"`
	// Running says its process runs; Exit is its exit code once it ended.
	Running bool `json:"running"`
	Exit    *int `json:"exit,omitempty"`
	// Agent is the agent in it, if any.
	Agent string `json:"agent,omitempty"`
}

// Rect is a place in a tab, in cells.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Agent is a coding agent in a pane.
type Agent struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Name          string    `json:"name"`
	EnvironmentID string    `json:"environment_id"`
	TabID         string    `json:"tab_id,omitempty"`
	PaneID        string    `json:"pane_id,omitempty"`
	State         string    `json:"state"`
	Since         time.Time `json:"since"`
	Reason        string    `json:"reason,omitempty"`
	StateSeq      uint64    `json:"state_seq"`
	// Machine is the saved machine it runs on ("" for this computer).
	Machine string `json:"machine,omitempty"`
}

// Manifest is an agent Hive can start.
type Manifest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// workspaceOf turns a daemon snapshot into the view model.
func workspaceOf(s hiveapi.Snapshot) Workspace {
	w := Workspace{
		Environments: []Environment{}, Tabs: []Tab{}, Panes: []Pane{}, Agents: []Agent{},
		State: string(s.Rollups.Session), Counts: map[string]int{}, Plans: []Plan{}, Branches: []Branch{}, Workflows: []WorkflowRun{},
	}
	for _, e := range s.Environments {
		env := Environment{ID: e.ID, Name: cmp(e.Name, e.ID), Path: e.Path, State: string(s.Rollups.Environments[e.ID]), Worktree: e.Worktree != nil}
		if e.Git != nil {
			env.Branch, env.Dirty = e.Git.Branch, e.Git.Staged+e.Git.Modified > 0
		}
		w.Environments = append(w.Environments, env)
	}
	for _, t := range s.Tabs {
		w.Tabs = append(w.Tabs, Tab{
			ID: t.ID, EnvironmentID: t.EnvironmentID, Name: t.Name, Active: t.Active, Focused: t.Focused,
			Zoomed: t.Zoomed, Width: t.Width, Height: t.Height, State: string(s.Rollups.Tabs[t.ID]),
		})
	}
	procs := map[string]hiveapi.Process{}
	for _, p := range s.Processes {
		procs[p.ID] = p
	}
	agentOf := map[string]string{}
	for _, a := range s.Agents {
		if a.Kind == "" {
			continue // a terminal no manifest recognises: a shell, not an agent
		}
		agentOf[a.PaneID] = a.ID
		w.Agents = append(w.Agents, Agent{
			ID: a.ID, Kind: a.Kind, Name: a.Name, EnvironmentID: a.EnvironmentID, TabID: a.TabID, PaneID: a.PaneID,
			State: string(a.State), Since: a.Since, Reason: a.Reason, StateSeq: a.StateSeq,
		})
		w.Counts[string(a.State)]++
	}
	for _, p := range s.Panes {
		pn := Pane{
			ID: p.ID, TabID: p.TabID, EnvironmentID: p.EnvironmentID, ProcessID: p.ProcessID, Name: p.Name,
			Focused: p.Focused, Zoomed: p.Zoomed, Popup: p.Popup, Agent: agentOf[p.ID],
		}
		if p.Rect != nil {
			pn.Rect = &Rect{X: p.Rect.X, Y: p.Rect.Y, W: p.Rect.W, H: p.Rect.H}
		}
		proc, ok := procs[p.ProcessID]
		if p.Process != nil {
			proc, ok = *p.Process, true
		}
		if ok {
			pn.Running, pn.Exit = proc.Active(), proc.ExitCode
		}
		w.Panes = append(w.Panes, pn)
	}
	return w
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
