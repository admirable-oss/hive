package persist

import "time"

// Wire types of session.save, session.restore and runtime.gc.

// SaveResult is what session.save did: Written is false when the
// workspace had not changed since the last snapshot.
type SaveResult struct {
	Path    string `json:"path"`
	Written bool   `json:"written"`
}

// RestoreRequest asks session.restore to start lost panes again.
type RestoreRequest struct {
	// ResumeAgents overrides session.resume_agents_on_restore.
	ResumeAgents *bool `json:"resume_agents,omitempty"`
}

// RestoreReport is what a restore did; it is also the data of the
// session.restored event.
type RestoreReport struct {
	Panes []RestoredPane `json:"panes"`
}

// RestoredPane is one pane a restore started again.
type RestoredPane struct {
	Pane        string   `json:"pane"`
	Environment string   `json:"environment"`
	ProcessID   string   `json:"process_id,omitempty"`
	Command     []string `json:"command"`
	Cwd         string   `json:"cwd,omitempty"`
	// Agent is the kind of agent the pane held; Resumed says how its
	// conversation was resumed: "session" (by its ID), "last" (the latest
	// one in its directory) or "" (started fresh, or not an agent).
	Agent   string `json:"agent,omitempty"`
	Resumed string `json:"resumed,omitempty"`
	// Typed is the command typed into the pane's shell (for an agent that
	// ran under it).
	Typed string `json:"typed,omitempty"`
	Error string `json:"error,omitempty"`
}

// GCRequest asks runtime.gc to clean up.
type GCRequest struct {
	// DryRun lists what would be removed without removing it.
	DryRun bool `json:"dry_run,omitempty"`
	// OlderThan overrides retention.exited_processes.
	OlderThan time.Duration `json:"older_than,omitempty"`
}

// GCReport is what runtime.gc removed (or would remove).
type GCReport struct {
	DryRun bool `json:"dry_run"`
	// Processes are the IDs of ended processes whose records and output
	// were removed.
	Processes []string `json:"processes"`
	// Files are other files and directories removed: shims' leftovers,
	// kept pane history, unreadable files moved aside.
	Files []string `json:"files"`
	// Bytes is the space freed.
	Bytes int64 `json:"bytes"`
}
