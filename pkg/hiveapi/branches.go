package hiveapi

import (
	"context"

	"github.com/admirable-oss/hive/internal/branch"
)

// Branches: the branch of every worktree Hive makes for an agent or a
// task, from its creation to its cleanup (`hive branch`), with the files
// it overlaps other branches on, review and publishing on request.
//
//	started, err := c.StartAgent(ctx, hiveapi.StartAgent{Kind: "claude", EnvironmentID: "api", Isolate: true})
//	d, err := c.BranchDiff(ctx, hiveapi.DiffRequest{Ref: started.Agent.ID})
//	b, err := c.CommentBranch(ctx, hiveapi.CommentRequest{Ref: started.Agent.ID, Body: "Add a test.", Send: true})
//	b, err = c.ReadyBranch(ctx, started.Agent.ID, nil)
type (
	Branch             = branch.Branch
	BranchState        = branch.State
	BranchOwner        = branch.Owner
	Overlap            = branch.Overlap
	Gate               = branch.Gate
	GateResult         = branch.GateResult
	Comment            = branch.Comment
	PullRequest        = branch.PullRequest
	Diff               = branch.Diff
	DiffRequest        = branch.DiffRequest
	Conflicts          = branch.Conflicts
	UpdateRequest      = branch.UpdateRequest
	UpdateResult       = branch.UpdateResult
	CommentRequest     = branch.CommentRequest
	PullRequestRequest = branch.PullRequestRequest
	CleanRequest       = branch.CleanRequest
	OverlapEvent       = branch.OverlapEvent
)

// Branch states.
const (
	BranchCreated = branch.StateCreated
	BranchWorking = branch.StateWorking
	BranchReady   = branch.StateReady
	BranchMerged  = branch.StateMerged
	BranchCleaned = branch.StateCleaned
	BranchRemoved = branch.StateRemoved
)

// Branch events (see Events).
const (
	EventBranchUpdated = branch.EventUpdated
	EventBranchOverlap = branch.EventOverlap
	EventBranchRemoved = branch.EventRemoved
)

// Branches lists the branches Hive follows: active ones, or with all the
// cleaned and removed ones too; repo keeps one repository's.
func (c *Client) Branches(ctx context.Context, repo string, all bool) ([]Branch, error) {
	return c.brs.List(ctx, repo, all)
}

// Branch returns a branch by its ID, environment, agent, plan/task or name.
func (c *Client) Branch(ctx context.Context, ref string) (Branch, error) { return c.brs.Get(ctx, ref) }

// CheckBranches reads a branch's repository (or repo, or every one) again
// now, and returns its branches.
func (c *Client) CheckBranches(ctx context.Context, ref, repo string) ([]Branch, error) {
	return c.brs.Check(ctx, ref, repo)
}

// BranchDiff returns a branch's changes against its base, uncommitted
// ones included.
func (c *Client) BranchDiff(ctx context.Context, req DiffRequest) (Diff, error) {
	return c.brs.Diff(ctx, req)
}

// BranchConflicts shows what merging a branch with its base (or with
// another branch) would conflict on, without merging.
func (c *Client) BranchConflicts(ctx context.Context, ref, with string) (Conflicts, error) {
	return c.brs.Conflicts(ctx, ref, with)
}

// UpdateBranch merges (or rebases onto) a branch's base in its worktree,
// stopping on conflicts for a person, or aborts one so stopped.
func (c *Client) UpdateBranch(ctx context.Context, req UpdateRequest) (UpdateResult, error) {
	return c.brs.Update(ctx, req)
}

// ReadyBranch runs a branch's gates (gates replace the configured ones);
// the branch is ready when they pass. A failing gate is not an error.
func (c *Client) ReadyBranch(ctx context.Context, ref string, gates []Gate) (Branch, error) {
	return c.brs.Ready(ctx, ref, gates)
}

// CommentBranch adds a review comment; with Send, the comments not sent
// yet go to the branch's agent as one prompt.
func (c *Client) CommentBranch(ctx context.Context, req CommentRequest) (Branch, error) {
	return c.brs.Comment(ctx, req)
}

// SendComments sends a branch's comments not sent yet to its agent.
func (c *Client) SendComments(ctx context.Context, ref string) (Branch, error) {
	return c.brs.Send(ctx, ref)
}

// PushBranch pushes a branch to a remote (its upstream's, else origin),
// never forced.
func (c *Client) PushBranch(ctx context.Context, ref, remote string) (Branch, error) {
	return c.brs.Push(ctx, ref, remote)
}

// OpenPullRequest opens a pull request for a branch.
func (c *Client) OpenPullRequest(ctx context.Context, req PullRequestRequest) (Branch, error) {
	return c.brs.PullRequest(ctx, req)
}

// CleanBranches removes a branch's worktree, or every merged one's.
func (c *Client) CleanBranches(ctx context.Context, req CleanRequest) ([]Branch, error) {
	return c.brs.Clean(ctx, req)
}
