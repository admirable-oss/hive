package environment

import (
	"time"

	"github.com/admirable-oss/hive/internal/git"
)

type Status string

const StatusReady Status = "ready"

// schema is the current version of environment.json. Version 0 (no field)
// predates rooted environments: every one had a Hive-managed workspace.
const schema = 1

type Environment struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	// Path is where the environment's agents run: the directory it was
	// created for, or a workspace Hive made for it.
	Path string `json:"path"`
	// Managed is set when Hive created Path; it is deleted with the
	// environment. A user's own directory never is.
	Managed bool `json:"managed"`
	// Env is added to the environment of every process started in it.
	Env map[string]string `json:"env,omitempty"`
	// Worktree is set when Hive created Path as a git worktree.
	Worktree  *Worktree `json:"worktree,omitempty"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	// Git is the repository state of Path, kept current by the daemon. It
	// is not stored.
	Git *git.Status `json:"git,omitempty"`
}

// Worktree records the repository and branch of a worktree environment.
type Worktree struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

// CreateRequest describes a new environment.
type CreateRequest struct {
	ID string `json:"id"`
	// Root is an existing directory to run agents in (absolute). Empty
	// makes a managed workspace.
	Root     string            `json:"root,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Worktree *Worktree         `json:"worktree,omitempty"`
}

// UpdateRequest changes an environment. Set adds or replaces variables;
// Unset removes them.
type UpdateRequest struct {
	ID    string            `json:"id"`
	Set   map[string]string `json:"set,omitempty"`
	Unset []string          `json:"unset,omitempty"`
}
