package pane_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
)

// isIn checks a pane's shell is in dir. The shell compares, as temporary
// paths are wider than a pane and would wrap if printed.
func (w *world) isIn(t *testing.T, paneID, dir, what string) {
	t.Helper()
	line := w.runAndWait(t, paneID, `[ "$(pwd -P)" = '`+dir+`' ] && echo "cwd:same" || echo "cwd:other:$(basename "$(pwd -P)")"`, `^cwd:`)
	if line != "cwd:same" {
		t.Fatalf("%s: %s, want %s", what, line, filepath.Base(dir))
	}
}

func TestNewPanesStartWhereTheirPaneIsWorking(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("reading a process's directory is not supported here")
	}
	w := newWorld(t, t.TempDir())
	w.env(t, "dev")
	ctx := context.Background()
	env, _ := w.envs.Get(ctx, "dev")
	root, _ := filepath.EvalSymlinks(env.Path)
	sub := filepath.Join(root, "src", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	_, first, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	w.isIn(t, first.ID, root, "a tab's first pane starts at the environment's root")
	w.runAndWait(t, first.ID, `cd src/api && echo moved`, `^moved`)

	split, err := w.panes.Split(ctx, pane.SplitRequest{Pane: first.ID, Direction: layout.Right})
	if err != nil {
		t.Fatal(err)
	}
	w.isIn(t, split.ID, sub, "a split starts where its pane is working")

	popup, err := w.panes.Popup(ctx, pane.PopupRequest{TabID: first.TabID})
	if err != nil {
		t.Fatal(err)
	}
	w.isIn(t, popup.ID, sub, "a popup starts where the focused pane is working")

	explicit, err := w.panes.Split(ctx, pane.SplitRequest{Pane: first.ID, Direction: layout.Down, Spec: pane.Spec{Cwd: "src"}})
	if err != nil {
		t.Fatal(err)
	}
	w.isIn(t, explicit.ID, filepath.Join(root, "src"), "an explicit directory wins")

	// The directory goes away under the shell: the next split falls back
	// to the environment's root instead of failing.
	gone := filepath.Join(root, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	w.runAndWait(t, split.ID, `cd `+gone+` && echo in-gone`, `^in-gone`)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	fallback, err := w.panes.Split(ctx, pane.SplitRequest{Pane: split.ID, Direction: layout.Down})
	if err != nil {
		t.Fatalf("a split from a pane whose directory was deleted: %v", err)
	}
	w.isIn(t, fallback.ID, root, "a deleted directory falls back to the environment's root")
}
