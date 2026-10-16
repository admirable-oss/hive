package web

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
)

// Connection states of a runtime, as the browser shows them.
const (
	StateIdle       = "idle"         // nobody is looking; not connected
	StateConnecting = "connecting"   // first connection
	StateLive       = "live"         // connected and current
	StateRetrying   = "reconnecting" // lost; showing what was last known
	StateRefused    = "refused"      // the credential was refused (revoked, expired)
)

// activityLimit bounds a runtime's activity feed.
const activityLimit = 200

// Activity is one line of a runtime's activity feed.
type Activity struct {
	Seq         uint64
	Time        time.Time
	Environment string
	Pane        string
	Kind        string // agent, pane, process, environment, tab, worktree
	Text        string
}

// Model is what the web observer knows of one runtime: a snapshot kept
// current by events. It is safe for concurrent use; readers take a View.
type Model struct {
	mu sync.RWMutex
	v  RuntimeView
}

// RuntimeView is a copy of a model at one moment.
type RuntimeView struct {
	State    string
	Err      string
	SyncedAt time.Time // the last time the runtime was known current
	Status   *observer.Status
	Epoch    string
	Seq      uint64
	Version  uint64 // bumps on every change

	Envs     map[string]observer.Environment
	Tabs     map[string]observer.Tab
	Panes    map[string]observer.Pane
	Agents   map[string]observer.Agent
	Meta     map[observer.Target]observer.Metadata
	Output   map[string]time.Time // pane → when its screen last changed
	Activity []Activity           // newest last
	loaded   bool
}

// NewModel returns an empty model.
func NewModel() *Model {
	return &Model{v: RuntimeView{State: StateIdle}}
}

// View returns a copy of the model for rendering.
func (m *Model) View() RuntimeView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v := m.v
	v.Envs = cloneMap(v.Envs)
	v.Tabs = cloneMap(v.Tabs)
	v.Panes = cloneMap(v.Panes)
	v.Agents = cloneMap(v.Agents)
	v.Meta = cloneMap(v.Meta)
	v.Output = cloneMap(v.Output)
	v.Activity = slices.Clone(v.Activity)
	return v
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Loaded reports whether a snapshot was ever applied.
func (v RuntimeView) Loaded() bool { return v.loaded }

// setState records the connection's state.
func (m *Model) setState(state, errText string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.v.State == state && m.v.Err == errText {
		return
	}
	m.v.State, m.v.Err = state, errText
	m.v.Version++
}

// synced records that the runtime answered (a status, an event).
func (m *Model) synced(st *observer.Status, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.v.SyncedAt = at
	if st != nil {
		m.v.Status = st
	}
	m.v.Version++
}

// Apply records a snapshot or an event. Events at or before the model's
// position are ignored (they are already in it), and an agent's state never
// goes back to an older one, so nothing old overwrites something newer.
func (m *Model) Apply(u observer.Update, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u.Snapshot != nil {
		m.snapshot(*u.Snapshot)
		m.v.SyncedAt = at
		m.v.Version++
		return
	}
	ev := u.Event
	if ev == nil || (m.v.loaded && ev.Seq <= m.v.Seq) {
		return
	}
	m.v.Seq = ev.Seq
	m.v.SyncedAt = at
	if m.event(*ev) {
		m.v.Version++
	}
}

func (m *Model) snapshot(s observer.Snapshot) {
	v := &m.v
	v.Epoch, v.Seq, v.loaded = s.Epoch, s.Seq, true
	v.Envs, v.Tabs, v.Panes, v.Agents = map[string]observer.Environment{}, map[string]observer.Tab{}, map[string]observer.Pane{}, map[string]observer.Agent{}
	v.Meta = map[observer.Target]observer.Metadata{}
	if v.Output == nil {
		v.Output = map[string]time.Time{}
	}
	for _, e := range s.Environments {
		v.Envs[e.ID] = e
	}
	for _, t := range s.Tabs {
		v.Tabs[t.ID] = t
	}
	for _, p := range s.Panes {
		v.Panes[p.ID] = p
	}
	for _, a := range s.Agents {
		v.Agents[a.ID] = a
	}
	for _, md := range s.Metadata {
		v.Meta[observer.Target{Environment: md.Environment, Pane: md.Pane}] = md
	}
}

