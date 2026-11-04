// Package edits says which files agents are changing, so that editors
// can mark them and warn a person before they edit one too (M31).
//
// While an agent works, the service reads its working tree's changed
// files (those that differ from HEAD, and untracked ones) about once a
// second, with each one's size and modification time. A file that
// appears, changes or disappears while agents work in that tree is
// attributed to them. An attribution lasts while the file differs from
// HEAD and the agent runs. Nothing is read while no agent works, and
// nothing is stored: a restarted daemon starts again.
//
// A person editing a file in the same tree while an agent works there is
// counted as the agent: the file alone cannot tell two writers apart, and
// the warning errs on the side of caution.
package edits

import "time"

// EventFiles is published with a Files whenever the files agents are
// changing in one working tree change.
const EventFiles = "agent.files"

// Edit is one file one agent is changing.
type Edit struct {
	// Path is the file, absolute; Worktree the working tree it is in, and
	// Rel its path there, with forward slashes.
	Path     string `json:"path" desc:"the file (absolute)"`
	Worktree string `json:"worktree" desc:"the working tree it is in"`
	Rel      string `json:"rel" desc:"its path in the working tree, with forward slashes"`
	// Deleted says the agent removed it.
	Deleted bool `json:"deleted,omitempty" desc:"the agent deleted it"`

	Agent         string `json:"agent" desc:"the agent's ID"`
	Kind          string `json:"kind" desc:"the agent's kind"`
	Name          string `json:"name,omitempty" desc:"the agent's name"`
	PaneID        string `json:"pane_id,omitempty" desc:"the agent's pane"`
	EnvironmentID string `json:"environment_id" desc:"the agent's environment"`
	// State is the agent's state now: working while it may still change
	// the file.
	State string `json:"state" desc:"the agent's state now"`

	Since time.Time `json:"since" desc:"when the agent first changed it"`
	Last  time.Time `json:"last" desc:"when it last changed it"`
}

// Files is the files agents are changing, in one working tree (in an
// event) or wherever a request asked.
type Files struct {
	Worktree string `json:"worktree,omitempty" desc:"the working tree (in an agent.files event)"`
	Files    []Edit `json:"files" desc:"files agents are changing, by path, then agent"`
}

// ListRequest is agent.files's request: every file, or those of one
// environment's working tree, or those under a directory.
type ListRequest struct {
	EnvironmentID string `json:"environment_id,omitempty" desc:"only the working tree of this environment"`
	Dir           string `json:"dir,omitempty" desc:"only files under this directory (absolute)"`
}

// Bounds.
const (
	// MaxScanned bounds the changed files read in one working tree; a tree
	// with more is read in part.
	MaxScanned = 5000
	// DefaultInterval is how often working trees are read while agents
	// work.
	DefaultInterval = time.Second
)
