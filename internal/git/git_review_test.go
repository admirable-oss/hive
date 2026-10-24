package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/git"
)

func TestSnapshotLeavesTheWorkingTreeAlone(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	head, _ := g.Head(ctx, dir)

	clean, err := g.Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Commit != head || clean.Dirty || clean.Tree == "" {
		t.Fatalf("a clean tree's snapshot is HEAD: %+v (head %s)", clean, head)
	}

	write(t, filepath.Join(dir, "README.md"), "changed\n")
	write(t, filepath.Join(dir, "new.txt"), "new\n")
	write(t, filepath.Join(dir, ".gitignore"), "ignored.txt\n")
	write(t, filepath.Join(dir, "ignored.txt"), "secret\n")
	statusBefore := run(t, dir, "status", "--porcelain")
	snap, err := g.Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Dirty || snap.Commit == head || snap.Head != head {
		t.Fatalf("dirty snapshot %+v", snap)
	}
	if after := run(t, dir, "status", "--porcelain"); after != statusBefore {
		t.Fatalf("the snapshot changed the index or tree:\n%s\nwas\n%s", after, statusBefore)
	}
	if got := run(t, dir, "show", snap.Commit+":new.txt"); got != "new\n" {
		t.Fatalf("new file in the snapshot: %q", got)
	}
	if _, _, err := g.Show(ctx, dir, snap.Commit, "ignored.txt", 0); err == nil {
		t.Fatal("an ignored file is not part of the snapshot")
	}
	again, err := g.Snapshot(ctx, dir)
	if err != nil || again.Tree != snap.Tree {
		t.Fatalf("the same working tree has the same tree: %+v %v", again, err)
	}

	files, err := g.Files(ctx, dir, head, snap.Commit)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Status+" "+f.Path)
	}
	if strings.Join(got, ",") != "A .gitignore,M README.md,A new.txt" {
		t.Fatalf("files: %v", got)
	}
	if files[1].Added != 1 || files[1].Deleted != 1 {
		t.Fatalf("README counts: %+v", files[1])
	}
	patch, truncated, err := g.Patch(ctx, dir, head, snap.Commit, 3, 0, "README.md")
	if err != nil || truncated || !strings.Contains(patch, "-hello\n+changed\n") || strings.Contains(patch, "new.txt") {
		t.Fatalf("patch %q %v %v", patch, truncated, err)
	}
	if short, truncated, _ := g.Patch(ctx, dir, head, snap.Commit, 3, 40); !truncated || len(short) > 40 {
		t.Fatalf("limited patch %q %v", short, truncated)
	}
}

