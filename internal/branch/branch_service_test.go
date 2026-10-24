package branch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/branch"
)

func TestLifecycleFromCreatedToCleaned(t *testing.T) {
	e := newEnv(t, branch.Config{DeleteMerged: true})
	ctx := context.Background()
	b := e.worktree("login", "feature/login", &branch.Owner{Agent: "p1", Kind: "claude"})
	if b.State != branch.StateCreated || b.Base != "main" || b.BaseCommit == "" || b.Owner.Kind != "claude" {
		t.Fatalf("tracked: %+v", b)
	}

	// Uncommitted work counts.
	write(t, filepath.Join(b.Path, "login.go"), "package login\n")
	e.refresh()
	b = e.get("login")
	if b.State != branch.StateWorking || b.Files != 1 || !b.Dirty || b.Ahead != 0 || strings.Join(b.Changed, ",") != "login.go" {
		t.Fatalf("working: %+v", b)
	}

	// Gates pass: ready, until the files change.
	e.commit(b.Path, "login")
	b, err := e.svc.Ready(ctx, branch.ReadyRequest{Ref: "login", Gates: []branch.Gate{{Name: "ok", Command: []string{"git", "status"}}}})
	if err != nil || b.State != branch.StateReady || len(b.Gates) != 1 || !b.Gates[0].Passed || b.ReadyAt == nil {
		t.Fatalf("ready: %+v %v", b, err)
	}
	e.refresh()
	if b = e.get("login"); b.State != branch.StateReady || b.Ahead != 1 || b.Dirty {
		t.Fatalf("still ready: %+v", b)
	}
	write(t, filepath.Join(b.Path, "login.go"), "package login // more\n")
	e.refresh()
	if b = e.get("login"); b.State != branch.StateWorking || b.ReadyTree != "" {
		t.Fatalf("a change after the gates makes it working again: %+v", b)
	}
	e.commit(b.Path, "more")

	// Merged into main: cleaned up, and the branch deleted.
	run(t, e.repo, "merge", "-q", "--no-edit", "feature/login")
	e.refresh()
	b = e.get("login")
	if b.State != branch.StateCleaned || b.Path != "" || b.MergedAt == nil || b.ClosedAt == nil {
		t.Fatalf("cleaned: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.repo), "worktrees", "feature-login")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the worktree is removed: %v", err)
	}
	if e.git.BranchExists(ctx, e.repo, "feature/login") {
		t.Fatal("delete_merged deletes the merged branch")
	}
	if got := e.svc.List(branch.ListRequest{}); len(got) != 0 {
		t.Fatalf("closed branches are listed only with all: %+v", got)
	}
	if got := e.svc.List(branch.ListRequest{All: true}); len(got) != 1 {
		t.Fatalf("all: %+v", got)
	}

	// Kept across a restart.
	again := branch.NewService(branch.Config{}, e.store, branch.Ports{Git: e.git})
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	if got, err := again.Get(ctx, "feature/login"); err != nil || got.State != branch.StateCleaned {
		t.Fatalf("reloaded: %+v %v", got, err)
	}
}

func TestSquashMergeCountsAsMerged(t *testing.T) {
	e := newEnv(t, branch.Config{Cleanup: branch.CleanupNever})
	b := e.worktree("s", "squash", nil)
	write(t, filepath.Join(b.Path, "a.txt"), "a\n")
	e.commit(b.Path, "a")
	write(t, filepath.Join(b.Path, "b.txt"), "b\n")
	e.commit(b.Path, "b")
	e.refresh()
	if b = e.get("s"); b.State != branch.StateWorking || b.Ahead != 2 {
		t.Fatalf("working: %+v", b)
	}
	run(t, e.repo, "merge", "-q", "--squash", "squash")
	run(t, e.repo, "commit", "-q", "-m", "squashed")
	e.refresh()
	if b = e.get("s"); b.State != branch.StateMerged || b.Path == "" {
		t.Fatalf("squash-merged, kept by the never policy: %+v", b)
	}
}

func TestABaseMovingForwardIsNotAMerge(t *testing.T) {
	e := newEnv(t, branch.Config{})
	b := e.worktree("ff", "follow", nil)
	write(t, filepath.Join(e.repo, "main.txt"), "main\n")
	e.commit(e.repo, "main moves")
	run(t, b.Path, "merge", "-q", "--ff-only", "main")
	e.refresh()
	if b = e.get("ff"); b.State == branch.StateMerged || b.State == branch.StateCleaned || b.Path == "" {
		t.Fatalf("a branch with no commits of its own has merged nothing: %+v", b)
	}
}

