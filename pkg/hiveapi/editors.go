package hiveapi

import (
	"context"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/edits"
	"github.com/admirable-oss/hive/internal/git"
)

// Review and edits, for editors (M31): hunks of a branch's diff accepted
// or rejected one by one, and the files agents are changing.
//
//	d, err := c.BranchDiff(ctx, hiveapi.DiffRequest{Ref: agentID})
//	for _, f := range hiveapi.ParsePatch(d.Patch) {
//		res, err := c.ReviewHunk(ctx, hiveapi.HunkRequest{Ref: agentID, Path: f.Name(), Hunk: f.Hunks[0].ID, Action: hiveapi.HunkReject})
//	}
//	files, err := c.AgentFiles(ctx, hiveapi.AgentFilesRequest{Dir: "/home/me/src/shop"})
type (
	// FilePatch is one file of a unified diff, with its hunks.
	FilePatch = git.FilePatch
	// Hunk is one change in a file; its ID names its lines.
	Hunk        = git.Hunk
	HunkRequest = branch.HunkRequest
	HunkResult  = branch.HunkResult
	HunkReview  = branch.HunkReview
	Verdict     = branch.Verdict
	// AgentEdit is a file an agent is changing.
	AgentEdit         = edits.Edit
	AgentFiles        = edits.Files
	AgentFilesRequest = edits.ListRequest
)

// Hunk actions and verdicts.
const (
	HunkAccept      = branch.HunkAccept
	HunkReject      = branch.HunkReject
	VerdictAccepted = branch.VerdictAccepted
	VerdictRejected = branch.VerdictRejected
)

// EventAgentFiles says the files agents are changing in one working tree
// changed; its data is an AgentFiles.
const EventAgentFiles = edits.EventFiles

// ParsePatch splits a unified diff (Diff.Patch) into files and hunks, with
// the hunk IDs ReviewHunk takes.
func ParsePatch(patch string) []FilePatch { return git.ParsePatch(patch) }

// ReviewHunk accepts a hunk of a branch's diff (recording it) or rejects it
// (taking it out of the branch's worktree through git). Rejecting refuses
// a hunk that changed since the diff was read, and refuses while an agent
// works there unless Force is set. It needs API level 19.
func (c *Client) ReviewHunk(ctx context.Context, req HunkRequest) (HunkResult, error) {
	var res HunkResult
	err := c.Call(ctx, "branch.hunk", req, &res)
	return res, err
}

// AgentFiles lists the files agents are changing: all of them, one
// environment's working tree's, or those under a directory. It needs API
// level 19.
func (c *Client) AgentFiles(ctx context.Context, req AgentFilesRequest) (AgentFiles, error) {
	var res AgentFiles
	err := c.Call(ctx, "agent.files", req, &res)
	return res, err
}
