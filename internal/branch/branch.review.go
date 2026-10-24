package branch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/git"
)

// Patch limits.
const (
	// DefaultPatchBytes is how much of a patch Diff returns by default.
	DefaultPatchBytes = 512 << 10
	// MaxPatchBytes is the most it returns.
	MaxPatchBytes = 4 << 20
	// MaxConflictBytes bounds each conflicted file Conflicts shows.
	MaxConflictBytes = 64 << 10
)

// DiffRequest asks for a branch's changes.
type DiffRequest struct {
	Ref string `json:"ref" desc:"the branch: its ID, environment, agent, plan/task or name"`
	// Paths limit the diff to these files.
	Paths []string `json:"paths,omitempty" desc:"only these files"`
	// Context is the lines of context around each change (default 3).
	Context *int `json:"context,omitempty" desc:"lines of context around each change (default 3)"`
	// Limit bounds the patch in bytes (default 512 KiB, at most 4 MiB).
	Limit int `json:"limit,omitempty" desc:"the most bytes of patch to return (default 512 KiB, at most 4 MiB)"`
	// Stat leaves the patch out: only the files and their counts.
	Stat bool `json:"stat,omitempty" desc:"only the files and their line counts, no patch"`
}

// Diff returns a branch's changes against where it forked from its base,
// uncommitted changes and new files included.
func (s *Service) Diff(ctx context.Context, req DiffRequest) (Diff, error) {
	b, err := s.Get(ctx, req.Ref)
	if err != nil {
		return Diff{}, err
	}
	for _, p := range req.Paths {
		if p == "" || !repoRelative(p) || strings.ContainsRune(p, 0) {
			return Diff{}, fmt.Errorf("%w: path %q (paths are relative to the repository)", ErrInvalid, p)
		}
	}
	d := Diff{Branch: b.Name, Base: b.Base}
	dir := b.Path
	if b.State.Active() {
		snap, err := s.git.Snapshot(ctx, dir)
		if err != nil {
			return Diff{}, err
		}
		d.To, d.Head, d.Dirty = snap.Commit, snap.Head, snap.Dirty
	} else {
		// Closed: what the branch has, if git still has it.
		dir = b.Repo
		if d.To, err = s.git.Resolve(ctx, dir, "refs/heads/"+b.Name); err != nil {
			return Diff{}, fmt.Errorf("%w: branch %s is %s and git no longer has it", ErrState, b.Name, b.State)
		}
		d.Head = d.To
	}
	d.From = b.BaseCommit
	if base, err := s.git.Resolve(ctx, dir, b.Base); err == nil {
		if mb, err := s.git.MergeBase(ctx, dir, d.To, base); err == nil && mb != "" {
			d.From = mb
		}
	}
	if d.From == "" {
		return Diff{}, fmt.Errorf("%w: branch %s has no base to compare with", ErrState, b.Name)
	}
	files, err := s.git.Files(ctx, dir, d.From, d.To)
	if err != nil {
		return Diff{}, err
	}
	if len(req.Paths) > 0 {
		files = slices.DeleteFunc(files, func(f git.FileChange) bool {
			return !slices.Contains(req.Paths, f.Path) && (f.From == "" || !slices.Contains(req.Paths, f.From))
		})
	}
	d.Files = files
	if d.Files == nil {
		d.Files = []git.FileChange{}
	}
	if req.Stat {
		return d, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultPatchBytes
	}
	limit = min(limit, MaxPatchBytes)
	lines := 3
	if req.Context != nil {
		lines = min(max(*req.Context, 0), 1000)
	}
	d.Patch, d.Truncated, err = s.git.Patch(ctx, dir, d.From, d.To, lines, limit, req.Paths...)
	return d, err
}

// ConflictsRequest asks what merging a branch would conflict on.
type ConflictsRequest struct {
	Ref string `json:"ref" desc:"the branch"`
	// With is another branch (any reference to it); empty means the
	// branch's base.
	With string `json:"with,omitempty" desc:"another branch to merge with (default: its base)"`
}

