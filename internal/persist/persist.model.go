// Package persist keeps snapshots of a session's workspace, so a host
// reboot does not lose it: every environment's tabs, layout and focus, and
// for each pane what it ran, where it was working and which agent
// conversation it held.
//
// The live state is kept elsewhere (environment.json, layout.json and the
// process records), written on every change. A snapshot adds what only a
// running system knows (a shell's current directory, an agent's session
// ID) and keeps history: session.json is the latest, and the previous ones
// are rotated into snapshots/, keeping the last few. A file that cannot be
// read is moved aside as a backup, and the newest readable one is used.
package persist

import (
	"time"

	"github.com/admirable-oss/hive/internal/pane"
)

// Schema is the snapshot format this build writes. A snapshot with a later
// schema was written by a newer Hive; it is left alone and an older one is
// used instead.
const Schema = 1

// DefaultKeep is how many rotated snapshots are kept.
const DefaultKeep = 48

// Snapshot is a session's workspace at one moment.
type Snapshot struct {
	Schema  int       `json:"schema"`
	SavedAt time.Time `json:"saved_at"`
	// Version is the Hive build that wrote it.
	Version string `json:"version,omitempty"`
	// BootTime is when the host had booted, so a reader can tell that it
	// rebooted since.
	BootTime     time.Time     `json:"boot_time,omitzero"`
	Environments []Environment `json:"environments"`
}

// Environment is one environment's tabs and panes.
type Environment struct {
	ID        string     `json:"id"`
	Path      string     `json:"path"`
	ActiveTab string     `json:"active_tab,omitempty"`
	Tabs      []pane.Tab `json:"tabs"`
	Panes     []Pane     `json:"panes"`
}

// Pane is what a pane ran and where.
type Pane struct {
	ID        string `json:"id"`
	TabID     string `json:"tab_id"`
	Name      string `json:"name,omitempty"`
	ProcessID string `json:"process_id"`
	// Command is the pane's program and arguments.
	Command []string `json:"command"`
	// Cwd is where the program was working (a shell's directory after
	// `cd`), or where it started when that is unknown.
	Cwd string `json:"cwd,omitempty"`
	// Env holds the variables the pane was started with on top of its
	// environment's (not Hive's own).
	Env     map[string]string `json:"env,omitempty"`
	Running bool              `json:"running"`
	Agent   *Agent            `json:"agent,omitempty"`
}

// Agent is the coding agent a pane showed.
type Agent struct {
	Kind string `json:"kind"`
	// SessionID is the agent's conversation, as its hooks reported it.
	SessionID string `json:"session_id,omitempty"`
	// Args is the agent's command line. InShell says it ran under the
	// pane's shell (typed there) rather than as the pane's program.
	Args    []string `json:"args,omitempty"`
	InShell bool     `json:"in_shell,omitempty"`
}

// Pane returns the pane with id, or nil.
func (s *Snapshot) Pane(id string) *Pane {
	for i := range s.Environments {
		for j := range s.Environments[i].Panes {
			if p := &s.Environments[i].Panes[j]; p.ID == id {
				return p
			}
		}
	}
	return nil
}

// Environment returns the environment with id, or nil.
func (s *Snapshot) Environment(id string) *Environment {
	for i := range s.Environments {
		if s.Environments[i].ID == id {
			return &s.Environments[i]
		}
	}
	return nil
}
