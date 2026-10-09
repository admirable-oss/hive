package pane_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type world struct {
	root  string
	envs  environment.Service
	procs process.Service
	panes *pane.Service
	store *pane.FilesystemStore
	terms terminal.Service
}

// newWorld wires real environments, processes and PTYs in one directory.
func newWorld(t *testing.T, root string) *world {
	t.Helper()
	envs := environment.NewService(environment.NewFilesystemStore(root + "/environments"))
	terms := terminal.NewService(terminal.PTYFactory{})
	procs := process.NewService(process.NewFilesystemStore(root+"/environments"), envs, process.NewExecRunner(0), terms)
	store := pane.NewFilesystemStore(root + "/environments")
	w := &world{root: root, envs: envs, procs: procs, terms: terms, store: store}
	w.panes = pane.NewService(pane.Config{Size: terminal.Size{Width: 81, Height: 24}, Shell: []string{"sh"}}, store, procs, terms, envs, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = procs.StopAll(ctx)
	})
	return w
}

func (w *world) env(t *testing.T, id string) {
	t.Helper()
	if _, err := w.envs.Create(context.Background(), environment.CreateRequest{ID: id}); err != nil {
		t.Fatal(err)
	}
}

// runAndWait runs cmd in a pane's shell and waits for a line matching pattern.
func (w *world) runAndWait(t *testing.T, paneID, cmd, pattern string) string {
	t.Helper()
	ctx := context.Background()
	done := make(chan struct{})
	var line string
	var err error
	go func() {
		defer close(done)
		line, err = w.panes.WaitOutput(ctx, paneID, terminal.WaitRequest{Pattern: pattern}, 5*time.Second)
	}()
	time.Sleep(30 * time.Millisecond)
	if rerr := w.panes.Run(ctx, paneID, cmd); rerr != nil {
		t.Fatal(rerr)
	}
	<-done
	if err != nil {
		lines, _ := w.panes.Read(ctx, paneID, terminal.ReadRequest{})
		t.Fatalf("waiting for %q after %q: %v\nscreen:\n%s", pattern, cmd, err, strings.Join(lines, "\n"))
	}
	return line
}

func TestTabsSplitsAndPaneSizes(t *testing.T) {
	w := newWorld(t, t.TempDir())
	w.env(t, "dev")
	ctx := context.Background()
	tab, first, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Name: "agents", Pane: pane.Spec{Name: "left"}})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Focused || first.Rect == nil || first.Rect.W != 81 {
		t.Fatalf("first pane = %+v", first)
	}
	second, err := w.panes.Split(ctx, pane.SplitRequest{Pane: first.ID, Direction: layout.Right, Spec: pane.Spec{Name: "right"}})
	if err != nil {
		t.Fatal(err)
	}
	// 81 columns: 40 + border + 40.
	if second.Rect.W != 40 || !second.Focused {
		t.Fatalf("second pane = %+v", second)
	}
	// Each pane's program sees its own size, and its identity.
	w.runAndWait(t, first.ID, "stty size", `^24 40$`)
	w.runAndWait(t, second.ID, "stty size", `^24 40$`)
	w.runAndWait(t, second.ID, `echo "id=$HIVE_PANE_ID tab=$HIVE_TAB_ID"`, `^id=`+second.ID+` tab=`+tab.ID+`$`)

	// Zoom gives one pane the whole tab and resizes its terminal.
	if _, err := w.panes.Zoom(ctx, first.ID, nil); err != nil {
		t.Fatal(err)
	}
	w.runAndWait(t, first.ID, "stty size", `^24 81$`)
	if p, _ := w.panes.Pane(ctx, second.ID); p.Rect != nil {
		t.Fatal("a pane hidden by zoom has no area")
	}
	if _, err := w.panes.Zoom(ctx, first.ID, nil); err != nil { // toggle back
		t.Fatal(err)
	}

	// Resizing moves the border; focus moves by direction.
	if _, err := w.panes.Resize(ctx, first.ID, layout.Right, 10); err != nil {
		t.Fatal(err)
	}
	w.runAndWait(t, first.ID, "stty size", `^24 50$`)
	if p, err := w.panes.FocusDirection(ctx, second.ID, layout.Left); err != nil || p.ID != first.ID {
		t.Fatalf("focus left: %+v, %v", p, err)
	}
	if err := w.panes.Swap(ctx, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := w.panes.Tab(ctx, tab.ID)
	if panes := got.Layout.Panes(); panes[0] != second.ID {
		t.Fatalf("after swap %v", panes)
	}
}

