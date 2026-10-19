package core

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// hiveBin is a hive built for these tests (TestMain).
var hiveBin string

func TestMain(m *testing.M) {
	os.Exit(func() int {
		flag.Parse()
		if testing.Short() {
			return m.Run()
		}
		if hiveBin = os.Getenv(EnvHive); hiveBin != "" {
			return m.Run() // CI builds it once for every package
		}
		dir, err := os.MkdirTemp("", "hivebin")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		hiveBin = filepath.Join(dir, "hive")
		if runtime.GOOS == "windows" {
			hiveBin += ".exe"
		}
		// From the repository's own module: the desktop module does not
		// carry the CLI's dependencies.
		build := exec.Command("go", "build", "-o", hiveBin, "./cmd/hive")
		build.Dir = filepath.Join("..", "..", "..")
		out, err := build.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build hive: %v\n%s", err, out)
			return 1
		}
		return m.Run()
	}())
}

// recorder keeps what the hub emits.
type recorder struct {
	mu     sync.Mutex
	events []recorded
}

type recorded struct {
	name string
	data any
}

func (r *recorder) Emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recorded{name, data})
}

// screen is pane's text as the frontend would draw it from the updates.
func (r *recorder) screen(pane string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := map[int]string{}
	for _, e := range r.events {
		u, ok := e.data.(ScreenUpdate)
		if !ok || u.Pane != pane {
			continue
		}
		if u.Full {
			lines = map[int]string{}
		}
		for _, l := range u.Lines {
			var b strings.Builder
			for _, run := range l.Runs {
				b.WriteString(run.Text)
			}
			lines[l.Y] = b.String()
		}
	}
	var b strings.Builder
	for y := range 200 {
		b.WriteString(lines[y] + "\n")
	}
	return b.String()
}

func (r *recorder) last(name string) any {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].name == name {
			return r.events[i].data
		}
	}
	return nil
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// testHome is a HIVE_HOME with a short path (sockets must fit macOS's
// 104 bytes) whose daemon is stopped when the test ends.
func testHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIVE_CONFIG", filepath.Join(home, "config.toml"))
	t.Setenv("HIVE_SOCKET_PATH", "")
	t.Setenv("HIVE_SESSION", "")
	t.Cleanup(func() {
		cmd := exec.Command(hiveBin, "stop")
		cmd.Env = append(os.Environ(), "HIVE_HOME="+home)
		_ = cmd.Run()
		_ = os.RemoveAll(home)
	})
	return home
}

// TestHubDrivesTheDaemon: the hub starts the daemon when none runs, shows
// the workspace, streams a pane's screen, types into it, splits and closes
// panes; agents' processes stay with the daemon when the hub closes.
func TestHubDrivesTheDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a daemon")
	}
	if runtime.GOOS == "windows" {
		t.Skip("types POSIX shell commands")
	}
	home := testHome(t)
	rec := &recorder{}
	h := New(Config{Home: home, Hive: hiveBin, Emitter: rec})
	h.Start()
	defer h.Close()
	waitFor(t, "a connection", 30*time.Second, func() bool { return h.Connection().State == StateConnected })
	if c := h.Connection(); c.Version == "" || c.APILevel == 0 || c.Session != "default" {
		t.Fatalf("connection %+v", c)
	}

	dir := t.TempDir()
	if err := h.CreateEnvironment("work", dir); err != nil {
		t.Fatal(err)
	}
	pane, err := h.NewTab("work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pane in the workspace", 10*time.Second, func() bool {
		_, ok := paneByID(h.Workspace(), pane)
		return ok
	})
	// The app sizes the tab to its window; the pane follows.
	p, _ := paneByID(h.Workspace(), pane)
	if err := h.ResizeTab(p.TabID, 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := h.OpenView("w1", pane, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first screen at the tab's size", 10*time.Second, func() bool {
		u, ok := rec.last(EventScreen).(ScreenUpdate)
		return ok && u.Pane == pane && u.Cols == 80 && u.Rows == 24
	})
	// Typing waits until the shell is up: its prompt is on the screen.
	waitFor(t, "the shell's prompt", 15*time.Second, func() bool { return strings.TrimSpace(rec.screen(pane)) != "" })
	if err := h.Input(pane, "echo desk$((40+2))top\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the command's output", 15*time.Second, func() bool { return strings.Contains(rec.screen(pane), "desk42top") })

	// A paste arrives as text (bracketed when the shell asks: then its
	// newlines do not run it, Enter does).
	if err := h.Paste(pane, "echo pasted-$((1+1))"); err != nil {
		t.Fatal(err)
	}
	if err := h.Input(pane, "\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the paste's output", 15*time.Second, func() bool { return strings.Contains(rec.screen(pane), "pasted-2") })

	// Layout: split, then close the new half.
	other, err := h.SplitPane(pane, "right")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two panes with rects", 10*time.Second, func() bool {
		a, ok1 := paneByID(h.Workspace(), pane)
		b, ok2 := paneByID(h.Workspace(), other)
		return ok1 && ok2 && a.Rect != nil && b.Rect != nil && b.Rect.X > a.Rect.X
	})
	if err := h.ClosePane(other); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pane closed", 10*time.Second, func() bool {
		_, ok := paneByID(h.Workspace(), other)
		return !ok
	})

	// The view model is what the frontend gets: JSON, without secrets.
	data, err := json.Marshal(rec.last(EventWorkspace))
	if err != nil || !strings.Contains(string(data), `"environment_id":"work"`) {
		t.Fatalf("workspace event %s %v", data, err)
	}

	// Closing the app leaves the daemon and the shell running.
	h.Close()
	h2 := New(Config{Home: home, Hive: hiveBin, Emitter: &recorder{}})
	h2.Start()
	defer h2.Close()
	// Connected comes before the first workspace, so wait for the pane.
	waitFor(t, "the pane, running, on a second connection", 30*time.Second, func() bool {
		p, ok := paneByID(h2.Workspace(), pane)
		return h2.Connection().State == StateConnected && ok && p.Running
	})
}

// TestHubReconnects: when the daemon goes away the hub says so, and comes
// back to the same panes (starting the daemon again).
func TestHubReconnects(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a daemon")
	}
	if runtime.GOOS == "windows" {
		t.Skip("types POSIX shell commands")
	}
	home := testHome(t)
	rec := &recorder{}
	h := New(Config{Home: home, Hive: hiveBin, Emitter: rec})
	h.Start()
	defer h.Close()
	waitFor(t, "a connection", 30*time.Second, func() bool { return h.Connection().State == StateConnected })
	if err := h.CreateEnvironment("work", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pane, err := h.NewTab("work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pane", 10*time.Second, func() bool { _, ok := paneByID(h.Workspace(), pane); return ok })
	if err := h.OpenView("w1", pane, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shell's prompt", 15*time.Second, func() bool { return strings.TrimSpace(rec.screen(pane)) != "" })

	cmd := exec.Command(hiveBin, "daemon", "restart")
	cmd.Env = append(os.Environ(), "HIVE_HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restart: %v: %s", err, out)
	}
	waitFor(t, "connected again", 30*time.Second, func() bool {
		_, ok := paneByID(h.Workspace(), pane)
		return h.Connection().State == StateConnected && ok
	})
	if err := h.Input(pane, "echo back-$((2*3))\r"); err != nil {
		waitFor(t, "the view reopened", 10*time.Second, func() bool { return h.Input(pane, "echo back-$((2*3))\r") == nil })
	}
	waitFor(t, "output after the restart", 15*time.Second, func() bool { return strings.Contains(rec.screen(pane), "back-6") })
}
