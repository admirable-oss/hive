//go:build !windows

package mux

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/runtime"
)

// TestMain is also the plugin sandbox's launcher for the daemons these
// tests run, as the hive binary is for the real one.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == plugin.SandboxCommand {
		os.Exit(plugin.SandboxMain(os.Args[2:]))
	}
	goleak.VerifyTestMain(m)
}

var update = flag.Bool("update", false, "rewrite golden files")

// startDaemon runs a daemon whose shell is cat, so a pane shows exactly
// what is typed into it.
func startDaemon(t testing.TB) client.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return startDaemonIn(t, dir)
}

// startDaemonIn runs a daemon whose socket and data are in dir (its
// plugins in dir/plugins).
func startDaemonIn(t testing.TB, dir string) client.Client {
	t.Helper()
	mod := runtime.NewModule(runtime.Config{
		SocketPath: filepath.Join(dir, "hive.sock"),
		Shell:      []string{"/bin/cat"},
		InheritEnv: true,
	})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := mod.Service.Stop(ctx); err != nil {
			t.Errorf("stop daemon: %v", err)
		}
	})
	c := client.NewService(client.Config{SocketPath: filepath.Join(dir, "hive.sock")})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// harness is an App against a real daemon, with one environment ("api")
// holding one tab ("main") of one pane.
type harness struct {
	t    testing.TB
	c    client.Client
	api  client.Workspace
	a    *App
	dir  string
	w, h int
}

func newHarness(t testing.TB, w, h int, opts Options) *harness {
	t.Helper()
	c := startDaemon(t)
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: "api", Root: dir}); err != nil {
		t.Fatal(err)
	}
	api := client.NewWorkspace(c)
	if _, err := api.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "api", Name: "main"}); err != nil {
		t.Fatal(err)
	}
	return startApp(t, c, w, h, dir, opts)
}

func startApp(t testing.TB, c client.Client, w, h int, dir string, opts Options) *harness {
	t.Helper()
	if opts.Clipboard == "" {
		opts.Clipboard = ClipboardOSC52 // never run pbcopy from tests
	}
	if opts.Cwd == "" {
		opts.Cwd = dir
	}
	a := New(context.Background(), c, opts)
	t.Cleanup(a.Close)
	a.HandleEvent(uv.WindowSizeEvent{Width: w, Height: h})
	a.Start()
	hs := &harness{t: t, c: c, api: client.NewWorkspace(c), a: a, dir: dir, w: w, h: h}
	hs.waitFor("the workspace", func() bool { return a.ws.snap != nil })
	if a.tab() != nil {
		hs.waitSized()
	}
	return hs
}

// settle lets background work finish and screens catch up.
func (h *harness) settle() {
	h.a.Settle(2 * time.Second)
	select {
	case <-h.a.Wake():
	default:
	}
}

// waitFor polls cond, letting the app work, for up to five seconds.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.a.Settle(50 * time.Millisecond)
		select {
		case <-h.a.Wake():
		default:
		}
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s; screen:\n%s", what, h.screen())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitSized waits until the tab on screen has this client's size.
func (h *harness) waitSized() {
	h.t.Helper()
	h.waitFor("the tab to take the screen's size", func() bool {
		t, want := h.a.tab(), h.a.paneArea()
		return t != nil && t.Width == want.w && t.Height == want.h
	})
}

// waitText waits until the screen shows s.
func (h *harness) waitText(s string) {
	h.t.Helper()
	h.waitFor(strconv.Quote(s), func() bool { return strings.Contains(h.screen(), s) })
}

// screen draws the app and returns it as text, trailing blanks trimmed,
// plus the cursor position.
func (h *harness) screen() string {
	buf := uv.NewScreenBuffer(h.w, h.h)
	res := h.a.Draw(buf)
	var b strings.Builder
	for y := 0; y < h.h; y++ {
		var line strings.Builder
		for x := 0; x < h.w; {
			c := buf.CellAt(x, y)
			if c == nil || c.Width == 0 {
				x++
				continue
			}
			content := c.Content
			if content == "" {
				content = " "
			}
			line.WriteString(content)
			x += max(c.Width, 1)
		}
		b.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}
	if c := res.Cursor; c != nil && !c.Hidden {
		b.WriteString("cursor: " + strconv.Itoa(c.X) + "," + strconv.Itoa(c.Y) + "\n")
	}
	return b.String()
}

func (h *harness) golden(name string) {
	h.t.Helper()
	// A pane's first screen arrives on its own stream, after the layout.
	h.waitFor("every pane's screen", func() bool { return !strings.Contains(h.screen(), "starting…") })
	got := h.screen()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("%v (go test -update creates it)", err)
	}
	if string(want) != got {
		h.t.Errorf("screen differs from %s (go test -update rewrites it)\n--- got ---\n%s--- want ---\n%s", path, got, want)
	}
}

// press sends keys written as in key bindings: "ctrl+b", "%", "enter".
func (h *harness) press(keys ...string) {
	for _, k := range keys {
		h.a.HandleEvent(uv.KeyPressEvent(key(k)))
		h.settle()
	}
}

// typeText types every rune of s.
func (h *harness) typeText(s string) {
	for _, r := range s {
		if r == '\r' || r == '\n' {
			h.press("enter")
			continue
		}
		h.press(string(r))
	}
}

// typeLines types each line and Enter into cat, waiting for its echo and
// output before the next, as a person would; typing ahead would interleave
// the terminal's echo with cat's output.
func (h *harness) typeLines(lines ...string) {
	h.t.Helper()
	for _, l := range lines {
		before := strings.Count(h.screen(), l)
		h.typeText(l + "\r")
		h.waitFor(strconv.Quote(l)+" echoed", func() bool { return strings.Count(h.screen(), l) >= before+2 })
	}
}

// click presses and releases the left button at a screen cell.
func (h *harness) click(x, y int, mod uv.KeyMod) {
	m := uv.Mouse{X: x, Y: y, Button: uv.MouseLeft, Mod: mod}
	h.a.HandleEvent(uv.MouseClickEvent(m))
	h.a.HandleEvent(uv.MouseReleaseEvent(m))
	h.settle()
}

// drag presses at one cell, moves through to another and releases.
func (h *harness) drag(x1, y1, x2, y2 int) {
	h.a.HandleEvent(uv.MouseClickEvent(uv.Mouse{X: x1, Y: y1, Button: uv.MouseLeft}))
	h.a.HandleEvent(uv.MouseMotionEvent(uv.Mouse{X: (x1 + x2) / 2, Y: (y1 + y2) / 2, Button: uv.MouseLeft}))
	h.a.HandleEvent(uv.MouseMotionEvent(uv.Mouse{X: x2, Y: y2, Button: uv.MouseLeft}))
	h.a.HandleEvent(uv.MouseReleaseEvent(uv.Mouse{X: x2, Y: y2, Button: uv.MouseLeft}))
	h.settle()
}

// panes returns the tab's panes in layout order.
func (h *harness) panes() []string { return h.a.paneOrder() }

// focused is the focused pane's ID.
func (h *harness) focused() string {
	if t := h.a.tab(); t != nil {
		return t.Focused
	}
	return ""
}
