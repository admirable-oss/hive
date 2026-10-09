// Package git reads repository state for environments (branch, ahead and
// behind, dirty files) and manages the worktrees Hive creates for agents.
// It drives the git CLI rather than reimplementing git, so it always agrees
// with what the user sees in their shell.
package git