// event applies one event and reports whether anything shown changed.
func (m *Model) event(ev observer.Event) bool {
	v := &m.v
	if !v.loaded {
		return false
	}
	note := func(env, pane, kind, text string) {
		v.Activity = append(v.Activity, Activity{Seq: ev.Seq, Time: ev.Time, Environment: env, Pane: pane, Kind: kind, Text: text})
		if len(v.Activity) > activityLimit {
			v.Activity = slices.Delete(v.Activity, 0, len(v.Activity)-activityLimit)
		}
	}
	switch ev.Type {
	case "environment.created", "environment.updated":
		var e observer.Environment
		if json.Unmarshal(ev.Data, &e) != nil {
			return false
		}
		if _, ok := v.Envs[e.ID]; !ok {
			note(e.ID, "", "environment", "environment "+e.ID+" created")
		}
		v.Envs[e.ID] = e
	case "environment.removed":
		var e struct{ ID string }
		if json.Unmarshal(ev.Data, &e) != nil {
			return false
		}
		delete(v.Envs, e.ID)
		for id, t := range v.Tabs {
			if t.EnvironmentID == e.ID {
				delete(v.Tabs, id)
			}
		}
		for id, p := range v.Panes {
			if p.EnvironmentID == e.ID {
				delete(v.Panes, id)
			}
		}
		for id, a := range v.Agents {
			if a.EnvironmentID == e.ID {
				delete(v.Agents, id)
			}
		}
		note(e.ID, "", "environment", "environment "+e.ID+" removed")
	case "environment.git":
		var g struct {
			ID  string          `json:"id"`
			Git json.RawMessage `json:"git"`
		}
		if json.Unmarshal(ev.Data, &g) != nil {
			return false
		}
		e, ok := v.Envs[g.ID]
		if !ok {
			return false
		}
		e.Git = nil
		_ = json.Unmarshal(g.Git, &e.Git)
		v.Envs[g.ID] = e
	case "tab.created", "tab.updated", "layout.updated":
		var t observer.Tab
		if json.Unmarshal(ev.Data, &t) != nil {
			return false
		}
		if _, ok := v.Tabs[t.ID]; !ok {
			note(t.EnvironmentID, "", "tab", fmt.Sprintf("tab %q opened", t.Name))
		}
		v.Tabs[t.ID] = t
	case "tab.closed":
		var t observer.Tab
		if json.Unmarshal(ev.Data, &t) != nil {
			return false
		}
		delete(v.Tabs, t.ID)
		note(t.EnvironmentID, "", "tab", fmt.Sprintf("tab %q closed", t.Name))
	case "pane.created", "pane.updated", "pane.moved", "pane.focused":
		var p observer.Pane
		if json.Unmarshal(ev.Data, &p) != nil {
			return false
		}
		old, existed := v.Panes[p.ID]
		if p.Process == nil {
			p.Process = old.Process
		}
		v.Panes[p.ID] = p
		if !existed {
			note(p.EnvironmentID, p.ID, "pane", "pane "+paneLabel(p)+" opened")
		}
	case "pane.closed":
		var p observer.Pane
		if json.Unmarshal(ev.Data, &p) != nil {
			return false
		}
		delete(v.Panes, p.ID)
		for id, a := range v.Agents {
			if a.PaneID == p.ID {
				delete(v.Agents, id)
			}
		}
		note(p.EnvironmentID, p.ID, "pane", "pane "+paneLabel(p)+" closed")
	case "process.started", "process.exited", "process.recovered":
		var p observer.Process
		if json.Unmarshal(ev.Data, &p) != nil {
			return false
		}
		for id, pn := range v.Panes {
			if pn.ProcessID == p.ID {
				pc := p
				pn.Process = &pc
				v.Panes[id] = pn
			}
		}
		if ev.Type == "process.exited" {
			text := p.Program + " exited"
			if p.ExitCode != nil {
				text += fmt.Sprintf(" (%d)", *p.ExitCode)
			}
			note(p.EnvironmentID, p.PaneID, "process", text)
		}
	case "process.output":
		var o struct {
			PaneID string `json:"pane_id"`
		}
		if json.Unmarshal(ev.Data, &o) != nil || o.PaneID == "" {
			return false
		}
		v.Output[o.PaneID] = ev.Time
		return false // news for a watched screen, not a change to show
	case "agent.state":
		var a observer.Agent
		if json.Unmarshal(ev.Data, &a) != nil {
			return false
		}
		if old, ok := v.Agents[a.ID]; ok && old.StateSeq > a.StateSeq {
			return false // older than what is shown
		}
		old, existed := v.Agents[a.ID]
		// Keep where it is when an event does not say (daemons before
		// 1.0.6 published agent states without their environment).
		if a.EnvironmentID == "" {
			a.EnvironmentID = old.EnvironmentID
		}
		if a.PaneID == "" {
			a.PaneID, a.TabID = old.PaneID, old.TabID
		}
		v.Agents[a.ID] = a
		if !existed || old.State != a.State {
			text := fmt.Sprintf("%s is %s", a.Name, a.State)
			if a.Reason != "" {
				text += ": " + a.Reason
			}
			note(a.EnvironmentID, a.PaneID, "agent", text)
		}
	case "metadata.changed":
		var md observer.Metadata
		if json.Unmarshal(ev.Data, &md) != nil {
			return false
		}
		t := observer.Target{Environment: md.Environment, Pane: md.Pane}
		if md.Title == "" && len(md.Tokens) == 0 {
			delete(v.Meta, t)
		} else {
			v.Meta[t] = md
		}
	case "worktree.created", "worktree.removed":
		var w observer.Worktree
		if json.Unmarshal(ev.Data, &w) != nil {
			return false
		}
		note(w.EnvironmentID, "", "worktree", strings.TrimPrefix(ev.Type, "worktree.")+" worktree "+w.Branch)
	default:
		return false
	}
	return true
}

func paneLabel(p observer.Pane) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Process != nil && p.Process.Program != "" {
		return p.Process.Program
	}
	return p.ID
}
