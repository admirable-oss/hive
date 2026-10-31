package observer

import (
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/vt"
)

// The views below are what observers see of the daemon's state. Each is
// built field by field from the daemon's own types, so a field added there
// stays hidden from observers until it is added here on purpose. None
// carries environment variables, command arguments, working directories of
// processes, process IDs or agents' own session IDs.

// Environment is an environment as observers see it.
type Environment struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Path      string                `json:"path"`
	Worktree  *environment.Worktree `json:"worktree,omitempty"`
	Status    environment.Status    `json:"status"`
	Git       *git.Status           `json:"git,omitempty"`
	CreatedAt time.Time             `json:"created_at"`
}

// EnvironmentView redacts e.
func EnvironmentView(e environment.Environment) Environment {
	return Environment{ID: e.ID, Name: e.Name, Path: e.Path, Worktree: e.Worktree, Status: e.Status, Git: e.Git, CreatedAt: e.CreatedAt}
}

// Process is a process as observers see it: its program's name, not its
// command line.
type Process struct {
	ID            string         `json:"id"`
	EnvironmentID string         `json:"environment_id"`
	PaneID        string         `json:"pane_id,omitempty"`
	Program       string         `json:"program"`
	Status        process.Status `json:"status"`
	ExitCode      *int           `json:"exit_code,omitempty"`
	Terminal      bool           `json:"terminal"`
	StartedAt     time.Time      `json:"started_at"`
	EndedAt       *time.Time     `json:"ended_at,omitempty"`
}

// ProcessView redacts p; paneID is the pane showing it, if any.
func ProcessView(p process.Process, paneID string) Process {
	return Process{
		ID: p.ID, EnvironmentID: p.EnvironmentID, PaneID: paneID, Program: program(p.Command),
		Status: p.Status, ExitCode: p.ExitCode, Terminal: p.Terminal, StartedAt: p.StartedAt, EndedAt: p.EndedAt,
	}
}

// program is a command's base name, without its directory (which can name
// a user's private paths).
func program(command string) string {
	if command == "" {
		return ""
	}
	return filepath.Base(filepath.FromSlash(command))
}

// Tab is a tab as observers see it.
type Tab struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	Name          string    `json:"name"`
	Panes         []string  `json:"panes"`
	Popups        []string  `json:"popups,omitempty"`
	Focused       string    `json:"focused,omitempty"`
	Zoomed        string    `json:"zoomed,omitempty"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	Active        bool      `json:"active,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// TabView redacts t.
func TabView(t pane.Tab) Tab {
	v := Tab{
		ID: t.ID, EnvironmentID: t.EnvironmentID, Name: t.Name, Focused: t.Focused, Zoomed: t.Zoomed,
		Width: t.Width, Height: t.Height, Active: t.Active, CreatedAt: t.CreatedAt, Panes: []string{},
	}
	if t.Layout != nil {
		v.Panes = t.Layout.Panes()
	}
	for _, p := range t.Popups {
		v.Popups = append(v.Popups, p.Pane)
	}
	return v
}

