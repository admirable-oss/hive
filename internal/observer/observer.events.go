package observer

import (
	"encoding/json"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/task"
)

// Event is something observers are told about. Seq is the daemon's event
// sequence number: it increases within an Epoch (one daemon run), with gaps
// where events were for others or not for observers at all. Loss is never
// signalled by a gap, only by a Resync event.
type Event struct {
	Seq  uint64          `json:"seq"`
	Type string          `json:"type"`
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data,omitempty"`

	target Target
}

// Resync tells an observer it missed events (it fell behind, or asked for
// history no longer kept): take a new snapshot (observe.snapshot) and
// continue from its Seq. Data is ResyncData.
const Resync = "observer.resync"

// ResyncData says why a resync is needed.
type ResyncData struct {
	// Reason is "lost" (events were dropped for this observer), "history"
	// (the requested position is no longer retained) or "epoch" (the
	// daemon restarted since).
	Reason string `json:"reason"`
	Epoch  string `json:"epoch"`
}

// Event types observers receive; any other daemon event is withheld.
// Data is the observer view named.
var Types = []string{
	event.EnvironmentCreated, // Environment
	event.EnvironmentUpdated, // Environment
	event.EnvironmentRemoved, // {"id"}
	event.EnvironmentGit,     // {"id", "git"}
	event.ProcessStarted,     // Process
	event.ProcessExited,      // Process
	event.ProcessRecovered,   // Process
	agent.EventOutput,        // {"id", "pane_id"}: a screen changed (at most 1/s per agent)
	pane.EventTabCreated,     // Tab
	pane.EventTabClosed,      // Tab
	pane.EventTabUpdated,     // Tab
	pane.EventLayoutUpdated,  // Tab
	pane.EventPaneCreated,    // Pane
	pane.EventPaneClosed,     // Pane
	pane.EventPaneMoved,      // Pane
	pane.EventPaneFocused,    // Pane
	pane.EventPaneUpdated,    // Pane
	agent.EventState,         // Agent
	metadata.EventChange,     // Metadata
	EventWorktreeCreated,     // Worktree
	EventWorktreeRemoved,     // Worktree
	task.EventTask,           // PlanTask: a task of a plan changed
	task.EventPlan,           // Plan without Tasks: how a plan stands (observers that see all)
	task.EventRemoved,        // {"ids"}: finished plans forgotten (observers that see all)
	insights.EventAlert,      // Alert: a budget warns or is exceeded (where its key is seen)
	EventPresenceChanged,     // {"people": []Present}: who is where (observers that see all)
	EventPaneFloor,           // Floor: who holds a pane's input now
	Resync,                   // ResyncData
}

// Worktree events, as the runtime publishes them.
const (
	EventWorktreeCreated = "worktree.created"
	EventWorktreeRemoved = "worktree.removed"
)