// Conflicts merges a branch, as it is now, with its base or another
// branch, in memory, and shows each conflicted file as the merge leaves
// it. Nothing is merged and no worktree is touched.
func (s *Service) Conflicts(ctx context.Context, req ConflictsRequest) (Conflicts, error) {
	b, err := s.Get(ctx, req.Ref)
	if err != nil {
		return Conflicts{}, err
	}
	if !b.State.Active() {
		return Conflicts{}, fmt.Errorf("%w: branch %s is %s", ErrState, b.Name, b.State)
	}
	snap, err := s.git.Snapshot(ctx, b.Path)
	if err != nil {
		return Conflicts{}, err
	}
	out := Conflicts{Branch: b.Name, With: b.Base}
	other := ""
	if req.With == "" {
		if other, err = s.git.Resolve(ctx, b.Path, b.Base); err != nil {
			return Conflicts{}, err
		}
	} else {
		o, err := s.Get(ctx, req.With)
		if err != nil {
			return Conflicts{}, err
		}
		if !o.State.Active() || !samePath(o.Repo, b.Repo) {
			return Conflicts{}, fmt.Errorf("%w: %s is not an open branch of the same repository", ErrInvalid, req.With)
		}
		osnap, err := s.git.Snapshot(ctx, o.Path)
		if err != nil {
			return Conflicts{}, err
		}
		other, out.With = osnap.Commit, o.Name
	}
	m, err := s.git.MergeTree(ctx, b.Path, snap.Commit, other)
	if err != nil {
		return Conflicts{}, err
	}
	out.Clean = m.Clean()
	for _, p := range m.Conflicts {
		f := ConflictFile{Path: p}
		f.Content, f.Truncated, _ = s.git.Show(ctx, b.Path, m.Tree, p, MaxConflictBytes)
		out.Files = append(out.Files, f)
	}
	return out, nil
}

// UpdateRequest brings a branch's base into it, on a person's command.
type UpdateRequest struct {
	Ref string `json:"ref" desc:"the branch"`
	// Rebase rebases the branch onto its base instead of merging it.
	Rebase bool `json:"rebase,omitempty" desc:"rebase onto the base instead of merging it"`
	// Abort undoes a merge or rebase that stopped on a conflict.
	Abort bool `json:"abort,omitempty" desc:"undo a merge or rebase stopped on a conflict"`
}

// UpdateResult is how an update ended.
type UpdateResult struct {
	Branch Branch `json:"branch"`
	// Conflicts are the files git stopped on, left for a person to
	// resolve (then commit, or abort).
	Conflicts []string `json:"conflicts,omitempty"`
}

// Update merges (or rebases onto) a branch's base in its worktree. It is
// the person's merge: it runs only when they ask, refuses while an agent
// works there or files are uncommitted, and stops on a conflict, which it
// never resolves.
func (s *Service) Update(ctx context.Context, req UpdateRequest) (UpdateResult, error) {
	b, err := s.Get(ctx, req.Ref)
	if err != nil {
		return UpdateResult{}, err
	}
	if !b.State.Active() {
		return UpdateResult{}, fmt.Errorf("%w: branch %s is %s", ErrState, b.Name, b.State)
	}
	unlock := s.lockRepo(b.Repo)
	defer unlock()
	var conflicts []string
	if req.Abort {
		if err := s.git.Abort(ctx, b.Path); err != nil {
			return UpdateResult{}, fmt.Errorf("%w: %w", ErrState, err)
		}
	} else {
		if s.git.InProgress(ctx, b.Path) {
			return UpdateResult{}, fmt.Errorf("%w: a merge or rebase is already stopped in %s; resolve it, or abort it", ErrState, b.Path)
		}
		if err := s.agentsIdle(ctx, b); err != nil {
			return UpdateResult{}, err
		}
		if st, err := s.git.Status(ctx, b.Path); err == nil && st.Dirty() {
			return UpdateResult{}, fmt.Errorf("%w: %s has uncommitted changes; commit them first", ErrState, b.Path)
		}
		if conflicts, err = s.git.Update(ctx, b.Path, b.Base, req.Rebase); err != nil {
			return UpdateResult{}, err
		}
	}
	s.refreshLocked(ctx, b.Repo)
	b, err = s.Get(ctx, b.ID)
	return UpdateResult{Branch: b, Conflicts: conflicts}, err
}

// agentsIdle refuses while an agent works or waits on a person in the
// branch's worktree: changing its files under it would confuse both.
func (s *Service) agentsIdle(ctx context.Context, b Branch) error {
	if s.ports.Agents == nil || b.EnvironmentID == "" {
		return nil
	}
	agents, err := s.ports.Agents.In(ctx, b.EnvironmentID)
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.State == "working" || a.State == "blocked" {
			return fmt.Errorf("%w: agent %s is %s in %s; wait for it, or stop it", ErrState, a.ID, a.State, b.Path)
		}
	}
	return nil
}

// CommentRequest adds a review comment.
type CommentRequest struct {
	Ref  string `json:"ref" desc:"the branch"`
	Path string `json:"path,omitempty" desc:"the file it is about"`
	Line int    `json:"line,omitempty" desc:"the line it is about, in the branch's version of the file"`
	Body string `json:"body" desc:"the comment"`
	By   string `json:"by,omitempty" desc:"who wrote it"`
	// Send sends it, and every comment not sent yet, to the agent now.
	Send bool `json:"send,omitempty" desc:"send it (and every comment not sent yet) to the branch's agent now"`
}

