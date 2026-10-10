// Package agent reads what coding agents are doing from their terminals.
//
// Every terminal the daemon runs is watched: a manifest recognises which
// agent runs in it (Claude Code, Codex, …) from its process, and the
// manifest's rules read its state from its screen. Agents may also report
// their state themselves (hooks call `hive pane report-agent`); a report
// wins until it expires. Without either, screen activity decides.
//
// States move unknown → working ⇄ blocked → done → idle: done is an
// agent that finished working and has not been looked at since; looking
// at it (focusing its pane) or prompting it makes it idle.
package agent

import "time"

// State is what an agent is doing.
type State string

const (
	StateUnknown State = "unknown" // no evidence yet
	StateWorking State = "working" // busy on a task
	StateBlocked State = "blocked" // waiting for the user's decision (a permission prompt)
	StateDone    State = "done"    // finished, and not looked at since
	StateIdle    State = "idle"    // waiting for a prompt
	StateExited  State = "exited"  // its process ended
)

// States lists the states in rollup priority order: when several agents
// roll up into one tab, environment or session, the first state any of
// them has is the summary.
var States = []State{StateBlocked, StateWorking, StateDone, StateIdle, StateUnknown, StateExited}

// detectable reports whether a manifest rule may produce s.
func (s State) detectable() bool {
	switch s {
	case StateWorking, StateBlocked, StateIdle, StateDone:
		return true
	}
	return false
}

// reportable reports whether an agent may report s about itself.
func (s State) reportable() bool { return s.detectable() }

// Rank orders states for rollups: lower is more urgent.
func (s State) Rank() int {
	for i, x := range States {
		if x == s {
			return i
		}
	}
	return len(States)
}

// Rollup summarises states: the most urgent one (blocked > working > done
// > idle > unknown > exited); "" for none.
func Rollup(states ...State) State {
	best := State("")
	for _, s := range states {
		if s == "" {
			continue
		}
		if best == "" || s.Rank() < best.Rank() {
			best = s
		}
	}
	return best
}

// Source says what decided a state.
type Source string

const (
	SourceReport   Source = "report"   // the agent said so (a hook)
	SourceManifest Source = "manifest" // a manifest rule matched the screen
	SourceActivity Source = "activity" // the screen changed, or stopped changing
	SourceProcess  Source = "process"  // the process started or exited
)

// Status is an agent's detected state.
type Status struct {
	State  State  `json:"state"`
	Source Source `json:"source,omitempty"`
	// Since is when the state began.
	Since time.Time `json:"since"`
	// StateSeq counts state changes; CompletionSeq counts entries into
	// done. A waiter compares them with values it saw earlier.
	StateSeq      uint64 `json:"state_seq"`
	CompletionSeq uint64 `json:"completion_seq"`
	// LastOutput is when the screen last changed.
	LastOutput time.Time `json:"last_output,omitzero"`
	// SessionID is the agent's own session ID, when it reported one (for
	// resuming it later).
	SessionID string `json:"session_id,omitempty"`
	// Reason says why, in a few words: the rule that matched, the report.
	Reason string `json:"reason,omitempty"`
}

// Agent is one agent: a terminal process a manifest recognises.
type Agent struct {
	// ID is the agent's process ID.
	ID   string `json:"id"`
	Kind string `json:"kind"` // the manifest ID: claude, codex, …
	// Name is its pane's name, else the manifest's name.
	Name          string `json:"name"`
	EnvironmentID string `json:"environment_id"`
	TabID         string `json:"tab_id,omitempty"`
	PaneID        string `json:"pane_id,omitempty"`
	PID           int    `json:"pid,omitempty"`
	Status
}

// Report is what an agent says about itself.
type Report struct {
	State State `json:"state"`
	// TTL is how long the report holds without another one (default
	// DefaultReportTTL).
	TTL       time.Duration `json:"ttl,omitempty"`
	SessionID string        `json:"session_id,omitempty"`
	Message   string        `json:"message,omitempty"`
}

// DefaultReportTTL is how long a report holds when it does not say.
const DefaultReportTTL = 10 * time.Minute