// Worktree is a worktree event as observers see it.
type Worktree struct {
	Path          string `json:"path"`
	Repo          string `json:"repo"`
	Branch        string `json:"branch,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
}

// Location is where a process is shown.
type Location struct {
	Environment string
	Tab         string
	Pane        string
}

// Locator finds where a process or a pane is; ok is false for one the
// daemon no longer knows.
type Locator interface {
	Process(id string) (Location, bool)
	Pane(id string) (Location, bool)
}

// Translate turns a daemon event into what observers see of it, and where
// it happened. ok is false for events observers are not told about.
func Translate(ev event.Event, locate Locator) (Event, bool) {
	var (
		data   any
		target Target
		err    error
	)
	switch ev.Type {
	case event.EnvironmentCreated, event.EnvironmentUpdated:
		var e environment.Environment
		err = json.Unmarshal(ev.Data, &e)
		data, target = EnvironmentView(e), Target{Environment: e.ID}
	case event.EnvironmentRemoved:
		var e struct {
			ID string `json:"id"`
		}
		err = json.Unmarshal(ev.Data, &e)
		data, target = e, Target{Environment: e.ID}
	case event.EnvironmentGit:
		var e struct {
			ID  string      `json:"id"`
			Git *git.Status `json:"git"`
		}
		err = json.Unmarshal(ev.Data, &e)
		data, target = e, Target{Environment: e.ID}
	case event.ProcessStarted, event.ProcessExited, event.ProcessRecovered:
		var p process.Process
		err = json.Unmarshal(ev.Data, &p)
		loc, _ := locate.Process(p.ID)
		data, target = ProcessView(p, loc.Pane), Target{Environment: p.EnvironmentID, Pane: loc.Pane}
	case agent.EventOutput:
		var p struct {
			ID string `json:"id"`
		}
		err = json.Unmarshal(ev.Data, &p)
		loc, ok := locate.Process(p.ID)
		if !ok {
			return Event{}, false
		}
		data = struct {
			ID     string `json:"id"`
			PaneID string `json:"pane_id,omitempty"`
		}{p.ID, loc.Pane}
		target = Target{Environment: loc.Environment, Pane: loc.Pane}
	case pane.EventTabCreated, pane.EventTabClosed, pane.EventTabUpdated, pane.EventLayoutUpdated:
		var t pane.Tab
		err = json.Unmarshal(ev.Data, &t)
		data, target = TabView(t), Target{Environment: t.EnvironmentID}
	case pane.EventPaneCreated, pane.EventPaneClosed, pane.EventPaneMoved, pane.EventPaneFocused, pane.EventPaneUpdated:
		var p pane.Pane
		err = json.Unmarshal(ev.Data, &p)
		data, target = PaneView(p), Target{Environment: p.EnvironmentID, Pane: p.ID}
	case agent.EventState:
		var a agent.Agent
		err = json.Unmarshal(ev.Data, &a)
		data, target = AgentView(a), Target{Environment: a.EnvironmentID, Pane: a.PaneID}
	case metadata.EventChange:
		var e metadata.Entry
		err = json.Unmarshal(ev.Data, &e)
		target = Target{Environment: e.Environment, Pane: e.Pane}
		if e.Pane != "" && e.Environment == "" {
			// A pane's entry: in scope wherever the pane is.
			if loc, ok := locate.Pane(e.Pane); ok {
				target.Environment = loc.Environment
			}
		}
		data = MetadataView(e)
	case EventWorktreeCreated, EventWorktreeRemoved:
		var w struct {
			Path        string `json:"path"`
			Repo        string `json:"repo"`
			Branch      string `json:"branch,omitempty"`
			Environment *struct {
				ID string `json:"id"`
			} `json:"environment,omitempty"`
		}
		err = json.Unmarshal(ev.Data, &w)
		v := Worktree{Path: w.Path, Repo: w.Repo, Branch: w.Branch}
		if w.Environment != nil {
			v.EnvironmentID = w.Environment.ID
		}
		data, target = v, Target{Environment: v.EnvironmentID}
	case task.EventTask:
		var v task.TaskView
		err = json.Unmarshal(ev.Data, &v)
		t := PlanTaskView(v)
		data, target = t, t.Target()
	case task.EventPlan:
		// Counts span every environment: only for observers that see all.
		var s task.Summary
		err = json.Unmarshal(ev.Data, &s)
		data = PlanSummaryView(s)
	case task.EventRemoved:
		var r struct {
			IDs []string `json:"ids"`
		}
		err = json.Unmarshal(ev.Data, &r)
		data = r
	case insights.EventAlert:
		var a insights.Alert
		err = json.Unmarshal(ev.Data, &a)
		data, target = a, Target{Environment: a.Target()}
	case EventPresenceChanged, EventPaneFloor:
		data, target, err = translatePresence(ev.Data, ev.Type)
	default:
		return Event{}, false
	}
	if err != nil {
		return Event{}, false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, false
	}
	return Event{Seq: ev.Seq, Type: ev.Type, Time: ev.Time, Data: raw, target: target}, true
}

// Visible reports whether ev is in scope s. Resync markers always are.
func (ev Event) Visible(s Scope) bool {
	return ev.Type == Resync || s.Sees(ev.target)
}

func resyncEvent(seq uint64, reason, epoch string, now time.Time) Event {
	raw, _ := json.Marshal(ResyncData{Reason: reason, Epoch: epoch})
	return Event{Seq: seq, Type: Resync, Time: now, Data: raw}
}