func TestMergedWorkIsNeverLost(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	b := e.worktree("w", "work", nil)
	write(t, filepath.Join(b.Path, "done.txt"), "done\n")
	e.commit(b.Path, "done")
	run(t, e.repo, "merge", "-q", "--no-edit", "work")

	// Uncommitted work after the merge: kept, and said why.
	write(t, filepath.Join(b.Path, "more.txt"), "unsaved\n")
	e.refresh()
	b = e.get("w")
	if b.State != branch.StateMerged || b.Path == "" || !strings.Contains(b.Cleanup, "uncommitted") {
		t.Fatalf("dirty merged branch kept: %+v", b)
	}
	if _, err := e.svc.Clean(ctx, branch.CleanRequest{Ref: "w"}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("clean without force refuses: %v", err)
	}
	list, err := e.svc.Clean(ctx, branch.CleanRequest{Ref: "w", Force: true})
	if err != nil || len(list) != 1 || list[0].State != branch.StateCleaned || !strings.HasPrefix(list[0].Saved, "refs/hive/saved/work-") {
		t.Fatalf("forced clean saves the work: %+v %v", list, err)
	}
	if got := run(t, e.repo, "show", list[0].Saved+":more.txt"); got != "unsaved\n" {
		t.Fatalf("saved work: %q", got)
	}
	if !e.git.BranchExists(ctx, e.repo, "work") {
		t.Fatal("the branch stays without delete_merged")
	}
}

func TestBusyMergedWorktreeWaitsForItsAgents(t *testing.T) {
	e := newEnv(t, branch.Config{})
	b := e.worktree("busy", "busy", nil)
	write(t, filepath.Join(b.Path, "x.txt"), "x\n")
	e.commit(b.Path, "x")
	run(t, e.repo, "merge", "-q", "--no-edit", "busy")
	e.ws.setBusy(b.Path, true)
	e.refresh()
	if b = e.get("busy"); b.State != branch.StateMerged || !strings.Contains(b.Cleanup, "processes") {
		t.Fatalf("waits: %+v", b)
	}
	e.ws.setBusy(b.Path, false)
	e.refresh()
	if b = e.get("busy"); b.State != branch.StateCleaned {
		t.Fatalf("cleaned once idle: %+v", b)
	}
}

func TestUnmergedWorktreeRemovedKeepsTheBranch(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	b := e.worktree("u", "unmerged", nil)
	write(t, filepath.Join(b.Path, "u.txt"), "u\n")
	e.commit(b.Path, "u")
	e.refresh()
	list, err := e.svc.Clean(ctx, branch.CleanRequest{Ref: "u", DeleteBranch: true})
	if err != nil || list[0].State != branch.StateRemoved {
		t.Fatalf("removed: %+v %v", list, err)
	}
	if !e.git.BranchExists(ctx, e.repo, "unmerged") {
		t.Fatal("an unmerged branch is never deleted")
	}
	if d, err := e.svc.Diff(ctx, branch.DiffRequest{Ref: "unmerged"}); err != nil || len(d.Files) != 1 || !strings.Contains(d.Patch, "+u") {
		t.Fatalf("a removed branch's diff comes from git: %+v %v", d, err)
	}
}

func TestWorktreeRemovedElsewhereClosesTheBranch(t *testing.T) {
	e := newEnv(t, branch.Config{})
	b := e.worktree("gone", "gone", nil)
	run(t, e.repo, "worktree", "remove", b.Path)
	e.refresh()
	if b = e.get("gone"); b.State != branch.StateRemoved || b.Path != "" {
		t.Fatalf("gone: %+v", b)
	}
}

