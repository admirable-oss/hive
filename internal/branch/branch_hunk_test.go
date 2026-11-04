package branch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/git"
)

// hunks reads a branch's diff and returns the hunks of one file.
func (e *env) hunks(ref, path string) []git.Hunk {
	e.t.Helper()
	d, err := e.svc.Diff(context.Background(), branch.DiffRequest{Ref: ref})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, f := range git.ParsePatch(d.Patch) {
		if f.Name() == path {
			return f.Hunks
		}
	}
	e.t.Fatalf("%s is not in the diff:\n%s", path, d.Patch)
	return nil
}

func TestHunksAreAcceptedAndRejectedOneByOne(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	lines[0], lines[19] = "top", "bottom"
	write(t, filepath.Join(e.repo, "f.txt"), strings.Join(lines, "\n")+"\n")
	e.commit(e.repo, "f")
	b := e.worktree("h", "hunks", &branch.Owner{Agent: "p1", Kind: "claude"})

	changed := append([]string(nil), lines...)
	changed[0], changed[19] = "TOP", "BOTTOM"
	write(t, filepath.Join(b.Path, "f.txt"), strings.Join(changed, "\n")+"\n")
	e.commit(b.Path, "committed change")
	write(t, filepath.Join(b.Path, "f.txt"), strings.Join(append(changed[:19:19], "BOTTOM", "uncommitted"), "\n")+"\n")
	write(t, filepath.Join(b.Path, "added.txt"), "new\n")

	hs := e.hunks("h", "f.txt")
	if len(hs) != 2 {
		t.Fatalf("two hunks, one committed and one not: %+v", hs)
	}
	before, _ := os.ReadFile(filepath.Join(b.Path, "f.txt"))

	// Accepting records the verdict and changes nothing.
	res, err := e.svc.Hunk(ctx, branch.HunkRequest{Ref: "h", Path: "f.txt", Hunk: hs[1].ID, Action: branch.HunkAccept, By: "me"})
	if err != nil || res.Review.Verdict != branch.VerdictAccepted || len(res.Branch.Reviews) != 1 || res.Branch.Reviews[0].By != "me" {
		t.Fatalf("accepted: %+v %v", res, err)
	}
	if after, _ := os.ReadFile(filepath.Join(b.Path, "f.txt")); string(after) != string(before) {
		t.Fatal("accepting changes no file")
	}

	// Rejecting refuses while the agent works there, unless forced.
	e.agents.add(branch.AgentInfo{ID: "p1", EnvironmentID: "h", State: "working"})
	if _, err := e.svc.Hunk(ctx, branch.HunkRequest{Ref: "h", Path: "f.txt", Hunk: hs[0].ID, Action: branch.HunkReject}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("not while the agent works: %v", err)
	}
	res, err = e.svc.Hunk(ctx, branch.HunkRequest{Ref: "h", Path: "f.txt", Hunk: hs[0].ID, Action: branch.HunkReject, Force: true})
	if err != nil || res.Review.Verdict != branch.VerdictRejected {
		t.Fatalf("rejected: %+v %v", res, err)
	}
	got, _ := os.ReadFile(filepath.Join(b.Path, "f.txt"))
	if !strings.HasPrefix(string(got), "top\n") || !strings.HasSuffix(string(got), "BOTTOM\nuncommitted\n") {
		t.Fatalf("only the first hunk is taken out, even though it was committed:\n%s", got)
	}
	if st := run(t, b.Path, "diff", "--cached", "--name-only"); st != "" {
		t.Fatalf("the index is left alone: %q", st)
	}
	if head := run(t, e.repo, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(head) != "main" {
		t.Fatalf("the person's branch is untouched: %q", head)
	}
	if main, _ := os.ReadFile(filepath.Join(e.repo, "f.txt")); !strings.HasPrefix(string(main), "top\n") || strings.Contains(string(main), "BOTTOM") {
		t.Fatalf("the person's checkout is untouched:\n%s", main)
	}

	// The same hunk is gone, so a second reject is refused.
	if _, err := e.svc.Hunk(ctx, branch.HunkRequest{Ref: "h", Path: "f.txt", Hunk: hs[0].ID, Action: branch.HunkReject, Force: true}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("a hunk no longer in the diff: %v", err)
	}
	// A new file's only hunk removes it.
	add := e.hunks("h", "added.txt")
	if _, err := e.svc.Hunk(ctx, branch.HunkRequest{Ref: "h", Path: "added.txt", Hunk: add[0].ID, Action: branch.HunkReject, Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Path, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejecting a new file removes it: %v", err)
	}

	// Verdicts are kept with the branch, one per hunk.
	reloaded := branch.NewService(branch.Config{}, e.store, branch.Ports{Git: e.git})
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	kept, err := reloaded.Get(ctx, "h")
	if err != nil || len(kept.Reviews) != 3 {
		t.Fatalf("verdicts are stored: %+v %v", kept.Reviews, err)
	}

	for _, bad := range []branch.HunkRequest{
		{Ref: "h", Path: "f.txt", Hunk: hs[1].ID, Action: "stage"},
		{Ref: "h", Path: "/etc/passwd", Hunk: hs[1].ID, Action: branch.HunkAccept},
		{Ref: "h", Path: "f.txt", Action: branch.HunkAccept},
	} {
		if _, err := e.svc.Hunk(ctx, bad); !errors.Is(err, branch.ErrInvalid) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	if !strings.Contains(strings.Join(e.events.of(branch.EventUpdated), ","), "h") {
		t.Fatal("verdicts are published as branch.updated")
	}
}