func TestSnapshotOfARepositoryWithoutCommits(t *testing.T) {
	g := git.New()
	dir := repo(t)
	empty := filepath.Join(dir, "empty")
	_ = os.Mkdir(empty, 0o755)
	run(t, empty, "init", "-q")
	write(t, filepath.Join(empty, "a.txt"), "a\n")
	snap, err := g.Snapshot(context.Background(), empty)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Head != "" || !snap.Dirty || snap.Commit == "" {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestRenamesAndBinaryFiles(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	base, _ := g.Head(ctx, dir)
	write(t, filepath.Join(dir, "long.txt"), strings.Repeat("a line that stays\n", 20))
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "long")
	from, _ := g.Head(ctx, dir)
	run(t, dir, "mv", "long.txt", "renamed.txt")
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0, 1, 2, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := g.Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := g.Files(ctx, dir, from, snap.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "blob.bin" || files[0].Added != -1 ||
		files[1].Status != "R" || files[1].From != "long.txt" || files[1].Path != "renamed.txt" {
		t.Fatalf("files: %+v", files)
	}
	if n, err := g.Count(ctx, dir, base, from); err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
}

func TestMergeTreeFindsConflictsWithoutAWorktree(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	write(t, filepath.Join(dir, "shared.txt"), "one\ntwo\nthree\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "shared")

	run(t, dir, "checkout", "-q", "-b", "a")
	write(t, filepath.Join(dir, "shared.txt"), "one\nTWO from a\nthree\n")
	write(t, filepath.Join(dir, "only-a.txt"), "a\n")
	run(t, dir, "commit", "-q", "-am", "a")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "a2")

	run(t, dir, "checkout", "-q", "main")
	run(t, dir, "checkout", "-q", "-b", "b")
	write(t, filepath.Join(dir, "shared.txt"), "one\nTWO from b\nthree\n")
	run(t, dir, "commit", "-q", "-am", "b")

	run(t, dir, "checkout", "-q", "main")
	run(t, dir, "checkout", "-q", "-b", "c")
	write(t, filepath.Join(dir, "shared.txt"), "one\ntwo\nthree\nfour from c\n")
	run(t, dir, "commit", "-q", "-am", "c")
	statusBefore := run(t, dir, "status", "--porcelain")

	m, err := g.MergeTree(ctx, dir, "a", "b")
	if errors.Is(err, git.ErrUnsupported) {
		t.Skip("git is older than 2.38")
	}
	if err != nil {
		t.Fatal(err)
	}
	if m.Clean() || strings.Join(m.Conflicts, ",") != "shared.txt" || m.Tree == "" {
		t.Fatalf("a and b conflict on shared.txt: %+v", m)
	}
	content, _, err := g.Show(ctx, dir, m.Tree, "shared.txt", 0)
	if err != nil || !strings.Contains(content, "<<<<<<<") || !strings.Contains(content, "TWO from a") {
		t.Fatalf("conflicted content %q %v", content, err)
	}
	if m, err := g.MergeTree(ctx, dir, "a", "c"); err != nil || !m.Clean() {
		t.Fatalf("a and c touch the same file without conflicting: %+v %v", m, err)
	}
	if after := run(t, dir, "status", "--porcelain"); after != statusBefore {
		t.Fatal("merge-tree touched the working tree")
	}

	if ok, err := g.IsAncestor(ctx, dir, "main", "a"); err != nil || !ok {
		t.Fatalf("main is an ancestor of a: %v %v", ok, err)
	}
	if ok, err := g.IsAncestor(ctx, dir, "a", "main"); err != nil || ok {
		t.Fatalf("a is not an ancestor of main: %v %v", ok, err)
	}
	mb, err := g.MergeBase(ctx, dir, "a", "b")
	if main, _ := g.Resolve(ctx, dir, "main"); err != nil || mb != main {
		t.Fatalf("merge base %q %v", mb, err)
	}
	if _, err := g.Resolve(ctx, dir, "nope"); !errors.Is(err, git.ErrNoCommit) {
		t.Fatalf("resolving an unknown ref: %v", err)
	}
	if _, err := g.Resolve(ctx, dir, "--all"); err == nil {
		t.Fatal("a revision that looks like an option is refused")
	}
}

func TestUpdateStopsOnAConflictForAPerson(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	write(t, filepath.Join(dir, "f.txt"), "base\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "f")
	run(t, dir, "checkout", "-q", "-b", "work")
	write(t, filepath.Join(dir, "f.txt"), "work\n")
	run(t, dir, "commit", "-q", "-am", "work")
	run(t, dir, "checkout", "-q", "main")
	write(t, filepath.Join(dir, "f.txt"), "main\n")
	run(t, dir, "commit", "-q", "-am", "main")
	run(t, dir, "checkout", "-q", "work")
	before, _ := g.Head(ctx, dir)

	for _, rebase := range []bool{false, true} {
		conflicts, err := g.Update(ctx, dir, "main", rebase)
		if err != nil || strings.Join(conflicts, ",") != "f.txt" {
			t.Fatalf("rebase=%v: conflicts %v %v", rebase, conflicts, err)
		}
		if !g.InProgress(ctx, dir) {
			t.Fatalf("rebase=%v: the conflict is left for a person", rebase)
		}
		if err := g.Abort(ctx, dir); err != nil {
			t.Fatal(err)
		}
		if g.InProgress(ctx, dir) {
			t.Fatal("aborted")
		}
		if h, _ := g.Head(ctx, dir); h != before {
			t.Fatalf("abort restores the branch: %s != %s", h, before)
		}
	}
	if err := g.Abort(ctx, dir); err == nil {
		t.Fatal("nothing to abort")
	}
}

func TestPushAndDeleteBranch(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	run(t, dir, "init", "-q", "--bare", remote)
	run(t, dir, "remote", "add", "origin", remote)
	run(t, dir, "checkout", "-q", "-b", "feature/x")
	write(t, filepath.Join(dir, "x.txt"), "x\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "x")

	if remotes, err := g.Remotes(ctx, dir); err != nil || strings.Join(remotes, ",") != "origin" {
		t.Fatalf("remotes %v %v", remotes, err)
	}
	if u, err := g.RemoteURL(ctx, dir, "origin"); err != nil || u != remote {
		t.Fatalf("url %q %v", u, err)
	}
	if err := g.Push(ctx, dir, "origin", "feature/x"); err != nil {
		t.Fatal(err)
	}
	if up, _ := g.Upstream(ctx, dir, "feature/x"); up != "origin/feature/x" {
		t.Fatalf("upstream %q", up)
	}
	if cur, _ := g.CurrentBranch(ctx, dir); cur != "feature/x" {
		t.Fatalf("current %q", cur)
	}
	h, _ := g.Head(ctx, dir)
	if err := g.SaveRef(ctx, dir, "refs/hive/saved/x", h); err != nil {
		t.Fatal(err)
	}
	if err := g.SaveRef(ctx, dir, "refs/heads/main", h); err == nil {
		t.Fatal("only refs/hive/ may be saved")
	}
	run(t, dir, "branch", "unpushed")
	run(t, dir, "checkout", "-q", "main")
	if err := g.DeleteBranch(ctx, dir, "unpushed", false); err == nil {
		t.Fatal("an unmerged branch is kept without force")
	}
	if err := g.DeleteBranch(ctx, dir, "unpushed", true); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteBranch(ctx, dir, "feature/x", false); err != nil {
		t.Fatalf("a branch merged into its upstream may go: %v", err)
	}
	if g.BranchExists(ctx, dir, "feature/x") || g.BranchExists(ctx, dir, "unpushed") {
		t.Fatal("deleted")
	}
	if c, err := g.Resolve(ctx, dir, "refs/hive/saved/x"); err != nil || c != h {
		t.Fatalf("the saved ref keeps the work: %q %v", c, err)
	}
}

func TestCommittedAndSubjects(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	base, _ := g.Head(ctx, dir)
	run(t, dir, "branch", "follower")
	run(t, dir, "checkout", "-q", "-b", "worker")
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "first")
	write(t, filepath.Join(dir, "b.txt"), "b\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "second")
	run(t, dir, "checkout", "-q", "follower")
	run(t, dir, "merge", "-q", "--ff-only", "worker")

	if !g.Committed(ctx, dir, "worker") {
		t.Fatal("worker made commits")
	}
	if g.Committed(ctx, dir, "follower") {
		t.Fatal("follower only moved forward")
	}
	if g.Committed(ctx, dir, "-x") {
		t.Fatal("invalid branch")
	}
	subjects, err := g.Subjects(ctx, dir, base, "worker", 0)
	if err != nil || strings.Join(subjects, ",") != "first,second" {
		t.Fatalf("subjects %v %v", subjects, err)
	}
	if one, _ := g.Subjects(ctx, dir, base, "worker", 1); len(one) != 1 {
		t.Fatalf("limit: %v", one)
	}
	if !git.IsCommitID(base) || git.IsCommitID("main") || git.IsCommitID(strings.ToUpper(base)) {
		t.Fatal("IsCommitID")
	}
}