// Comment adds a review comment to a branch, and with Send sends the
// comments not sent yet to its agent.
func (s *Service) Comment(ctx context.Context, req CommentRequest) (Branch, error) {
	body := strings.TrimSpace(req.Body)
	switch {
	case body == "":
		return Branch{}, fmt.Errorf("%w: the comment is empty", ErrInvalid)
	case len(body) > MaxCommentBytes:
		return Branch{}, fmt.Errorf("%w: the comment is longer than %d bytes", ErrInvalid, MaxCommentBytes)
	case req.Line < 0, req.Line > 0 && req.Path == "":
		return Branch{}, fmt.Errorf("%w: a line needs a path, and is counted from 1", ErrInvalid)
	case req.Path != "" && !repoRelative(req.Path):
		return Branch{}, fmt.Errorf("%w: path %q (paths are relative to the repository)", ErrInvalid, req.Path)
	}
	b, err := s.find(ctx, req.Ref)
	if err != nil {
		return Branch{}, err
	}
	s.mu.Lock()
	next := 1
	for _, c := range b.Comments {
		next = max(next, c.ID+1)
	}
	b.Comments = append(b.Comments, Comment{ID: next, Path: req.Path, Line: req.Line, Body: body, By: req.By, At: s.cfg.Now()})
	for len(b.Comments) > MaxComments {
		i := slices.IndexFunc(b.Comments, func(c Comment) bool { return c.SentAt != nil })
		if i < 0 {
			i = 0
		}
		b.Comments = slices.Delete(b.Comments, i, i+1)
	}
	s.commitLocked(b)
	id := b.ID
	s.mu.Unlock()
	s.persist()
	if req.Send {
		return s.Send(ctx, id)
	}
	return s.Get(ctx, id)
}

// Send sends a branch's comments not sent yet to its agent, as one prompt.
func (s *Service) Send(ctx context.Context, ref string) (Branch, error) {
	b, err := s.Get(ctx, ref)
	if err != nil {
		return Branch{}, err
	}
	var pending []Comment
	for _, c := range b.Comments {
		if c.SentAt == nil {
			pending = append(pending, c)
		}
	}
	if len(pending) == 0 {
		return Branch{}, fmt.Errorf("%w: branch %s has no comments to send", ErrState, b.Name)
	}
	agent, err := s.agentOf(ctx, b)
	if err != nil {
		return Branch{}, err
	}
	if err := s.ports.Agents.Prompt(ctx, agent, reviewPrompt(b, pending)); err != nil {
		return Branch{}, err
	}
	now := s.cfg.Now()
	s.mu.Lock()
	if live := s.branches[b.ID]; live != nil {
		for i := range live.Comments {
			if live.Comments[i].SentAt == nil && slices.ContainsFunc(pending, func(c Comment) bool { return c.ID == live.Comments[i].ID }) {
				live.Comments[i].SentAt, live.Comments[i].SentTo = &now, agent
			}
		}
		live.Started = true
		s.commitLocked(live)
	}
	s.mu.Unlock()
	s.persist()
	return s.Get(ctx, b.ID)
}

// agentOf picks the agent that receives a branch's comments: its owner
// when it still runs there, else another live agent in its worktree.
func (s *Service) agentOf(ctx context.Context, b Branch) (string, error) {
	if s.ports.Agents == nil || b.EnvironmentID == "" {
		return "", fmt.Errorf("%w: branch %s (its comments are kept)", ErrNoAgent, b.Name)
	}
	agents, err := s.ports.Agents.In(ctx, b.EnvironmentID)
	if err != nil {
		return "", err
	}
	live := slices.DeleteFunc(agents, func(a AgentInfo) bool { return a.State == "exited" })
	if len(live) == 0 {
		return "", fmt.Errorf("%w: branch %s (its comments are kept; start an agent there and send them)", ErrNoAgent, b.Name)
	}
	if b.Owner != nil {
		for _, a := range live {
			if a.ID == b.Owner.Agent {
				return a.ID, nil
			}
		}
	}
	return live[0].ID, nil
}

// reviewPrompt turns comments into the prompt an agent gets.
func reviewPrompt(b Branch, comments []Comment) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Review comments on your changes (branch %s):\n\n", b.Name)
	for i, c := range comments {
		where := ""
		switch {
		case c.Path != "" && c.Line > 0:
			where = fmt.Sprintf("%s:%d: ", c.Path, c.Line)
		case c.Path != "":
			where = c.Path + ": "
		}
		body := strings.ReplaceAll(c.Body, "\n", "\n   ")
		fmt.Fprintf(&sb, "%d. %s%s\n", i+1, where, body)
	}
	sb.WriteString("\nPlease address each of them in this worktree.")
	return sb.String()
}

// ReadyRequest runs a branch's gates.
type ReadyRequest struct {
	Ref string `json:"ref" desc:"the branch"`
	// Gates replace the configured ones for this run.
	Gates []Gate `json:"gates,omitempty" desc:"gates to run instead of the configured ones"`
}

