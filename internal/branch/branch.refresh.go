package branch

import (
	"context"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/admirable-oss/hive/internal/git"
)

// observation is what reading a branch's worktree found.
type observation struct {
	gone       bool // the worktree is no longer there
	snap       git.Snapshot
	baseCommit string
	mergeBase  string
	files      []git.FileChange
	ahead      int
	behind     int
	merged     bool
	inProgress bool
	err        error
}

// paths returns every path the observation's changes touch: both sides of
// a rename count.
func (o *observation) paths() []string {
	out := make([]string, 0, len(o.files))
	for _, f := range o.files {
		out = append(out, f.Path)
		if f.From != "" {
			out = append(out, f.From)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Refresh reads every active branch of a repository again: its state, its
// changes and its overlaps with the others. Merged branches are then
// cleaned up as the policy says.
func (s *Service) Refresh(ctx context.Context, repo string) error {
	unlock := s.lockRepo(repo)
	merged := s.refreshLocked(ctx, repo)
	unlock()
	if s.cfg.Cleanup != CleanupMerged {
		return ctx.Err()
	}
	for _, id := range merged {
		if ctx.Err() != nil {
			break
		}
		s.autoClean(ctx, id)
	}
	return ctx.Err()
}

// refreshLocked does Refresh's reading, with the repository locked, and
// returns the branches that are merged.
func (s *Service) refreshLocked(ctx context.Context, repo string) (merged []string) {
	s.mu.Lock()
	var list []Branch
	for _, b := range s.branches {
		if b.State.Active() && samePath(b.Repo, repo) {
			list = append(list, b.clone())
		}
	}
	s.mu.Unlock()
	if len(list) == 0 {
		return nil
	}
	slices.SortFunc(list, func(a, b Branch) int { return a.CreatedAt.Compare(b.CreatedAt) })

	obs := make(map[string]*observation, len(list))
	for i := range list {
		obs[list[i].ID] = s.observe(ctx, &list[i])
	}
	if ctx.Err() != nil {
		return nil
	}
	overlaps := s.overlaps(ctx, list, obs)

	now := s.cfg.Now()
	var events []OverlapEvent
	s.mu.Lock()
	for _, seen := range list {
		b := s.branches[seen.ID]
		if b == nil || !b.State.Active() || b.Path != seen.Path {
			continue // changed meanwhile: the next read will see it
		}
		o := obs[b.ID]
		before := b.clone()
		if o.gone {
			s.closeLocked(b, now)
			continue
		}
		s.applyLocked(b, o, now)
		b.Overlaps = overlaps[b.ID]
		events = append(events, newOverlaps(&before, b)...)
		if !sameObserved(&before, b) {
			s.commitLocked(b)
		}
		if b.State == StateMerged {
			merged = append(merged, b.ID)
		}
	}
	for _, ev := range events {
		s.publishLocked(EventOverlap, ev)
	}
	s.forgetLocked()
	s.mu.Unlock()
	s.persist()
	return merged
}

// observe reads one branch's worktree.
func (s *Service) observe(ctx context.Context, b *Branch) *observation {
	o := &observation{}
	if _, err := os.Stat(b.Path); errors.Is(err, os.ErrNotExist) {
		o.gone = true
		return o
	}
	if list, err := s.git.Worktrees(ctx, b.Repo); err == nil && !slices.ContainsFunc(list, func(w git.Worktree) bool { return samePath(w.Path, b.Path) }) {
		o.gone = true
		return o
	}
	if o.snap, o.err = s.git.Snapshot(ctx, b.Path); o.err != nil {
		return o
	}
	if o.baseCommit, o.err = s.git.Resolve(ctx, b.Repo, b.Base); o.err != nil {
		return o
	}
	if o.mergeBase, o.err = s.git.MergeBase(ctx, b.Path, o.snap.Commit, o.baseCommit); o.err != nil {
		return o
	}
	from := o.mergeBase
	if from == "" {
		from = b.BaseCommit
	}
	if from != "" && o.snap.Commit != "" {
		if o.files, o.err = s.git.Files(ctx, b.Path, from, o.snap.Commit); o.err != nil {
			return o
		}
	}
	if head := o.snap.Head; head != "" {
		if o.ahead, o.err = s.git.Count(ctx, b.Path, o.baseCommit, head); o.err != nil {
			return o
		}
		if o.behind, o.err = s.git.Count(ctx, b.Path, head, o.baseCommit); o.err != nil {
			return o
		}
		o.merged = s.merged(ctx, b, head, o.baseCommit, o.ahead)
	}
	o.inProgress = s.git.InProgress(ctx, b.Path)
	return o
}

// merged reports whether a branch's commits are in its base: its head is
// in the base (or the base's upstream, where a pull request merged and was
// fetched), or merging it would change nothing (a squash merge). A branch
// with no commits of its own has not merged anything.
func (s *Service) merged(ctx context.Context, b *Branch, head, baseCommit string, ahead int) bool {
	if head == b.BaseCommit || head == "" {
		return false
	}
	// A branch with no commits of its own has merged nothing: its head
	// may only have followed its base forward.
	if !b.Committed && ahead == 0 && !s.git.Committed(ctx, b.Path, b.Name) {
		return false
	}
	if ahead == 0 {
		return true
	}
	if !git.IsCommitID(b.Base) {
		if up, _ := s.git.Upstream(ctx, b.Repo, b.Base); up != "" {
			if n, err := s.git.Count(ctx, b.Path, up, head); err == nil && n == 0 {
				return true
			}
		}
	}
	m, err := s.git.MergeTree(ctx, b.Path, baseCommit, head)
	if err != nil || !m.Clean() {
		return false
	}
	baseTree, err := s.git.Tree(ctx, b.Path, baseCommit)
	return err == nil && baseTree == m.Tree
}

// applyLocked updates b from what was observed, and moves it through its
// states.
func (s *Service) applyLocked(b *Branch, o *observation, now time.Time) {
	if o.err != nil {
		b.Error = o.err.Error()
		return
	}
	b.Error = ""
	b.Head, b.Dirty = o.snap.Head, o.snap.Dirty
	b.Ahead, b.Behind, b.InProgress = o.ahead, o.behind, o.inProgress
	if o.ahead > 0 || o.merged {
		b.Committed = true
	}
	b.Files, b.Added, b.Deleted = len(o.files), 0, 0
	b.Changed = nil
	for _, f := range o.files {
		if f.Added > 0 {
			b.Added += f.Added
		}
		if f.Deleted > 0 {
			b.Deleted += f.Deleted
		}
		if len(b.Changed) < MaxChanged {
			b.Changed = append(b.Changed, f.Path)
		}
	}
	switch {
	case o.merged:
		if b.State != StateMerged {
			b.MergedAt = &now
		}
		b.State = StateMerged
		b.Started = true
	case b.ReadyTree != "" && b.ReadyTree == o.snap.Tree:
		b.State = StateReady
	default:
		if b.ReadyTree != "" {
			// Changed since its gates passed: they must pass again.
			b.ReadyTree, b.ReadyAt = "", nil
		}
		if b.State == StateMerged {
			b.MergedAt = nil // more work arrived after the merge
		}
		if len(o.files) > 0 || b.Ahead > 0 {
			b.Started = true
		}
		if b.Started {
			b.State = StateWorking
		} else {
			b.State = StateCreated
		}
	}
	if b.State != StateMerged {
		b.Cleanup = ""
	}
}

// sameObserved reports whether a refresh changed nothing worth an event.
func sameObserved(a, b *Branch) bool {
	return a.State == b.State && a.Head == b.Head && a.Dirty == b.Dirty && a.Ahead == b.Ahead &&
		a.Behind == b.Behind && a.Files == b.Files && a.Added == b.Added && a.Deleted == b.Deleted &&
		a.InProgress == b.InProgress && a.Error == b.Error && a.ReadyTree == b.ReadyTree &&
		a.Started == b.Started && a.Committed == b.Committed && a.Cleanup == b.Cleanup && slices.Equal(a.Changed, b.Changed) &&
		slices.EqualFunc(a.Overlaps, b.Overlaps, func(x, y Overlap) bool {
			return x.With == y.With && x.Branch == y.Branch && x.Unknown == y.Unknown &&
				slices.Equal(x.Files, y.Files) && slices.Equal(x.Conflicts, y.Conflicts)
		})
}

// overlaps compares the open branches of a repository with each other,
// and each with its base when the base moved: files both change, and
// which of those a merge would conflict on.
func (s *Service) overlaps(ctx context.Context, list []Branch, obs map[string]*observation) map[string][]Overlap {
	out := map[string][]Overlap{}
	type side struct {
		b     *Branch
		o     *observation
		paths []string
	}
	var open []side
	for i := range list {
		o := obs[list[i].ID]
		if o.gone || o.err != nil || o.merged || o.snap.Commit == "" || len(o.files) == 0 {
			continue
		}
		open = append(open, side{b: &list[i], o: o, paths: o.paths()})
	}
	for i := range open {
		for j := i + 1; j < len(open); j++ {
			a, b := open[i], open[j]
			common := intersect(a.paths, b.paths)
			if len(common) == 0 {
				continue
			}
			res := s.conflicts(ctx, a.b.Path, a.o.snap.Commit, a.o.snap.Tree, b.o.snap.Commit, b.o.snap.Tree)
			out[a.b.ID] = append(out[a.b.ID], Overlap{With: b.b.ID, Branch: b.b.Name, Files: capped(common), Conflicts: res.conflicts, Unknown: res.unknown})
			out[b.b.ID] = append(out[b.b.ID], Overlap{With: a.b.ID, Branch: a.b.Name, Files: capped(common), Conflicts: res.conflicts, Unknown: res.unknown})
		}
		// The base moved under the branch: does what it gained touch the
		// same files?
		x := open[i]
		if x.o.behind == 0 || x.o.mergeBase == "" {
			continue
		}
		baseFiles, err := s.git.Files(ctx, x.b.Path, x.o.mergeBase, x.o.baseCommit)
		if err != nil {
			continue
		}
		bo := observation{files: baseFiles}
		common := intersect(x.paths, bo.paths())
		if len(common) == 0 {
			continue
		}
		baseTree, err := s.git.Tree(ctx, x.b.Path, x.o.baseCommit)
		if err != nil {
			continue
		}
		res := s.conflicts(ctx, x.b.Path, x.o.snap.Commit, x.o.snap.Tree, x.o.baseCommit, baseTree)
		out[x.b.ID] = append(out[x.b.ID], Overlap{Branch: x.b.Base, Files: capped(common), Conflicts: res.conflicts, Unknown: res.unknown})
	}
	for id := range out {
		slices.SortFunc(out[id], func(a, b Overlap) int {
			if (a.With == "") != (b.With == "") {
				if a.With == "" {
					return -1
				}
				return 1
			}
			if a.Branch < b.Branch {
				return -1
			}
			if a.Branch > b.Branch {
				return 1
			}
			return 0
		})
	}
	return out
}

// conflicts merges two commits in memory, remembering the answer for
// their trees: unchanged branches are not merged again.
func (s *Service) conflicts(ctx context.Context, dir, a, treeA, b, treeB string) mergeResult {
	key := [2]string{treeA, treeB}
	if treeB < treeA {
		key = [2]string{treeB, treeA}
	}
	s.mu.Lock()
	res, ok := s.merges[key]
	s.mu.Unlock()
	if ok {
		return res
	}
	m, err := s.git.MergeTree(ctx, dir, a, b)
	switch {
	case errors.Is(err, git.ErrUnsupported):
		res = mergeResult{unknown: true}
	case err != nil:
		return mergeResult{unknown: true} // not remembered: try again next time
	default:
		res = mergeResult{conflicts: capped(m.Conflicts)}
	}
	s.mu.Lock()
	if len(s.merges) >= 4096 {
		clear(s.merges)
	}
	s.merges[key] = res
	s.mu.Unlock()
	return res
}

// newOverlaps returns the overlaps after has that before lacked (or that
// gained conflicts), as events. Each pair of branches is announced once,
// by the branch whose ID sorts first.
func newOverlaps(before, after *Branch) []OverlapEvent {
	var out []OverlapEvent
	for _, o := range after.Overlaps {
		if o.With != "" && o.With < after.ID {
			continue
		}
		i := slices.IndexFunc(before.Overlaps, func(x Overlap) bool { return x.With == o.With && x.Branch == o.Branch })
		if i >= 0 && len(before.Overlaps[i].Conflicts) >= len(o.Conflicts) && len(intersect(before.Overlaps[i].Files, o.Files)) > 0 {
			continue
		}
		out = append(out, OverlapEvent{
			Repo: after.Repo, IDs: [2]string{after.ID, o.With}, Names: [2]string{after.Name, o.Branch},
			Files: o.Files, Conflicts: o.Conflicts,
		})
	}
	return out
}

// intersect returns the strings in both sorted lists.
func intersect(a, b []string) []string {
	var out []string
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func capped(list []string) []string {
	if len(list) > MaxChanged {
		return slices.Clone(list[:MaxChanged])
	}
	return slices.Clone(list)
}