func TestOverlapsAndConflictsBetweenAgents(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	a := e.worktree("a", "agent/a", nil)
	b := e.worktree("b", "agent/b", nil)
	c := e.worktree("c", "agent/c", nil)
	write(t, filepath.Join(a.Path, "shared.txt"), "one\nTWO by a\nthree\n")
	write(t, filepath.Join(b.Path, "shared.txt"), "one\nTWO by b\nthree\n")
	e.commit(b.Path, "b") // committed or not, both count
	write(t, filepath.Join(c.Path, "other.txt"), "c\n")
	e.refresh()

	a, b, c = e.get("a"), e.get("b"), e.get("c")
	if len(a.Overlaps) != 1 || a.Overlaps[0].With != "b" || strings.Join(a.Overlaps[0].Files, ",") != "shared.txt" {
		t.Fatalf("a overlaps b: %+v", a.Overlaps)
	}
	if !a.Overlaps[0].Unknown && strings.Join(a.Overlaps[0].Conflicts, ",") != "shared.txt" {
		t.Fatalf("a conflicts with b: %+v", a.Overlaps[0])
	}
	if len(b.Overlaps) != 1 || b.Overlaps[0].With != "a" || len(c.Overlaps) != 0 {
		t.Fatalf("b %+v, c %+v", b.Overlaps, c.Overlaps)
	}
	events := e.events.of(branch.EventOverlap)
	if len(events) != 1 || !strings.Contains(events[0], `"ids":["a","b"]`) {
		t.Fatalf("one event per new overlap: %v", events)
	}
	e.refresh()
	if events := e.events.of(branch.EventOverlap); len(events) != 1 {
		t.Fatalf("an overlap already known is not announced again: %v", events)
	}

	// The conflict, shown without touching either worktree.
	conf, err := e.svc.Conflicts(ctx, branch.ConflictsRequest{Ref: "a", With: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.Clean || len(conf.Files) != 1 || !strings.Contains(conf.Files[0].Content, "<<<<<<<") {
		t.Fatalf("conflicts: %+v", conf)
	}
	if got := run(t, a.Path, "status", "--porcelain"); got != " M shared.txt\n" {
		t.Fatalf("a's worktree untouched: %q", got)
	}

	// The base moves under c, touching what c touches.
	write(t, filepath.Join(e.repo, "other.txt"), "main\n")
	e.commit(e.repo, "main adds other.txt")
	e.refresh()
	c = e.get("c")
	if len(c.Overlaps) != 1 || c.Overlaps[0].With != "" || c.Overlaps[0].Branch != "main" || c.Behind != 1 {
		t.Fatalf("c overlaps its base: %+v", c)
	}
	conf, err = e.svc.Conflicts(ctx, branch.ConflictsRequest{Ref: "c"})
	if err != nil || conf.Clean || conf.With != "main" {
		t.Fatalf("c conflicts with main: %+v %v", conf, err)
	}
}

func TestUpdateIsLeftToAPerson(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	b := e.worktree("up", "update", nil)
	write(t, filepath.Join(b.Path, "shared.txt"), "one\nbranch\nthree\n")
	e.commit(b.Path, "branch")
	write(t, filepath.Join(e.repo, "shared.txt"), "one\nmain\nthree\n")
	e.commit(e.repo, "main")
	e.refresh()

	e.agents.add(branch.AgentInfo{ID: "p1", EnvironmentID: "up", State: "working"})
	if _, err := e.svc.Update(ctx, branch.UpdateRequest{Ref: "up"}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("not while an agent works: %v", err)
	}
	e.agents.byEnv["up"][0].State = "idle"
	res, err := e.svc.Update(ctx, branch.UpdateRequest{Ref: "up"})
	if err != nil || strings.Join(res.Conflicts, ",") != "shared.txt" || !res.Branch.InProgress {
		t.Fatalf("stops on the conflict: %+v %v", res, err)
	}
	if got := run(t, b.Path, "status", "--porcelain"); !strings.HasPrefix(got, "UU shared.txt") {
		t.Fatalf("left unresolved: %q", got)
	}
	if _, err := e.svc.Update(ctx, branch.UpdateRequest{Ref: "up"}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("one at a time: %v", err)
	}
	res, err = e.svc.Update(ctx, branch.UpdateRequest{Ref: "up", Abort: true})
	if err != nil || res.Branch.InProgress {
		t.Fatalf("aborted: %+v %v", res, err)
	}
}

func TestDiffIncludesUncommittedWork(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	b := e.worktree("d", "diff", nil)
	write(t, filepath.Join(b.Path, "README.md"), "hello\nworld\n")
	e.commit(b.Path, "readme")
	write(t, filepath.Join(b.Path, "new.txt"), "new\n")
	write(t, filepath.Join(e.repo, "main.txt"), "the base moved on\n")
	e.commit(e.repo, "main")

	d, err := e.svc.Diff(ctx, branch.DiffRequest{Ref: "d"})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, f := range d.Files {
		files = append(files, f.Status+" "+f.Path)
	}
	if strings.Join(files, ",") != "M README.md,A new.txt" || !d.Dirty || !strings.Contains(d.Patch, "+world") || !strings.Contains(d.Patch, "+new") {
		t.Fatalf("diff against the fork point, base changes left out: %v %+v", files, d)
	}
	zero := 0
	d, err = e.svc.Diff(ctx, branch.DiffRequest{Ref: "diff", Paths: []string{"new.txt"}, Context: &zero})
	if err != nil || len(d.Files) != 1 || strings.Contains(d.Patch, "README") {
		t.Fatalf("one file: %+v %v", d, err)
	}
	d, err = e.svc.Diff(ctx, branch.DiffRequest{Ref: "d", Stat: true})
	if err != nil || d.Patch != "" || len(d.Files) != 2 {
		t.Fatalf("stat: %+v %v", d, err)
	}
	if _, err := e.svc.Diff(ctx, branch.DiffRequest{Ref: "d", Paths: []string{"/etc/passwd"}}); !errors.Is(err, branch.ErrInvalid) {
		t.Fatalf("absolute paths are refused: %v", err)
	}
}

func TestCommentsGoBackToTheAgent(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	e.worktree("r", "review", &branch.Owner{Agent: "p2", Kind: "codex"})

	b, err := e.svc.Comment(ctx, branch.CommentRequest{Ref: "r", Path: "a.go", Line: 3, Body: "Handle the error.", By: "admir"})
	if err != nil || len(b.Comments) != 1 || b.Pending() != 1 {
		t.Fatalf("comment: %+v %v", b, err)
	}
	if _, err := e.svc.Send(ctx, "r"); !errors.Is(err, branch.ErrNoAgent) {
		t.Fatalf("no agent: %v", err)
	}
	if b = e.get("r"); b.Pending() != 1 {
		t.Fatal("unsent comments are kept")
	}

	e.agents.add(branch.AgentInfo{ID: "p9", EnvironmentID: "r", State: "idle"})
	e.agents.add(branch.AgentInfo{ID: "p2", EnvironmentID: "r", State: "idle"})
	b, err = e.svc.Comment(ctx, branch.CommentRequest{Ref: "p2", Body: "Add a test.", Send: true})
	if err != nil || b.Pending() != 0 || b.Comments[1].ID != 2 || b.Comments[0].SentTo != "p2" {
		t.Fatalf("sent: %+v %v", b, err)
	}
	if len(e.agents.prompts) != 1 {
		t.Fatalf("one prompt for both: %v", e.agents.prompts)
	}
	p := e.agents.prompts[0]
	if !strings.HasPrefix(p, "p2: ") || !strings.Contains(p, "1. a.go:3: Handle the error.") || !strings.Contains(p, "2. Add a test.") {
		t.Fatalf("the owner gets them, placed: %q", p)
	}
	if _, err := e.svc.Send(ctx, "r"); !errors.Is(err, branch.ErrState) {
		t.Fatalf("nothing left to send: %v", err)
	}
	for _, bad := range []branch.CommentRequest{{Ref: "r"}, {Ref: "r", Body: "x", Line: 2}, {Ref: "r", Body: "x", Path: "/abs"}} {
		if _, err := e.svc.Comment(ctx, bad); !errors.Is(err, branch.ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestGatesDecideReadiness(t *testing.T) {
	e := newEnv(t, branch.Config{Gates: []branch.Gate{
		{Name: "elsewhere", Command: []string{"false"}, Repo: "/somewhere/else"},
		{Name: "fails", Command: []string{"git", "rev-parse", "--verify", "nope"}},
		{Name: "after", Command: []string{"git", "status"}},
	}})
	ctx := context.Background()
	b := e.worktree("g", "gated", nil)
	write(t, filepath.Join(b.Path, "x.txt"), "x\n")
	b, err := e.svc.Ready(ctx, branch.ReadyRequest{Ref: "g"})
	if !errors.Is(err, branch.ErrState) || b.State == branch.StateReady || len(b.Gates) != 2 {
		t.Fatalf("a failing gate: %+v %v", b, err)
	}
	if b.Gates[0].Passed || b.Gates[0].ExitCode == 0 || !b.Gates[1].Skipped || b.Checking {
		t.Fatalf("results: %+v", b.Gates)
	}

	// The files change while the gate runs: not ready.
	touch := []string{"sh", "-c", "echo more >> x.txt"}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no sh")
	}
	b, err = e.svc.Ready(ctx, branch.ReadyRequest{Ref: "g", Gates: []branch.Gate{{Name: "edits", Command: touch}}})
	if !errors.Is(err, branch.ErrState) || !strings.Contains(err.Error(), "changed") || b.State == branch.StateReady {
		t.Fatalf("changed meanwhile: %+v %v", b, err)
	}
	env := []string{"sh", "-c", `test "$HIVE_BRANCH" = gated && test "$HIVE_BASE" = main`}
	if b, err = e.svc.Ready(ctx, branch.ReadyRequest{Ref: "g", Gates: []branch.Gate{{Name: "env", Command: env}}}); err != nil || b.State != branch.StateReady {
		t.Fatalf("gates know the branch: %+v %v", b, err)
	}
	if _, err := e.svc.Ready(ctx, branch.ReadyRequest{Ref: "g", Gates: []branch.Gate{{Name: "empty"}}}); !errors.Is(err, branch.ErrInvalid) {
		t.Fatalf("a gate needs a command: %v", err)
	}
}

func TestPullRequestsOnlyOnRequest(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	bare := filepath.Join(t.TempDir(), "remote.git")
	run(t, e.repo, "init", "-q", "--bare", bare)
	// Fetches name GitHub; pushes go to the bare repository.
	run(t, e.repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	run(t, e.repo, "config", "url."+bare+".pushInsteadOf", "https://github.com/acme/app.git")

	b := e.worktree("pr", "feature/pr", &branch.Owner{Kind: "claude"})
	if _, err := e.svc.OpenPullRequest(ctx, branch.PullRequestRequest{Ref: "pr"}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("nothing to propose: %v", err)
	}
	write(t, filepath.Join(b.Path, "pr.txt"), "pr\n")
	if _, err := e.svc.OpenPullRequest(ctx, branch.PullRequestRequest{Ref: "pr"}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("uncommitted changes: %v", err)
	}
	e.commit(b.Path, "Add pr.txt")
	if _, err := e.svc.OpenPullRequest(ctx, branch.PullRequestRequest{Ref: "pr"}); err == nil || !strings.Contains(err.Error(), "push it first") {
		t.Fatalf("never pushed unasked: %v", err)
	}
	if out := run(t, bare, "branch", "--list"); out != "" {
		t.Fatalf("nothing was pushed: %q", out)
	}
	b, err := e.svc.OpenPullRequest(ctx, branch.PullRequestRequest{Ref: "pr", Push: true, Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if b.PullRequest == nil || b.PullRequest.Number != 7 || b.Pushed == nil || b.Pushed.Remote != "origin" {
		t.Fatalf("opened: %+v", b)
	}
	req := e.forge.requests[0]
	if req.Host != "github.com" || req.Project != "acme/app" || req.Head != "feature/pr" || req.Base != "main" ||
		req.Title != "Add pr.txt" || !req.Draft || !strings.Contains(req.Body, "claude") {
		t.Fatalf("forge request: %+v", req)
	}
	if out := run(t, bare, "branch", "--list"); !strings.Contains(out, "feature/pr") {
		t.Fatalf("pushed on request: %q", out)
	}
	if _, err := e.svc.OpenPullRequest(ctx, branch.PullRequestRequest{Ref: "pr"}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("one pull request per branch: %v", err)
	}
}

func TestPushNeedsARemote(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	e.worktree("np", "no-remote", nil)
	if _, err := e.svc.Push(ctx, branch.PushRequest{Ref: "np"}); !errors.Is(err, branch.ErrState) {
		t.Fatalf("no remote: %v", err)
	}
	run(t, e.repo, "remote", "add", "one", t.TempDir())
	run(t, e.repo, "remote", "add", "two", t.TempDir())
	if _, err := e.svc.Push(ctx, branch.PushRequest{Ref: "np"}); !errors.Is(err, branch.ErrInvalid) {
		t.Fatalf("several remotes: %v", err)
	}
	if _, err := e.svc.Push(ctx, branch.PushRequest{Ref: "np", Remote: "three"}); !errors.Is(err, branch.ErrInvalid) {
		t.Fatalf("unknown remote: %v", err)
	}
}

func TestReferencesToABranch(t *testing.T) {
	e := newEnv(t, branch.Config{})
	ctx := context.Background()
	e.worktree("env-a", "hive/plan1/schema", &branch.Owner{Agent: "p7", Plan: "plan1", Task: "schema"})
	e.agents.add(branch.AgentInfo{ID: "p8", EnvironmentID: "env-a", State: "idle"})
	for _, ref := range []string{"env-a", "p7", "plan1/schema", "hive/plan1/schema", "p8"} {
		if b, err := e.svc.Get(ctx, ref); err != nil || b.ID != "env-a" {
			t.Errorf("%s: %+v %v", ref, b, err)
		}
	}
	if _, err := e.svc.Get(ctx, "nope"); !errors.Is(err, branch.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	// The same branch name in another repository is ambiguous.
	other := repo(t)
	path := filepath.Join(filepath.Dir(other), "wt")
	run(t, other, "worktree", "add", "-q", "-b", "hive/plan1/schema", path)
	if _, err := e.svc.Track(ctx, branch.TrackRequest{EnvironmentID: "env-b", Path: path, Repo: other, Name: "hive/plan1/schema"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, "hive/plan1/schema"); !errors.Is(err, branch.ErrAmbiguous) {
		t.Fatalf("ambiguous: %v", err)
	}
	if got := e.svc.List(branch.ListRequest{Repo: other}); len(got) != 1 || got[0].ID != "env-b" {
		t.Fatalf("by repository: %+v", got)
	}
}

func TestTrackingAgainUpdatesTheOwner(t *testing.T) {
	e := newEnv(t, branch.Config{})
	b := e.worktree("t", "tracked", nil)
	again, err := e.svc.Track(context.Background(), branch.TrackRequest{EnvironmentID: "t", Path: b.Path, Repo: e.repo, Name: "tracked", Owner: &branch.Owner{Agent: "p3", Kind: "claude"}})
	if err != nil || again.Owner == nil || again.Owner.Agent != "p3" || again.CreatedAt != b.CreatedAt {
		t.Fatalf("same branch, new owner: %+v %v", again, err)
	}
	if len(e.svc.List(branch.ListRequest{})) != 1 {
		t.Fatal("tracked once")
	}
}

func TestTouchMarksWorkStarted(t *testing.T) {
	e := newEnv(t, branch.Config{})
	e.worktree("tw", "touched", nil)
	e.svc.Touch("tw", true)
	if b := e.get("tw"); b.State != branch.StateWorking || !b.Started {
		t.Fatalf("an agent working there makes it working: %+v", b)
	}
	e.refresh()
	if b := e.get("tw"); b.State != branch.StateWorking {
		t.Fatalf("and it stays so with no changes: %+v", b)
	}
}

func TestKeepBoundsClosedBranches(t *testing.T) {
	e := newEnv(t, branch.Config{Keep: 2})
	ctx := context.Background()
	for _, n := range []string{"k1", "k2", "k3"} {
		e.worktree(n, n, nil)
		e.now = e.now.Add(1)
		if _, err := e.svc.Clean(ctx, branch.CleanRequest{Ref: n}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for _, b := range e.svc.List(branch.ListRequest{All: true}) {
		ids = append(ids, b.ID)
	}
	slices.Sort(ids)
	if strings.Join(ids, ",") != "k2,k3" {
		t.Fatalf("the oldest closed branch is forgotten: %v", ids)
	}
	if ev := e.events.of(branch.EventRemoved); len(ev) == 0 || !strings.Contains(ev[len(ev)-1], "k1") {
		t.Fatalf("removed event: %v", ev)
	}
}

func TestRunReadsChangedBranches(t *testing.T) {
	e := newEnv(t, branch.Config{Interval: 1 << 40, Debounce: 1})
	b := e.worktree("run", "running", nil)
	write(t, filepath.Join(b.Path, "f.txt"), "f\n")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	e.svc.Touch("run", false)
	waitFor(t, func() bool { return e.get("run").Files == 1 })
}

func TestStoreRefusesANewerFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "branches.json"), `{"schema": 99, "branches": []}`)
	s := branch.NewFilesystemStore(dir)
	if _, err := s.Load(); !errors.Is(err, branch.ErrNewer) {
		t.Fatalf("newer: %v", err)
	}
	if err := s.Save(nil); !errors.Is(err, branch.ErrNewer) {
		t.Fatalf("never written over: %v", err)
	}
	write(t, filepath.Join(dir, "branches.json"), `{not json`)
	s = branch.NewFilesystemStore(dir)
	if _, err := s.Load(); err == nil {
		t.Fatal("corrupt")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "branches.json.corrupt-*")); len(m) != 1 {
		t.Fatalf("moved aside: %v", m)
	}
}
