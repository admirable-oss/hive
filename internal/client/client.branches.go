package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/branch"
)

// Branches is the typed branch API (branch.*) over any Client.
type Branches struct {
	c Client
}

// NewBranches wraps c.
func NewBranches(c Client) Branches { return Branches{c: c} }

func branchCall[R any](ctx context.Context, b Branches, method string, params any) (R, error) {
	var r R
	err := b.c.Call(ctx, method, params, &r)
	return r, err
}

// List returns the branches Hive follows: the active ones, or with all
// the cleaned and removed ones too; repo keeps one repository's.
func (b Branches) List(ctx context.Context, repo string, all bool) ([]branch.Branch, error) {
	return branchCall[[]branch.Branch](ctx, b, "branch.list", branch.ListRequest{Repo: repo, All: all})
}

// Get returns a branch by reference: its ID, environment, agent,
// plan/task or name.
func (b Branches) Get(ctx context.Context, ref string) (branch.Branch, error) {
	return branchCall[branch.Branch](ctx, b, "branch.get", branch.Ref{Ref: ref})
}

// Check reads branches again now: one branch's repository (ref), one
// repository, or every one.
func (b Branches) Check(ctx context.Context, ref, repo string) ([]branch.Branch, error) {
	return branchCall[[]branch.Branch](ctx, b, "branch.check", branch.CheckParams{Ref: ref, Repo: repo})
}

// Diff returns a branch's changes against its base, uncommitted ones
// included.
func (b Branches) Diff(ctx context.Context, req branch.DiffRequest) (branch.Diff, error) {
	return branchCall[branch.Diff](ctx, b, "branch.diff", req)
}

// Conflicts shows what merging a branch with its base (or another branch)
// would conflict on, without merging.
func (b Branches) Conflicts(ctx context.Context, ref, with string) (branch.Conflicts, error) {
	return branchCall[branch.Conflicts](ctx, b, "branch.conflicts", branch.ConflictsRequest{Ref: ref, With: with})
}

// Update merges (or rebases onto) a branch's base in its worktree, or
// aborts one stopped on a conflict.
func (b Branches) Update(ctx context.Context, req branch.UpdateRequest) (branch.UpdateResult, error) {
	return branchCall[branch.UpdateResult](ctx, b, "branch.update", req)
}

// Ready runs a branch's gates; gates, when given, replace the configured
// ones. A failing gate is not an error: the branch comes back with its
// results.
func (b Branches) Ready(ctx context.Context, ref string, gates []branch.Gate) (branch.Branch, error) {
	return branchCall[branch.Branch](ctx, b, "branch.ready", branch.ReadyRequest{Ref: ref, Gates: gates})
}

// Comment adds a review comment, and with Send sends the comments not sent
// yet to the branch's agent.
func (b Branches) Comment(ctx context.Context, req branch.CommentRequest) (branch.Branch, error) {
	return branchCall[branch.Branch](ctx, b, "branch.comment", req)
}

// Send sends a branch's comments not sent yet to its agent.
func (b Branches) Send(ctx context.Context, ref string) (branch.Branch, error) {
	return branchCall[branch.Branch](ctx, b, "branch.send", branch.Ref{Ref: ref})
}

// Push pushes a branch to a remote (its upstream's, else origin).
func (b Branches) Push(ctx context.Context, ref, remote string) (branch.Branch, error) {
	return branchCall[branch.Branch](ctx, b, "branch.push", branch.PushRequest{Ref: ref, Remote: remote})
}

// PullRequest opens a pull request for a branch.
func (b Branches) PullRequest(ctx context.Context, req branch.PullRequestRequest) (branch.Branch, error) {
	return branchCall[branch.Branch](ctx, b, "branch.pr", req)
}

// Clean removes branches' worktrees: one, or every merged one.
func (b Branches) Clean(ctx context.Context, req branch.CleanRequest) ([]branch.Branch, error) {
	return branchCall[[]branch.Branch](ctx, b, "branch.clean", req)
}