// Pane is a pane as observers see it.
type Pane struct {
	ID            string    `json:"id"`
	TabID         string    `json:"tab_id"`
	EnvironmentID string    `json:"environment_id"`
	ProcessID     string    `json:"process_id"`
	Name          string    `json:"name,omitempty"`
	Rect          *Rect     `json:"rect,omitempty"`
	Focused       bool      `json:"focused,omitempty"`
	Zoomed        bool      `json:"zoomed,omitempty"`
	Popup         bool      `json:"popup,omitempty"`
	Process       *Process  `json:"process,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// Rect is a pane's area in its tab, in cells; nil when another pane is
// zoomed.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

func rectView(r *layout.Rect) *Rect {
	if r == nil {
		return nil
	}
	return &Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
}

// PaneView redacts p.
func PaneView(p pane.Pane) Pane {
	v := Pane{
		ID: p.ID, TabID: p.TabID, EnvironmentID: p.EnvironmentID, ProcessID: p.ProcessID, Name: p.Name,
		Rect: rectView(p.Rect), Focused: p.Focused, Zoomed: p.Zoomed, Popup: p.Popup, CreatedAt: p.CreatedAt,
	}
	if p.Process != nil {
		proc := ProcessView(*p.Process, p.ID)
		v.Process = &proc
	}
	return v
}

// Agent is an agent as observers see it: what it is and its state.
type Agent struct {
	ID            string       `json:"id"`
	Kind          string       `json:"kind"`
	Name          string       `json:"name"`
	EnvironmentID string       `json:"environment_id"`
	TabID         string       `json:"tab_id,omitempty"`
	PaneID        string       `json:"pane_id,omitempty"`
	State         agent.State  `json:"state"`
	Source        agent.Source `json:"source,omitempty"`
	Since         time.Time    `json:"since"`
	StateSeq      uint64       `json:"state_seq"`
	CompletionSeq uint64       `json:"completion_seq"`
	LastOutput    time.Time    `json:"last_output,omitzero"`
	Reason        string       `json:"reason,omitempty"`
}

// AgentView redacts a.
func AgentView(a agent.Agent) Agent {
	return Agent{
		ID: a.ID, Kind: a.Kind, Name: a.Name, EnvironmentID: a.EnvironmentID, TabID: a.TabID, PaneID: a.PaneID,
		State: a.State, Source: a.Source, Since: a.Since, StateSeq: a.StateSeq, CompletionSeq: a.CompletionSeq,
		LastOutput: a.LastOutput, Reason: a.Reason,
	}
}

// Plan is a plan (hive task) as observers see it: its tasks' progress,
// without their prompts, results or logs. Total and Counts are of the
// tasks the observer sees; Tasks is nil in a task.plan event, which says
// only how the plan as a whole stands.
type Plan struct {
	ID        string             `json:"id"`
	Name      string             `json:"name,omitempty"`
	State     task.PlanState     `json:"state"`
	Rev       uint64             `json:"rev"`
	Total     int                `json:"total"`
	Counts    map[task.State]int `json:"counts"`
	CreatedAt time.Time          `json:"created_at"`
	EndedAt   time.Time          `json:"ended_at,omitzero"`
	Tasks     []PlanTask         `json:"tasks,omitempty"`
}

// PlanTask is a task of a plan as observers see it.
type PlanTask struct {
	PlanID string `json:"plan_id"`
	// Rev is the plan's revision the task is from.
	Rev           uint64     `json:"rev"`
	ID            string     `json:"id"`
	Title         string     `json:"title,omitempty"`
	Kind          string     `json:"kind"`
	State         task.State `json:"state"`
	Attempt       int        `json:"attempt,omitempty"`
	Machine       string     `json:"machine,omitempty"`
	EnvironmentID string     `json:"environment_id"`
	PaneID        string     `json:"pane_id,omitempty"`
	Needs         []string   `json:"needs,omitempty"`
	StartedAt     time.Time  `json:"started_at,omitzero"`
	EndedAt       time.Time  `json:"ended_at,omitzero"`
}

// PlanTaskView redacts a task.
func PlanTaskView(v task.TaskView) PlanTask {
	return PlanTask{
		PlanID: v.PlanID, Rev: v.Rev, ID: v.ID, Title: v.Title, Kind: v.Kind, State: v.State, Attempt: v.Attempt,
		Machine: v.Machine, EnvironmentID: v.Environment, PaneID: v.PaneID, Needs: v.Needs,
		StartedAt: v.StartedAt, EndedAt: v.EndedAt,
	}
}

// Target is where a task is: the environment it works on, its pane.
func (t PlanTask) Target() Target { return Target{Environment: t.EnvironmentID, Pane: t.PaneID} }

// PlanSummaryView redacts a plan's summary (for observers that see all).
func PlanSummaryView(s task.Summary) Plan {
	return Plan{ID: s.ID, Name: s.Name, State: s.State, Rev: s.Rev, Total: s.Total, Counts: s.Counts, CreatedAt: s.CreatedAt, EndedAt: s.EndedAt}
}

// PlanView redacts p to the tasks scope sees; ok is false when it sees
// none.
func PlanView(p task.Plan, scope Scope) (Plan, bool) {
	v := Plan{ID: p.ID, Name: p.Name, State: p.State, Rev: p.Rev, CreatedAt: p.CreatedAt, EndedAt: p.EndedAt, Counts: map[task.State]int{}, Tasks: []PlanTask{}}
	for i := range p.Tasks {
		t := PlanTaskView(p.Tasks[i].View(&p))
		if !scope.Sees(t.Target()) {
			continue
		}
		v.Tasks = append(v.Tasks, t)
		v.Counts[t.State]++
	}
	v.Total = len(v.Tasks)
	return v, v.Total > 0
}

// Metadata is what plugins and agents reported about a pane or environment:
// a title and status tokens.
type Metadata struct {
	Pane        string           `json:"pane,omitempty"`
	Environment string           `json:"environment,omitempty"`
	Title       string           `json:"title,omitempty"`
	Tokens      []metadata.Token `json:"tokens"`
}

// MetadataView redacts e.
func MetadataView(e metadata.Entry) Metadata {
	tokens := e.Tokens
	if tokens == nil {
		tokens = []metadata.Token{}
	}
	return Metadata{Pane: e.Pane, Environment: e.Environment, Title: e.Title, Tokens: tokens}
}

// Snapshot is everything an observer may see, at event Seq of stream
// Epoch: events after Seq may already be reflected in it, so a client
// applies them idempotently (by ID).
type Snapshot struct {
	Epoch        string        `json:"epoch"`
	Seq          uint64        `json:"seq"`
	Environments []Environment `json:"environments"`
	Tabs         []Tab         `json:"tabs"`
	Panes        []Pane        `json:"panes"`
	Agents       []Agent       `json:"agents"`
	Metadata     []Metadata    `json:"metadata"`
	// Plans are the plans with tasks in scope, newest first; omitted by
	// daemons before 1.3.
	Plans []Plan `json:"plans,omitempty"`
	// Floors are who holds the input of panes in scope; omitted by daemons
	// before 1.8, and when no one holds any.
	Floors []Floor `json:"floors,omitempty"`
}

// Cursor is where a screen's cursor is (0-based) and whether it shows.
type Cursor struct {
	X       int  `json:"x"`
	Y       int  `json:"y"`
	Visible bool `json:"visible"`
}

// CursorView redacts c to what observers see.
func CursorView(c vt.Cursor) Cursor { return Cursor{X: c.X, Y: c.Y, Visible: !c.Hidden} }

// Screen is a pane's screen as text.
type Screen struct {
	Pane      string   `json:"pane"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Lines     []string `json:"lines"`
	Cursor    Cursor   `json:"cursor"`
	Title     string   `json:"title,omitempty"`
	AltScreen bool     `json:"alt_screen,omitempty"`
	// Scrollback holds requested history, oldest first; it precedes Lines.
	Scrollback []string `json:"scrollback,omitempty"`
}