// Ready runs a branch's gates in its worktree, in order, stopping at the
// first that fails. When all pass and the files did not change meanwhile,
// the branch is ready, until its files change again. With no gates, it is
// ready as it is.
func (s *Service) Ready(ctx context.Context, req ReadyRequest) (Branch, error) {
	b, err := s.find(ctx, req.Ref)
	if err != nil {
		return Branch{}, err
	}
	gates := req.Gates
	s.mu.Lock()
	switch {
	case !b.State.Open():
		s.mu.Unlock()
		return Branch{}, fmt.Errorf("%w: branch %s is %s", ErrState, b.Name, b.State)
	case b.Checking:
		s.mu.Unlock()
		return Branch{}, fmt.Errorf("%w: branch %s's gates are already running", ErrState, b.Name)
	}
	if gates == nil {
		gates = s.gatesFor(b.Repo)
	}
	for _, g := range gates {
		if len(g.Command) == 0 || g.Command[0] == "" {
			s.mu.Unlock()
			return Branch{}, fmt.Errorf("%w: gate %q has no command", ErrInvalid, g.Name)
		}
	}
	b.Checking = true
	s.commitLocked(b)
	id, path, name, base := b.ID, b.Path, b.Name, b.Base
	s.mu.Unlock()

	results, tree, err := s.runGates(ctx, path, name, base, gates)

	now := s.cfg.Now()
	s.mu.Lock()
	b = s.branches[id]
	if b == nil || !b.State.Active() {
		s.mu.Unlock()
		return Branch{}, fmt.Errorf("%w: branch %s closed while its gates ran", ErrState, name)
	}
	b.Checking = false
	if results != nil {
		b.Gates = results
	}
	if err == nil {
		b.ReadyTree, b.ReadyAt, b.State, b.Started = tree, &now, StateReady, true
	}
	s.commitLocked(b)
	out := b.clone()
	s.mu.Unlock()
	s.persist()
	if err != nil {
		return out, err
	}
	s.log.Info("branch ready", "branch", name, "gates", len(results))
	return out, nil
}

// runGates runs gates in order and returns their results and, when all
// passed on unchanged files, the tree they passed on.
func (s *Service) runGates(ctx context.Context, path, name, base string, gates []Gate) ([]GateResult, string, error) {
	before, err := s.git.Snapshot(ctx, path)
	if err != nil {
		return nil, "", err
	}
	if len(gates) > 0 && s.ports.Commands == nil {
		return nil, "", errors.New("gates cannot run here")
	}
	env := map[string]string{"HIVE_BRANCH": name, "HIVE_BASE": base}
	results := make([]GateResult, 0, len(gates))
	failed := ""
	for _, g := range gates {
		r := GateResult{Name: g.Name, Command: g.Command, At: s.cfg.Now()}
		if failed != "" {
			r.Skipped, r.ExitCode = true, -1
			results = append(results, r)
			continue
		}
		timeout := DefaultGateTimeout
		if g.TimeoutMS > 0 {
			timeout = time.Duration(g.TimeoutMS) * time.Millisecond
		}
		gctx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		code, output, err := s.ports.Commands.Run(gctx, path, g.Command, env)
		timedOut := gctx.Err() != nil
		cancel()
		r.DurationMS = time.Since(start).Milliseconds()
		r.ExitCode, r.Output = code, output
		switch {
		case ctx.Err() != nil:
			return nil, "", ctx.Err()
		case timedOut:
			r.Error = fmt.Sprintf("timed out after %s", timeout)
		case err != nil:
			r.Error = err.Error()
		}
		r.Passed = r.Error == "" && code == 0
		if !r.Passed {
			failed = cmpStr(g.Name, strings.Join(g.Command, " "))
		}
		results = append(results, r)
	}
	if failed != "" {
		return results, "", fmt.Errorf("%w: gate %s failed", ErrState, failed)
	}
	after, err := s.git.Snapshot(ctx, path)
	if err != nil {
		return results, "", err
	}
	if after.Tree != before.Tree {
		return results, "", fmt.Errorf("%w: the files changed while the gates ran; run them again", ErrState)
	}
	return results, after.Tree, nil
}

// gatesFor returns the configured gates for a repository.
func (s *Service) gatesFor(repo string) []Gate {
	var out []Gate
	for _, g := range s.cfg.Gates {
		if g.Repo == "" || samePath(g.Repo, repo) || within(repo, g.Repo) {
			out = append(out, g)
		}
	}
	return out
}

// repoRelative reports whether p reads as a path inside a repository:
// not rooted on any platform, so "/etc" is refused on Windows too.
func repoRelative(p string) bool {
	return !filepath.IsAbs(p) && filepath.VolumeName(p) == "" && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, `\`)
}

// within reports whether path is inside dir.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
