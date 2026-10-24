package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// What reviewing a branch needs from git: a commit of a working tree as it
// is (agents rarely commit), the files and the patch between two commits,
// a merge computed without touching any working tree, and the few writes a
// person asks for by name (push, merge, rebase, delete a merged branch).

// EnvRunner is a Runner that can add variables to git's environment.
// ExecRunner is one; snapshots need it, to use an index of their own.
type EnvRunner interface {
	RunEnv(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)
}

// RunEnv runs `git -C dir args...` with env added to its environment.
func (ExecRunner) RunEnv(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &CommandError{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

var (
	// ErrUnsupported means the installed git is too old for a feature
	// (merge-tree --write-tree needs git 2.38).
	ErrUnsupported = errors.New("the installed git is too old for this (git 2.38 or newer is needed)")
	// ErrNoCommit means a reference names no commit.
	ErrNoCommit = errors.New("no such commit")
)

// exitCode is a failed git command's exit status, or -1.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// validRev rejects revisions that could be read as options.
func validRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, "\x00\n") {
		return fmt.Errorf("git: invalid revision %q", rev)
	}
	return nil
}

// IsCommitID reports whether s is a full commit ID (SHA-1 or SHA-256).
func IsCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Resolve returns the commit rev names, in full, or ErrNoCommit.
func (g *Git) Resolve(ctx context.Context, dir, rev string) (string, error) {
	if err := validRev(rev); err != nil {
		return "", err
	}
	out, err := g.Runner.Run(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if notRepo(err) {
		return "", ErrNotRepository
	}
	if err != nil {
		if exitCode(err) == 1 || isQuietFailure(err) {
			return "", fmt.Errorf("%w: %s", ErrNoCommit, rev)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func isQuietFailure(err error) bool {
	var ce *CommandError
	return errors.As(err, &ce) && strings.TrimSpace(ce.Stderr) == ""
}

// Tree returns the tree of a commit.
func (g *Git) Tree(ctx context.Context, dir, commit string) (string, error) {
	if err := validRev(commit); err != nil {
		return "", err
	}
	out, err := g.Runner.Run(ctx, dir, "rev-parse", "--verify", "--quiet", commit+"^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// MergeBase returns the best common ancestor of a and b; "" when they
// have none.
func (g *Git) MergeBase(ctx context.Context, dir, a, b string) (string, error) {
	if err := errors.Join(validRev(a), validRev(b)); err != nil {
		return "", err
	}
	out, err := g.Runner.Run(ctx, dir, "merge-base", "--end-of-options", a, b)
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// IsAncestor reports whether a is b or one of its ancestors.
func (g *Git) IsAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	if err := errors.Join(validRev(a), validRev(b)); err != nil {
		return false, err
	}
	_, err := g.Runner.Run(ctx, dir, "merge-base", "--is-ancestor", "--end-of-options", a, b)
	switch {
	case err == nil:
		return true, nil
	case exitCode(err) == 1:
		return false, nil
	}
	return false, err
}

// Count returns how many commits are in to but not in from.
func (g *Git) Count(ctx context.Context, dir, from, to string) (int, error) {
	if err := errors.Join(validRev(from), validRev(to)); err != nil {
		return 0, err
	}
	out, err := g.Runner.Run(ctx, dir, "rev-list", "--count", "--end-of-options", from+".."+to)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// Snapshot is a commit of a working tree as it is: its committed history,
// plus every change (staged or not) and every new file git does not
// ignore. Making one touches neither the working tree, nor the index, nor
// any reference, so it is safe while an agent works there.
type Snapshot struct {
	// Commit is the snapshot; it is Head when nothing is uncommitted.
	Commit string `json:"commit"`
	// Head is the commit checked out; "" before the first.
	Head string `json:"head,omitempty"`
	// Tree is the snapshot's tree: equal trees are equal working trees.
	Tree string `json:"tree"`
	// Dirty is set when the working tree differs from Head.
	Dirty bool `json:"dirty,omitempty"`
}

// snapshotIdentity signs snapshot commits, which are never on a branch, so
// git needs no identity from the user to make them.
var snapshotIdentity = []string{
	"GIT_AUTHOR_NAME=hive", "GIT_AUTHOR_EMAIL=hive@localhost",
	"GIT_COMMITTER_NAME=hive", "GIT_COMMITTER_EMAIL=hive@localhost",
}

// Snapshot commits the working tree containing dir as it is (see
// Snapshot). It works on a copy of the index, so git's stat cache spares
// it from reading unchanged files.
func (g *Git) Snapshot(ctx context.Context, dir string) (Snapshot, error) {
	er, ok := g.Runner.(EnvRunner)
	if !ok {
		return Snapshot{}, fmt.Errorf("git: snapshots need a runner that sets git's environment: %w", errors.ErrUnsupported)
	}
	root, err := g.Root(ctx, dir)
	if err != nil {
		return Snapshot{}, err
	}
	head, err := g.Head(ctx, root)
	if err != nil {
		return Snapshot{}, err
	}
	out, err := g.Runner.Run(ctx, root, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return Snapshot{}, err
	}
	tmp, err := os.CreateTemp("", "hive-index-*")
	if err != nil {
		return Snapshot{}, err
	}
	index := tmp.Name()
	defer os.Remove(index)
	defer os.Remove(index + ".lock")
	if src, err := os.Open(filepath.FromSlash(strings.TrimSpace(string(out)))); err == nil {
		_, err = io.Copy(tmp, src)
		if err = errors.Join(err, src.Close()); err != nil {
			return Snapshot{}, errors.Join(err, tmp.Close())
		}
	}
	if err := tmp.Close(); err != nil {
		return Snapshot{}, err
	}
	env := append([]string{"GIT_INDEX_FILE=" + index}, snapshotIdentity...)
	if fi, err := os.Stat(index); err == nil && fi.Size() == 0 {
		// No index yet (no commit, nothing added): start from HEAD's tree,
		// or from nothing.
		_ = os.Remove(index)
		if head != "" {
			if _, err := er.RunEnv(ctx, root, env, "read-tree", head); err != nil {
				return Snapshot{}, err
			}
		}
	}
	if _, err := er.RunEnv(ctx, root, env, "add", "--all", "--", "."); err != nil {
		return Snapshot{}, err
	}
	out, err = er.RunEnv(ctx, root, env, "write-tree")
	if err != nil {
		return Snapshot{}, err
	}
	tree := strings.TrimSpace(string(out))
	if head != "" {
		headTree, err := g.Tree(ctx, root, head)
		if err != nil {
			return Snapshot{}, err
		}
		if headTree == tree {
			return Snapshot{Commit: head, Head: head, Tree: tree}, nil
		}
	}
	args := []string{"commit-tree", "--no-gpg-sign", "-m", "hive: snapshot of uncommitted work", tree}
	if head != "" {
		args = append(args, "-p", head)
	}
	out, err = er.RunEnv(ctx, root, env, args...)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Commit: strings.TrimSpace(string(out)), Head: head, Tree: tree, Dirty: true}, nil
}

// FileChange is one file that differs between two commits.
type FileChange struct {
	Path string `json:"path"`
	// From is the old path of a renamed or copied file.
	From string `json:"from,omitempty"`
	// Status is A (added), M (modified), D (deleted), R (renamed), C
	// (copied) or T (type changed).
	Status string `json:"status"`
	// Added and Deleted count lines; both are -1 for a binary file.
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// Files lists the files that differ between from and to, with their line
// counts, in path order. Renames are found.
func (g *Git) Files(ctx context.Context, dir, from, to string) ([]FileChange, error) {
	if err := errors.Join(validRev(from), validRev(to)); err != nil {
		return nil, err
	}
	status, err := g.Runner.Run(ctx, dir, "diff", "--no-ext-diff", "--no-color", "--name-status", "-z", "-M", "--end-of-options", from, to, "--")
	if err != nil {
		return nil, err
	}
	nums, err := g.Runner.Run(ctx, dir, "diff", "--no-ext-diff", "--no-color", "--numstat", "-z", "-M", "--end-of-options", from, to, "--")
	if err != nil {
		return nil, err
	}
	files := parseNameStatus(status)
	counts := parseNumstat(nums)
	for i := range files {
		if c, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Deleted = c[0], c[1]
		}
	}
	slices.SortFunc(files, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// parseNameStatus reads `git diff --name-status -z`: a status, then one
// path, or two for renames and copies (R100, C75).
func parseNameStatus(out []byte) []FileChange {
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var files []FileChange
	for i := 0; i < len(fields); i++ {
		st := fields[i]
		if st == "" {
			continue
		}
		f := FileChange{Status: st[:1]}
		switch f.Status {
		case "R", "C":
			if i+2 >= len(fields) {
				return files
			}
			f.From, f.Path = fields[i+1], fields[i+2]
			i += 2
		default:
			if i+1 >= len(fields) {
				return files
			}
			f.Path = fields[i+1]
			i++
		}
		files = append(files, f)
	}
	return files
}

// parseNumstat reads `git diff --numstat -z`: "added\tdeleted\tpath\0", or
// for a rename "added\tdeleted\t\0from\0to\0". Binary files count "-".
func parseNumstat(out []byte) map[string][2]int {
	fields := strings.Split(string(out), "\x00")
	counts := map[string][2]int{}
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(fields) {
			path = fields[i+2]
			i += 2
		}
		added, errA := strconv.Atoi(parts[0])
		deleted, errD := strconv.Atoi(parts[1])
		if errA != nil || errD != nil {
			added, deleted = -1, -1
		}
		counts[path] = [2]int{added, deleted}
	}
	return counts
}

// Patch returns the unified diff from from to to (limited to paths when
// any are given), at most limit bytes of it; truncated says whether there
// was more. Renames are found and binary files are named, not shown.
func (g *Git) Patch(ctx context.Context, dir, from, to string, contextLines, limit int, paths ...string) (patch string, truncated bool, err error) {
	if err := errors.Join(validRev(from), validRev(to)); err != nil {
		return "", false, err
	}
	args := []string{"diff", "--no-ext-diff", "--no-color", "--no-textconv", "-M", fmt.Sprintf("-U%d", max(contextLines, 0)), "--end-of-options", from, to, "--"}
	for _, p := range paths {
		args = append(args, ":(literal)"+p)
	}
	out, err := g.Runner.Run(ctx, dir, args...)
	if err != nil {
		return "", false, err
	}
	if limit > 0 && len(out) > limit {
		cut := bytes.LastIndexByte(out[:limit], '\n')
		if cut < 0 {
			cut = limit
		}
		return strings.ToValidUTF8(string(out[:cut+1]), "�"), true, nil
	}
	return strings.ToValidUTF8(string(out), "�"), false, nil
}

// Merge is a merge git computed without a working tree.
type Merge struct {
	// Tree is the merged tree; conflicted files in it hold conflict
	// markers.
	Tree string `json:"tree"`
	// Conflicts are the paths git could not merge, sorted.
	Conflicts []string `json:"conflicts,omitempty"`
}

// Clean reports whether the merge had no conflicts.
func (m Merge) Clean() bool { return len(m.Conflicts) == 0 }

// MergeTree merges a and b in memory, as `git merge` would, and reports
// the conflicts. Nothing is written but objects. It needs git 2.38.
func (g *Git) MergeTree(ctx context.Context, dir, a, b string) (Merge, error) {
	if err := errors.Join(validRev(a), validRev(b)); err != nil {
		return Merge{}, err
	}
	out, err := g.Runner.Run(ctx, dir, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", "--end-of-options", a, b)
	if err != nil && exitCode(err) != 1 {
		var ce *CommandError
		if errors.As(err, &ce) && (strings.Contains(ce.Stderr, "unknown option") || strings.Contains(ce.Stderr, "usage: git merge-tree")) {
			return Merge{}, ErrUnsupported
		}
		return Merge{}, err
	}
	return parseMergeTree(out), nil
}

// parseMergeTree reads `git merge-tree --write-tree --name-only -z`: the
// tree, NUL, then each conflicted path, NUL-terminated, then an empty
// field before any messages.
func parseMergeTree(out []byte) Merge {
	fields := strings.Split(string(out), "\x00")
	m := Merge{Tree: strings.TrimSpace(fields[0])}
	for _, f := range fields[1:] {
		if f == "" {
			break
		}
		m.Conflicts = append(m.Conflicts, f)
	}
	slices.Sort(m.Conflicts)
	m.Conflicts = slices.Compact(m.Conflicts)
	return m
}

// Show returns a file as it is in a tree or commit, at most limit bytes.
func (g *Git) Show(ctx context.Context, dir, treeish, path string, limit int) (string, bool, error) {
	if err := validRev(treeish); err != nil {
		return "", false, err
	}
	out, err := g.Runner.Run(ctx, dir, "cat-file", "blob", treeish+":"+path)
	if err != nil {
		return "", false, err
	}
	if limit > 0 && len(out) > limit {
		return strings.ToValidUTF8(string(out[:limit]), "�"), true, nil
	}
	return strings.ToValidUTF8(string(out), "�"), false, nil
}

// CurrentBranch returns the branch checked out in dir; "" when detached.
func (g *Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := g.Runner.Run(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Upstream returns the remote-tracking branch ref follows ("" when none).
func (g *Git) Upstream(ctx context.Context, dir, branch string) (string, error) {
	if err := ValidateBranch(branch); err != nil {
		return "", err
	}
	out, err := g.Runner.Run(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		return "", nil // no upstream, or no such branch
	}
	return strings.TrimSpace(string(out)), nil
}

// Remotes lists the repository's remotes.
func (g *Git) Remotes(ctx context.Context, dir string) ([]string, error) {
	out, err := g.Runner.Run(ctx, dir, "remote")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// RemoteURL returns the URL a remote fetches from.
func (g *Git) RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	if err := validRev(remote); err != nil {
		return "", err
	}
	out, err := g.Runner.Run(ctx, dir, "remote", "get-url", "--", remote)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Push pushes a local branch to the branch of the same name on remote and
// makes it the branch's upstream. It never forces.
func (g *Git) Push(ctx context.Context, dir, remote, branch string) error {
	if err := errors.Join(validRev(remote), ValidateBranch(branch)); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	_, err := g.Runner.Run(ctx, dir, "push", "--porcelain", "--set-upstream", "--end-of-options", remote, ref+":"+ref)
	return err
}

// Update brings onto into the branch checked out in dir: a merge, or with
// rebase a rebase. It is the person's merge, made on their command: on a
// conflict git stops and leaves it for them (Abort undoes it). conflicts
// lists the conflicted paths when it stopped.
func (g *Git) Update(ctx context.Context, dir, onto string, rebase bool) (conflicts []string, err error) {
	if err := validRev(onto); err != nil {
		return nil, err
	}
	args := []string{"merge", "--no-edit", "--no-ff", "--end-of-options", onto}
	if rebase {
		args = []string{"rebase", "--no-autostash", "--end-of-options", onto}
	}
	_, err = g.Runner.Run(ctx, dir, args...)
	if err == nil {
		return nil, nil
	}
	if st, serr := g.Status(ctx, dir); serr == nil && st.Conflicts > 0 {
		paths, _ := g.Runner.Run(ctx, dir, "diff", "--name-only", "--diff-filter=U", "-z")
		for p := range bytes.SplitSeq(paths, []byte{0}) {
			if len(p) > 0 {
				conflicts = append(conflicts, string(p))
			}
		}
		slices.Sort(conflicts)
		return slices.Compact(conflicts), nil
	}
	return nil, err
}

// Abort stops a merge or rebase in progress in dir, putting the branch
// back as it was.
func (g *Git) Abort(ctx context.Context, dir string) error {
	gitDir, _, err := g.Dirs(ctx, dir)
	if err != nil {
		return err
	}
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gitDir, d)); err == nil {
			_, err := g.Runner.Run(ctx, dir, "rebase", "--abort")
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(gitDir, "MERGE_HEAD")); err == nil {
		_, err := g.Runner.Run(ctx, dir, "merge", "--abort")
		return err
	}
	return errors.New("no merge or rebase is in progress")
}

// InProgress reports whether a merge or rebase is stopped in dir.
func (g *Git) InProgress(ctx context.Context, dir string) bool {
	gitDir, _, err := g.Dirs(ctx, dir)
	if err != nil {
		return false
	}
	for _, f := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gitDir, f)); err == nil {
			return true
		}
	}
	return false
}

// SaveRef points ref (under refs/hive/) at commit, so a commit nothing
// else reaches is kept: work saved before a worktree is discarded.
func (g *Git) SaveRef(ctx context.Context, repo, ref, commit string) error {
	if !strings.HasPrefix(ref, "refs/hive/") || ValidateBranch(strings.TrimPrefix(ref, "refs/")) != nil {
		return fmt.Errorf("git: invalid saved ref %q", ref)
	}
	if err := validRev(commit); err != nil {
		return err
	}
	_, err := g.Runner.Run(ctx, repo, "update-ref", "-m", "hive: saved work", "--", ref, commit)
	return err
}

// DeleteBranch deletes a local branch. Without force git refuses one that
// is not merged into its upstream or HEAD.
func (g *Git) DeleteBranch(ctx context.Context, repo, branch string, force bool) error {
	if err := ValidateBranch(branch); err != nil {
		return err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := g.Runner.Run(ctx, repo, "branch", flag, "--", branch)
	return err
}

// Subjects returns the subjects of the commits in to but not in from,
// oldest first, at most limit of them.
func (g *Git) Subjects(ctx context.Context, dir, from, to string, limit int) ([]string, error) {
	if err := errors.Join(validRev(from), validRev(to)); err != nil {
		return nil, err
	}
	out, err := g.Runner.Run(ctx, dir, "log", "--reverse", "--no-color", "--format=%s", "--end-of-options", from+".."+to, "--")
	if err != nil {
		return nil, err
	}
	var subjects []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line != "" && (limit <= 0 || len(subjects) < limit) {
			subjects = append(subjects, line)
		}
	}
	return subjects, nil
}

// Committed reports whether commits were made on a local branch (as
// opposed to it only following others forward), from its reflog: git
// records "commit: …" for each. A branch without a reflog reports false.
func (g *Git) Committed(ctx context.Context, dir, branch string) bool {
	if ValidateBranch(branch) != nil {
		return false
	}
	out, err := g.Runner.Run(ctx, dir, "reflog", "show", "--no-color", "--format=%gs", "refs/heads/"+branch, "--")
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "commit") || strings.HasPrefix(line, "cherry-pick") || strings.HasPrefix(line, "revert") {
			return true
		}
	}
	return false
}
