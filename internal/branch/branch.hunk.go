package branch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/admirable-oss/hive/internal/git"
)

// Hunk actions.
const (
	HunkAccept = "accept"
	HunkReject = "reject"
)

// HunkRequest accepts or rejects one hunk of a branch's diff.
type HunkRequest struct {
	Ref  string `json:"ref" desc:"the branch: its ID, environment, agent, plan/task or name"`
	Path string `json:"path" desc:"the file, relative to the repository (its path in the branch)"`
	// Hunk is the hunk's ID as git.ParsePatch gives it for the branch's
	// diff, read with the same context.
	Hunk   string `json:"hunk" desc:"the hunk's ID, from the branch's diff"`
	Action string `json:"action" desc:"accept (record the verdict) or reject (take the hunk out of the worktree)"`
	// Context is the lines of context the diff was read with (default 3),
	// which decide where hunks begin and end.
	Context *int `json:"context,omitempty" desc:"the lines of context the diff was read with (default 3)"`
	// Force rejects even while an agent works in the worktree.
	Force bool   `json:"force,omitempty" desc:"reject even while an agent works or waits in the worktree"`
	By    string `json:"by,omitempty" desc:"who decided"`
}

// HunkResult is the branch after a verdict, and the verdict.
type HunkResult struct {
	Branch Branch     `json:"branch"`
	Review HunkReview `json:"review"`
}

// Hunk records a person's verdict on one hunk of a branch's diff. Accepting
// changes nothing but the record. Rejecting takes the hunk out of the
// branch's worktree, through git, and refuses when the hunk is no longer
// what the person saw (its ID names its lines) or while an agent works
// there, unless forced. The person's own branch is never touched.
func (s *Service) Hunk(ctx context.Context, req HunkRequest) (HunkResult, error) {
	switch {
	case req.Action != HunkAccept && req.Action != HunkReject:
		return HunkResult{}, fmt.Errorf("%w: action %q (accept or reject)", ErrInvalid, req.Action)
	case req.Path == "" || !repoRelative(req.Path) || strings.ContainsRune(req.Path, 0):
		return HunkResult{}, fmt.Errorf("%w: path %q (paths are relative to the repository)", ErrInvalid, req.Path)
	case req.Hunk == "":
		return HunkResult{}, fmt.Errorf("%w: name the hunk by its ID", ErrInvalid)
	case req.Context != nil && *req.Context < 1:
		return HunkResult{}, fmt.Errorf("%w: hunks need at least one line of context", ErrInvalid)
	}
	b, err := s.Get(ctx, req.Ref)
	if err != nil {
		return HunkResult{}, err
	}
	if !b.State.Active() {
		return HunkResult{}, fmt.Errorf("%w: branch %s is %s: its worktree is gone", ErrState, b.Name, b.State)
	}
	unlock := s.lockRepo(b.Repo)
	defer unlock()
	file, hunk, err := s.findHunk(ctx, b, req)
	if err != nil {
		return HunkResult{}, err
	}
	verdict := VerdictAccepted
	if req.Action == HunkReject {
		verdict = VerdictRejected
		if s.git.InProgress(ctx, b.Path) {
			return HunkResult{}, fmt.Errorf("%w: a merge or rebase is stopped in %s; resolve it, or abort it", ErrState, b.Path)
		}
		if !req.Force {
			if err := s.agentsIdle(ctx, b); err != nil {
				return HunkResult{}, err
			}
		}
		if err := s.git.Apply(ctx, b.Path, file.Single(hunk), true); err != nil {
			var ce *git.CommandError
			if errors.As(err, &ce) {
				return HunkResult{}, fmt.Errorf("%w: %s changed since its diff was read; read it again (%s)", ErrState, req.Path, strings.TrimSpace(ce.Stderr))
			}
			return HunkResult{}, err
		}
	}
	review := HunkReview{Path: req.Path, Hunk: req.Hunk, Verdict: verdict, By: req.By, At: s.cfg.Now()}
	s.mu.Lock()
	if live := s.branches[b.ID]; live != nil {
		live.Reviews = slices.DeleteFunc(live.Reviews, func(r HunkReview) bool { return r.Path == review.Path && r.Hunk == review.Hunk })
		live.Reviews = append(live.Reviews, review)
		if n := len(live.Reviews) - MaxReviews; n > 0 {
			live.Reviews = slices.Delete(live.Reviews, 0, n)
		}
		s.commitLocked(live)
	}
	s.mu.Unlock()
	s.persist()
	if verdict == VerdictRejected {
		s.refreshLocked(ctx, b.Repo)
	}
	b, err = s.Get(ctx, b.ID)
	return HunkResult{Branch: b, Review: review}, err
}

// findHunk reads the branch's diff as the person read it, and finds the
// hunk in the file.
func (s *Service) findHunk(ctx context.Context, b Branch, req HunkRequest) (git.FilePatch, git.Hunk, error) {
	find := func(paths ...string) (git.FilePatch, bool, error) {
		d, err := s.Diff(ctx, DiffRequest{Ref: b.ID, Paths: paths, Context: req.Context, Limit: MaxPatchBytes})
		if err != nil {
			return git.FilePatch{}, false, err
		}
		for _, f := range git.ParsePatch(d.Patch) {
			if f.Name() == req.Path {
				return f, true, nil
			}
		}
		return git.FilePatch{}, d.Truncated, nil
	}
	// The whole diff first, so a renamed file reads as it did in it; a
	// diff too long to hold the file is read again for the file alone.
	file, more, err := find()
	if err == nil && file.Name() == "" && more {
		file, _, err = find(req.Path)
	}
	switch {
	case err != nil:
		return git.FilePatch{}, git.Hunk{}, err
	case file.Name() == "":
		return git.FilePatch{}, git.Hunk{}, fmt.Errorf("%w: branch %s does not change %s any more", ErrState, b.Name, req.Path)
	case file.Binary:
		return git.FilePatch{}, git.Hunk{}, fmt.Errorf("%w: %s is a binary file, which has no hunks", ErrInvalid, req.Path)
	}
	hunk, ok := file.FindHunk(req.Hunk)
	if !ok {
		return git.FilePatch{}, git.Hunk{}, fmt.Errorf("%w: that hunk of %s is not in the branch's diff any more; read the diff again", ErrState, req.Path)
	}
	return file, hunk, nil
}
