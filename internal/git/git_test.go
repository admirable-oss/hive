package git_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/git"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// repo creates a repository with one commit and returns its path.
func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.email", "hive@example.com")
	run(t, dir, "config", "user.name", "Hive Test")
	run(t, dir, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(dir, "README.md"), "hello\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatus(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()

	st, err := g.Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Root != dir || st.Branch != "main" || st.Head == "" || st.Dirty() {
		t.Fatalf("clean repo: %+v", st)
	}

	write(t, filepath.Join(dir, "README.md"), "changed\n")
	write(t, filepath.Join(dir, "new.txt"), "x\n")
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	write(t, filepath.Join(dir, "sub", "staged.txt"), "y\n")
	run(t, dir, "add", "sub/staged.txt")
	st, err = g.Status(ctx, filepath.Join(dir, "sub")) // from a subdirectory
	if err != nil {
		t.Fatal(err)
	}
	if st.Root != dir || st.Modified != 1 || st.Untracked != 1 || st.Staged != 1 || !st.Dirty() {
		t.Fatalf("dirty repo: %+v", st)
	}

	run(t, dir, "checkout", "-q", "--detach")
	st, _ = g.Status(ctx, dir)
	if !st.Detached || st.Branch != "" {
		t.Fatalf("detached: %+v", st)
	}

	if _, err := g.Status(ctx, t.TempDir()); !errors.Is(err, git.ErrNotRepository) {
		t.Fatalf("plain directory: %v", err)
	}
}

func TestParseStatusUpstream(t *testing.T) {
	g := &git.Git{Runner: fakeRunner(func(args []string) string {
		if args[0] == "rev-parse" {
			return "/r\n"
		}
		return "# branch.oid 0123456789abcdef0123\n# branch.head feature/x\n# branch.upstream origin/feature/x\n# branch.ab +3 -2\n1 .M N... 100644 100644 100644 a b file\nu UU N... 1 2 3 4 a b c conflict\n? new\n"
	})}
	st, err := g.Status(context.Background(), "/r")
	if err != nil {
		t.Fatal(err)
	}
	want := git.Status{Root: "/r", Branch: "feature/x", Head: "0123456789ab", Upstream: "origin/feature/x", Ahead: 3, Behind: 2, Modified: 1, Conflicts: 1, Untracked: 1}
	if *st != want {
		t.Fatalf("got %+v\nwant %+v", *st, want)
	}
}

type fakeRunner func(args []string) string

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	return []byte(f(args)), nil
}

func TestWorktrees(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	wt := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-wt", git.Slug("feature/login"))

	if err := g.AddWorktree(ctx, dir, wt, "feature/login", ""); err != nil {
		t.Fatal(err)
	}
	list, err := g.Worktrees(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Main || list[1].Branch != "feature/login" {
		t.Fatalf("worktrees: %+v", list)
	}
	if st, _ := g.Status(ctx, wt); st.Branch != "feature/login" {
		t.Fatalf("worktree branch: %+v", st)
	}

	// An existing branch is checked out, not recreated.
	run(t, dir, "branch", "existing")
	wt2 := wt + "-2"
	if err := g.AddWorktree(ctx, dir, wt2, "existing", ""); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(wt, "dirty.txt"), "x")
	if err := g.RemoveWorktree(ctx, dir, wt, false); !errors.Is(err, git.ErrDirty) {
		t.Fatalf("removing a dirty worktree: %v", err)
	}
	if err := g.RemoveWorktree(ctx, dir, wt, true); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveWorktree(ctx, dir, wt2, false); err != nil {
		t.Fatal(err)
	}
	if list, _ := g.Worktrees(ctx, dir); len(list) != 1 {
		t.Fatalf("after removal: %+v", list)
	}
	if err := g.AddWorktree(ctx, dir, wt, "-bad", ""); err == nil {
		t.Fatal("branch names starting with - must be refused")
	}
}

func TestSlugAndValidateBranch(t *testing.T) {
	for in, want := range map[string]string{"feature/login": "feature-login", "fix bug#1": "fix-bug-1", "v1.2": "v1.2"} {
		if got := git.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "-x", "a..b", "a b", "x.lock", "a/", "@{u}"} {
		if git.ValidateBranch(bad) == nil {
			t.Errorf("ValidateBranch(%q) should fail", bad)
		}
	}
}

func TestWatcherNoticesBranchChangesQuickly(t *testing.T) {
	dir := repo(t)
	var (
		mu      sync.Mutex
		changes []string
	)
	w, err := git.NewWatcher(git.New(), func(d string, st *git.Status) {
		mu.Lock()
		defer mu.Unlock()
		if st != nil {
			changes = append(changes, st.Branch)
		}
	}, git.WatcherOptions{Interval: time.Hour, Debounce: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	if st := w.Watch(ctx, dir); st == nil || st.Branch != "main" {
		t.Fatalf("initial status %+v", st)
	}
	run(t, dir, "checkout", "-q", "-b", "agent/work")
	// The periodic refresh is an hour away: only the filesystem can tell.
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := strings.Join(changes, ",")
		mu.Unlock()
		if strings.Contains(got, "agent/work") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not notice the branch change (changes: %q)", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := w.Status(dir); st.Branch != "agent/work" {
		t.Fatalf("cached status %+v", st)
	}
	w.Unwatch(dir)
	if w.Status(dir) != nil {
		t.Fatal("an unwatched directory has no status")
	}
}
