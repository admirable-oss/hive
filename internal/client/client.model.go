package client

import "time"

// Status is the daemon state reported by runtime.status.
type Status struct {
	Status          string    `json:"status"`
	Socket          string    `json:"socket"`
	StartedAt       time.Time `json:"started_at"`
	PID             int       `json:"pid"`
	Version         string    `json:"version"`
	ProtocolVersion string    `json:"protocol_version"`
	// AgentsSurviveRestart is true when the daemon runs agents under shims.
	AgentsSurviveRestart bool `json:"agents_survive_restart"`
}

// Logs is the reply of process.logs.
type Logs struct {
	Logs string `json:"logs"`
	// Truncated means the daemon cut the reply to its size limit; stream the
	// log to read all of it.
	Truncated bool `json:"truncated,omitempty"`
}

// ViewRequest opens a view of an agent's terminal.
type ViewRequest struct {
	ProcessID string `json:"process_id"`
	// Width and Height are the client's size for this view.
	Width  uint16 `json:"width,omitempty"`
	Height uint16 `json:"height,omitempty"`
}

// View describes an open view.
type View struct {
	ID string `json:"view_id"`
	// Width and Height are the terminal's size when the view opened.
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
}

// Snapshot is an agent's screen as text.
type Snapshot struct {
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Lines     []string `json:"lines"`
	Cursor    Cursor   `json:"cursor"`
	Title     string   `json:"title,omitempty"`
	AltScreen bool     `json:"alt_screen,omitempty"`
	// Scrollback is the requested history, oldest first; it precedes Lines.
	Scrollback []string `json:"scrollback,omitempty"`
}

// SnapshotRequest selects what TerminalSnapshot returns.
type SnapshotRequest struct {
	ProcessID string `json:"process_id"`
	// ANSI keeps colours and styles as SGR sequences.
	ANSI bool `json:"ansi,omitempty"`
	// Scrollback adds up to this many lines of history.
	Scrollback int `json:"scrollback,omitempty"`
}

// Cursor is the cursor in a Snapshot.
type Cursor struct {
	X      int  `json:"X"`
	Y      int  `json:"Y"`
	Hidden bool `json:"Hidden"`
}