// ScreenUpdate is one change to a watched screen. A keyframe replaces the
// whole screen (every line is listed); otherwise only the listed lines
// changed. Lines past the listed text are blank.
type ScreenUpdate struct {
	Keyframe  bool         `json:"keyframe,omitempty"`
	Width     int          `json:"width"`
	Height    int          `json:"height"`
	Cursor    Cursor       `json:"cursor"`
	Title     string       `json:"title,omitempty"`
	AltScreen bool         `json:"alt_screen,omitempty"`
	Lines     []ScreenLine `json:"lines,omitempty"`
}

// ScreenLine is line Y's new text.
type ScreenLine struct {
	Y    int    `json:"y"`
	Text string `json:"text"`
}

// UpdateFromFrame turns a terminal frame into an update; ansi keeps SGR
// styling in the text.
func UpdateFromFrame(f *vt.Frame, ansi bool) ScreenUpdate {
	u := ScreenUpdate{
		Keyframe: f.Keyframe, Width: f.Cols, Height: f.Rows, Cursor: CursorView(f.Cursor), Title: f.Title,
		AltScreen: f.Modes&vt.ModeAltScreen != 0,
	}
	for _, l := range f.Lines {
		text := vt.LineText(l.Cells)
		if ansi {
			text = vt.LineANSI(l.Cells)
		}
		u.Lines = append(u.Lines, ScreenLine{Y: l.Y, Text: text})
	}
	return u
}
