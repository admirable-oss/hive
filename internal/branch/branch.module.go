// Package branch follows the branches agents work on, from the worktree
// Hive makes for one to its cleanup after it merges.
//
// Every worktree Hive creates (for an agent, a task, or on request) is
// tracked as a Branch: its base, its state (created, working, ready,
// merged, cleaned or removed), the files it changes, and where it overlaps
// other open branches of the repository, down to the files a merge would
// conflict on. Uncommitted work counts: the service compares snapshots of
// worktrees as they are, made without touching them.
//
// Review happens here too: a branch's diff, comments that go back to its
// agent as a prompt, and gates (tests, lint, build) that must pass in its
// worktree before it is ready. Publishing is by consent only: a push or a
// pull request happens on a person's explicit request, through a Forge.
// Hive never commits, merges, resolves a conflict or pushes by itself; the
// one write it makes unasked is removing the worktree of a branch that has
// merged, and only when no uncommitted work or running agent is in it.
package branch

import "path/filepath"

// ModuleConfig wires the package.
type ModuleConfig struct {
	// BaseDir is the Hive root: branches are kept in
	// <BaseDir>/branches.json.
	BaseDir string
	Service Config
}

// Module is the package's wiring.
type Module struct {
	Service *Service
}

// NewModule builds the service over a filesystem store.
func NewModule(cfg ModuleConfig, ports Ports) *Module {
	return &Module{Service: NewService(cfg.Service, NewFilesystemStore(filepath.Clean(cfg.BaseDir)), ports)}
}
