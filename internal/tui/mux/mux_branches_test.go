//go:build !windows

package mux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/pane"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=hive@example.com", "-c", "user.name=Hive", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestBranchesShowAndReview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := newHarness(t, 150, 40, Options{})
	ctx := context.Background()
	gitIn(t, h.dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(h.dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, h.dir, "add", ".")
	gitIn(t, h.dir, "commit", "-q", "-m", "init")

	env, err := h.api.WorktreeCreate(ctx, client.WorktreeCreateRequest{Repo: h.dir, Branch: "feature/review"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: env.ID, Name: "work"}); err != nil {
		t.Fatal(err)
	}
	h.waitText(env.ID + " created")

	if err := os.WriteFile(filepath.Join(env.Path, "README.md"), []byte("hello\nreviewed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NewBranches(h.c).Check(ctx, env.ID, ""); err != nil {
		t.Fatal(err)
	}
	h.waitText(env.ID + " working")

	h.press("ctrl+b", ")") // to the worktree's environment
	h.waitFor("the worktree's environment", func() bool { return h.a.ws.envID == env.ID })
	h.press("ctrl+b", "D")
	h.waitFor("the diff in the review", func() bool {
		s := h.screen()
		return strings.Contains(s, "Review · feature/review") && strings.Contains(s, "+reviewed") && strings.Contains(s, "1 files against main")
	})

	// A comment on the added line.
	h.press("G", "c")
	h.typeText("Say more")
	h.press("enter")
	h.waitText("1 comments, 1 not sent")
	b, err := client.NewBranches(h.c).Get(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c := b.Comments[0]; c.Path != "README.md" || c.Line != 2 || c.Body != "Say more" {
		t.Fatalf("comment %+v", c)
	}
	h.press("esc")

	h.press("ctrl+b", "O")
	h.waitFor("the branch in the Overview", func() bool {
		s := h.screen()
		return strings.Contains(s, "BRANCHES") && strings.Contains(s, "feature/review") && strings.Contains(s, "1 files +1 -0")
	})
}
