package branch

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/admirable-oss/hive/internal/git"
)

// CleanRequest removes branches' worktrees, on a person's command.
type CleanRequest struct {
	// Ref names one branch; Merged instead cleans every merged one.
	Ref    string `json:"ref,omitempty" desc:"the branch"`
	Merged bool   `json:"merged,omitempty" desc:"every merged branch, instead of one"`
	// Force removes a worktree with uncommitted changes. They are saved
	// first, under refs/hive/saved/, so nothing is lost.
	Force bool `json:"force,omitempty" desc:"remove it even with uncommitted changes (they are saved under refs/hive/saved/ first)"`
	// DeleteBranch deletes the local branch too, when git agrees it has
	// merged (as [branches] delete_merged does).
	DeleteBranch bool `json:"delete_branch,omitempty" desc:"also delete the local branch once git agrees it has merged"`
}

// Clean removes a branch's worktree and its environments (stopping their
// agents). An unmerged branch keeps its commits on the branch, which git
// keeps; uncommitted work is removed only with Force, and is saved under
// refs/hive/saved/ first.
func (s *Service) Clean(ctx context.Context, req CleanRequest) ([]Branch, error) {
	var ids []string
	if req.Merged {
		for _, b := range s.List(ListRequest{}) {
			if b.State == StateMerged {
				ids = append(ids, b.ID)
			}
		}
	} else {
		b, err := s.Get(ctx, req.Ref)
		if err != nil {
			return nil, err
		}
		if !b.State.Active() {
			return nil, fmt.Errorf("%w: branch %s is already %s", ErrState, b.Name, b.State)
		}
		ids = []string{b.ID}
	}
	out := []Branch{}
	var errs []error
	for _, id := range ids {
		b, err := s.clean(ctx, id, req.Force, req.DeleteBranch || s.cfg.DeleteMerged)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, b)
	}
	return out, errors.Join(errs...)
}

// autoClean applies the cleanup policy to a merged branch: its worktree
// goes when nothing uncommitted and no running process is in it, and the
// branch says why otherwise.
func (s *Service) autoClean(ctx context.Context, id string) {
	b, err := s.Get(ctx, id)
	if err != nil || b.State != StateMerged {
		return
	}
	reason := ""
	switch {
	case b.Dirty:
		reason = "it has uncommitted changes (hive branch clean --force saves and removes them)"
	case b.InProgress:
		reason = "a merge or rebase is stopped in it"
	case s.ports.Workspace == nil:
		reason = "worktrees cannot be removed here"
	default:
		if busy, err := s.ports.Workspace.Busy(ctx, b.Path); err != nil {
			reason = err.Error()
		} else if busy {
			reason = "processes still run in it; it is removed once they stop"
		}
	}
	if reason == "" {
		if _, err := s.clean(ctx, id, false, s.cfg.DeleteMerged); err != nil {
			reason = err.Error()
		}
	}
	if reason == "" {
		return
	}
	s.mu.Lock()
	if live := s.branches[id]; live != nil && live.State == StateMerged && live.Cleanup != reason {
		live.Cleanup = reason
		s.commitLocked(live)
	}
	s.mu.Unlock()
	s.persist()
}

// clean removes one branch's worktree.
func (s *Service) clean(ctx context.Context, id string, force, deleteBranch bool) (Branch, error) {
	b, err := s.Get(ctx, id)
	if err != nil {
		return Branch{}, err
	}
	if s.ports.Workspace == nil {
		return Branch{}, fmt.Errorf("%w: worktrees cannot be removed here", ErrState)
	}
	unlock := s.lockRepo(b.Repo)
	defer unlock()
	snap, err := s.git.Snapshot(ctx, b.Path)
	if err != nil {
		return Branch{}, err
	}
	saved := ""
	if snap.Dirty {
		if !force {
			return Branch{}, fmt.Errorf("%w: %s has uncommitted changes; --force saves them under refs/hive/saved/ and removes it", ErrState, b.Path)
		}
		saved = "refs/hive/saved/" + git.Slug(b.Name) + "-" + strconv.FormatInt(s.cfg.Now().Unix(), 10)
		if err := s.git.SaveRef(ctx, b.Repo, saved, snap.Commit); err != nil {
			return Branch{}, fmt.Errorf("saving the uncommitted work first: %w", err)
		}
	}
	if err := s.ports.Workspace.RemoveWorktree(ctx, b.Path, force); err != nil {
		return Branch{}, err
	}
	merged := b.State == StateMerged
	deleted := false
	if merged && deleteBranch && b.Ahead == 0 {
		// Only a branch whose head is in its base: git's own check agrees.
		if err := s.git.DeleteBranch(ctx, b.Repo, b.Name, false); err != nil {
			s.log.Info("merged branch kept", "branch", b.Name, "err", err)
		} else {
			deleted = true
		}
	}
	s.mu.Lock()
	if live := s.branches[id]; live != nil {
		if live.State.Active() {
			s.closeLocked(live, s.cfg.Now())
		}
		if saved != "" {
			live.Saved = saved
			s.commitLocked(live)
		}
	}
	s.forgetLocked()
	s.mu.Unlock()
	s.persist()
	s.log.Info("branch cleaned up", "branch", b.Name, "merged", merged, "branch_deleted", deleted, "saved", saved)
	return s.Get(ctx, id)
}