func TestClosingPanesAndTabs(t *testing.T) {
	w := newWorld(t, t.TempDir())
	w.env(t, "dev")
	ctx := context.Background()
	tab, a, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.panes.Split(ctx, pane.SplitRequest{TabID: tab.ID, Direction: layout.Down})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.panes.Close(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, w, b.ProcessID, process.StatusKilled)
	got, _ := w.panes.Tab(ctx, tab.ID)
	if got.Focused != a.ID || len(got.Layout.Panes()) != 1 {
		t.Fatalf("after closing b: %+v", got)
	}
	if err := w.panes.Close(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.panes.Tab(ctx, tab.ID); !errors.Is(err, pane.ErrTabNotFound) {
		t.Fatalf("closing the last pane closes the tab: %v", err)
	}
}

func waitStatus(t *testing.T, w *world, processID string, want process.Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := w.procs.Get(context.Background(), processID)
		if err == nil && p.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %s status %s, want %s", processID, p.Status, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMovingAPaneKeepsItsProcess(t *testing.T) {
	w := newWorld(t, t.TempDir())
	w.env(t, "a")
	w.env(t, "b")
	ctx := context.Background()
	src, mover, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	stay, err := w.panes.Split(ctx, pane.SplitRequest{Pane: mover.ID})
	if err != nil {
		t.Fatal(err)
	}
	dst, _, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	pid := mover.Process.PID

	moved, err := w.panes.Move(ctx, pane.MoveRequest{Pane: mover.ID, TabID: dst.ID, Direction: layout.Down})
	if err != nil {
		t.Fatal(err)
	}
	if moved.TabID != dst.ID || moved.EnvironmentID != "b" || moved.Process.PID != pid || moved.Process.EnvironmentID != "b" {
		t.Fatalf("moved pane = %+v (process %+v)", moved, moved.Process)
	}
	// It still runs and takes input, at its new size (24 rows split in two).
	w.runAndWait(t, mover.ID, "stty size", `^11 81$`)
	if got, _ := w.panes.Tab(ctx, src.ID); len(got.Layout.Panes()) != 1 || got.Layout.Panes()[0] != stay.ID {
		t.Fatalf("source tab after the move: %+v", got)
	}

	// Moving the last pane out of a tab closes that tab; moving to an
	// environment without a tab makes one.
	again, err := w.panes.Move(ctx, pane.MoveRequest{Pane: stay.ID, EnvironmentID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.panes.Tab(ctx, src.ID); !errors.Is(err, pane.ErrTabNotFound) {
		t.Fatalf("emptied tab should be closed: %v", err)
	}
	if again.TabID == dst.ID {
		t.Fatal("a move to an environment makes a new tab")
	}
}

func TestPopupClosesWithItsCommand(t *testing.T) {
	w := newWorld(t, t.TempDir())
	w.env(t, "dev")
	ctx := context.Background()
	tab, _, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	pop, err := w.panes.Popup(ctx, pane.PopupRequest{TabID: tab.ID, Spec: pane.Spec{Command: []string{"sh", "-c", "read x"}}, WidthPct: 50, HeightPct: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !pop.Popup || pop.Rect.W != 40 || pop.Rect.H != 12 || pop.Rect.X != 20 {
		t.Fatalf("popup = %+v rect %+v", pop, pop.Rect)
	}
	if _, err := w.panes.Split(ctx, pane.SplitRequest{Pane: pop.ID}); err == nil {
		t.Fatal("popups cannot be split")
	}
	if err := w.panes.Input(ctx, pop.ID, []byte("\n")); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, w, pop.ProcessID, process.StatusExited)
	w.panes.ProcessExited(ctx, pop.ProcessID)
	if _, err := w.panes.Pane(ctx, pop.ID); !errors.Is(err, pane.ErrPaneNotFound) {
		t.Fatalf("a finished popup closes: %v", err)
	}
}

func TestKeysTextAndReads(t *testing.T) {
	w := newWorld(t, t.TempDir())
	w.env(t, "dev")
	ctx := context.Background()
	_, p, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Pane: pane.Spec{
		Command: []string{"sh", "-c", `printf 'name? '; read n; echo "hello $n"; read _`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, w, p.ID, "name?")
	if err := w.panes.SendText(ctx, p.ID, "hive", false); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		line, _ := w.panes.WaitOutput(ctx, p.ID, terminal.WaitRequest{Pattern: "^hello "}, 5*time.Second)
		done <- line
	}()
	time.Sleep(30 * time.Millisecond)
	if err := w.panes.SendKeys(ctx, p.ID, []string{"Enter"}); err != nil {
		t.Fatal(err)
	}
	if got := <-done; got != "hello hive" {
		t.Fatalf("matched %q", got)
	}
	if err := w.panes.SendKeys(ctx, p.ID, []string{"Nope"}); !errors.Is(err, pane.ErrInvalid) {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := w.panes.WaitOutput(ctx, p.ID, terminal.WaitRequest{Pattern: "never"}, 100*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
}

func waitScreen(t *testing.T, w *world, paneID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, _ := w.panes.Read(context.Background(), paneID, terminal.ReadRequest{})
		if strings.Contains(strings.Join(lines, "\n"), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never showed %q:\n%s", want, strings.Join(lines, "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStateSurvivesARestartOfTheService(t *testing.T) {
	root := t.TempDir()
	w := newWorld(t, root)
	w.env(t, "dev")
	ctx := context.Background()
	tab, a, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.panes.Split(ctx, pane.SplitRequest{Pane: a.ID, Ratio: 0.3, Spec: pane.Spec{Name: "logs"}})
	if err != nil {
		t.Fatal(err)
	}
	// A fresh service over the same store (what a new daemon does).
	again := pane.NewService(pane.Config{Size: terminal.Size{Width: 81, Height: 24}}, w.store, w.procs, w.terms, w.envs, nil)
	got, err := again.Tab(ctx, tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "work" || got.Focused != b.ID || len(got.Layout.Panes()) != 2 {
		t.Fatalf("reloaded tab = %+v", got)
	}
	if p, err := again.Pane(ctx, b.ID); err != nil || p.Name != "logs" || p.Process == nil {
		t.Fatalf("reloaded pane = %+v, %v", p, err)
	}
}

func TestApplyAndExportRoundTrip(t *testing.T) {
	project := t.TempDir()
	_ = os.Mkdir(project+"/web", 0o755)
	spec := pane.LayoutSpec{Version: 1, Environments: []pane.EnvironmentSpec{
		{ID: "api", Root: project, Env: map[string]string{"PORT": "8080"}, Tabs: []pane.TabSpec{
			{Name: "agents", Layout: &pane.NodeSpec{
				Split: layout.Horizontal, Ratio: 0.6,
				First: &pane.NodeSpec{Pane: &pane.Spec{Name: "claude", Command: []string{"sh", "-c", "read x"}}},
				Second: &pane.NodeSpec{
					Split: layout.Vertical, Ratio: 0.5,
					First:  &pane.NodeSpec{Pane: &pane.Spec{Name: "web", Cwd: "web", Command: []string{"sh", "-c", "read x"}}},
					Second: &pane.NodeSpec{Pane: &pane.Spec{Name: "shell", Env: map[string]string{"DEBUG": "1"}}},
				},
			}},
			{Name: "solo", Layout: &pane.NodeSpec{Pane: &pane.Spec{Name: "one"}}},
		}},
		{ID: "scratch", Tabs: []pane.TabSpec{{Name: "main", Layout: &pane.NodeSpec{Pane: &pane.Spec{}}}}},
	}}
	ctx := context.Background()

	w := newWorld(t, t.TempDir())
	res, err := w.panes.Apply(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tabs) != 3 || res.Panes != 5 || len(res.CreatedEnvironments) != 2 {
		t.Fatalf("apply result = %+v", res)
	}
	exported, err := w.panes.Export(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := mustJSON(t, spec), mustJSON(t, exported); a != b {
		t.Fatalf("export differs from the applied spec:\napplied:  %s\nexported: %s", a, b)
	}

	// Applying the export in a fresh place reproduces it exactly.
	w2 := newWorld(t, t.TempDir())
	if _, err := w2.panes.Apply(ctx, exported); err != nil {
		t.Fatal(err)
	}
	again, _ := w2.panes.Export(ctx, nil)
	if a, b := mustJSON(t, exported), mustJSON(t, again); a != b {
		t.Fatalf("second round trip differs:\n%s\n%s", a, b)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyValidatesAndRollsBack(t *testing.T) {
	w := newWorld(t, t.TempDir())
	ctx := context.Background()
	bad := []pane.LayoutSpec{
		{Version: 2},
		{Version: 1, Environments: []pane.EnvironmentSpec{{ID: "../x"}}},
		{Version: 1, Environments: []pane.EnvironmentSpec{{ID: "e", Root: "relative"}}},
		{Version: 1, Environments: []pane.EnvironmentSpec{{ID: "e", Tabs: []pane.TabSpec{{Layout: &pane.NodeSpec{Split: "diagonal", First: &pane.NodeSpec{Pane: &pane.Spec{}}, Second: &pane.NodeSpec{Pane: &pane.Spec{}}}}}}}},
		{Version: 1, Environments: []pane.EnvironmentSpec{{ID: "e", Tabs: []pane.TabSpec{{Layout: &pane.NodeSpec{Split: layout.Horizontal, First: &pane.NodeSpec{Pane: &pane.Spec{}}}}}}}},
	}
	for i, sp := range bad {
		if _, err := w.panes.Apply(ctx, sp); !errors.Is(err, pane.ErrInvalid) {
			t.Errorf("spec %d: got %v, want ErrInvalid", i, err)
		}
	}

	// A pane that cannot start rolls back the tabs this apply made.
	w.env(t, "e")
	failing := pane.LayoutSpec{Version: 1, Environments: []pane.EnvironmentSpec{{ID: "e", Tabs: []pane.TabSpec{
		{Name: "ok", Layout: &pane.NodeSpec{Pane: &pane.Spec{Command: []string{"sh", "-c", "read x"}}}},
		{Name: "broken", Layout: &pane.NodeSpec{Pane: &pane.Spec{Cwd: "does/not/exist"}}},
	}}}}
	if _, err := w.panes.Apply(ctx, failing); err == nil {
		t.Fatal("expected the broken pane to fail the apply")
	}
	if tabs, _ := w.panes.Tabs(ctx, "e"); len(tabs) != 0 {
		t.Fatalf("rollback left %d tabs", len(tabs))
	}
}

// TestRandomOperationsKeepStateConsistent drives random API calls and checks
// the stored state stays coherent: every pane is in exactly one tab's
// layout or popups, and focus points at a pane of the tab.
func TestRandomOperationsKeepStateConsistent(t *testing.T) {
	w := newWorld(t, t.TempDir())
	w.env(t, "a")
	w.env(t, "b")
	ctx := context.Background()
	cmd := pane.Spec{Command: []string{"sh", "-c", "read x"}}
	if _, _, err := w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "a", Pane: cmd}); err != nil {
		t.Fatal(err)
	}
	dirs := []layout.Direction{layout.Left, layout.Right, layout.Up, layout.Down}
	for i := range 40 {
		panes, _ := w.panes.Panes(ctx, "", "")
		var err error
		if len(panes) == 0 {
			_, _, err = w.panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "a", Pane: cmd})
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		p := panes[i%len(panes)]
		switch i % 6 {
		case 0, 1:
			_, err = w.panes.Split(ctx, pane.SplitRequest{Pane: p.ID, Direction: dirs[i%4], Spec: cmd})
		case 2:
			err = w.panes.Close(ctx, p.ID)
		case 3:
			_, err = w.panes.Move(ctx, pane.MoveRequest{Pane: p.ID, EnvironmentID: []string{"a", "b"}[i%2]})
		case 4:
			_, err = w.panes.Zoom(ctx, p.ID, nil)
			if errors.Is(err, pane.ErrInvalid) {
				err = nil
			}
		case 5:
			_, err = w.panes.Resize(ctx, p.ID, dirs[i%4], 3)
			if errors.Is(err, layout.ErrNoBorder) {
				err = nil
			}
		}
		if err != nil {
			t.Fatalf("step %d (%d): %v", i, i%6, err)
		}
		checkConsistent(t, w)
	}
}

func checkConsistent(t *testing.T, w *world) {
	t.Helper()
	ctx := context.Background()
	for _, env := range []string{"a", "b"} {
		st, err := w.store.Load(ctx, env)
		if err != nil {
			t.Fatal(err)
		}
		where := map[string]string{}
		for _, tab := range st.Tabs {
			ids := tab.Layout.Panes()
			for _, pp := range tab.Popups {
				ids = append(ids, pp.Pane)
			}
			if len(ids) == 0 {
				t.Fatalf("env %s: empty tab %s kept", env, tab.ID)
			}
			if tab.Focused != "" && !contains(ids, tab.Focused) {
				t.Fatalf("env %s: tab %s focuses a pane it does not have", env, tab.ID)
			}
			for _, id := range ids {
				if prev, dup := where[id]; dup {
					t.Fatalf("pane %s in tabs %s and %s", id, prev, tab.ID)
				}
				where[id] = tab.ID
			}
		}
		for _, p := range st.Panes {
			if where[p.ID] != p.TabID || p.EnvironmentID != env {
				t.Fatalf("pane %s record (tab %s env %s) disagrees with layouts (tab %s env %s)", p.ID, p.TabID, p.EnvironmentID, where[p.ID], env)
			}
			delete(where, p.ID)
		}
		if len(where) != 0 {
			t.Fatalf("layouts reference unknown panes: %v", where)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
