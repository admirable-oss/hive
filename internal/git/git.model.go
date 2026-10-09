package git

import "errors"

// Status is a working tree's state.
type Status struct {
	// Root is the top of the working tree.
	Root string `json:"root"`
	// Branch is the checked-out branch; empty when Detached.
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"` // abbreviated commit, empty before the first commit
	Detached bool   `json:"detached,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
	// Staged, Modified, Untracked and Conflicts count paths in each state.
	Staged    int `json:"staged,omitempty"`
	Modified  int `json:"modified,omitempty"`
	Untracked int `json:"untracked,omitempty"`
	Conflicts int `json:"conflicts,omitempty"`
}

// Dirty reports whether the working tree has any change.
func (s *Status) Dirty() bool {
	return s != nil && s.Staged+s.Modified+s.Untracked+s.Conflicts > 0
}

// Worktree is one checkout of a repository.
type Worktree struct {
	Path     string `json:"path"`
	Head     string `json:"head,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Bare     bool   `json:"bare,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
	// Main is the repository's main working tree.
	Main bool `json:"main,omitempty"`
}

var (
	// ErrNotRepository means the directory is not inside a git working tree.
	ErrNotRepository = errors.New("not a git repository")
	// ErrDirty means git refused because the worktree has changes.
	ErrDirty = errors.New("the worktree has uncommitted changes")
)
