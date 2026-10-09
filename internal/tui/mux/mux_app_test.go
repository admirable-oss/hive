package mux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
)

// --- terminal mode ---

func TestTypingReachesTheFocusedPane(t *testing.T) {
	h := newHarness(t, 80, 14, Options{})
	h.typeLines("hello")
	h.golden("terminal")
}

func TestPasteIsTypedIntoThePane(t *testing.T) {
	h := newHarness(t, 60, 10, Options{HideSidebar: true})
	h.a.HandleEvent(uv.PasteEvent{Content: "pasted\nline"})
	h.waitFor("the paste", func() bool { return strings.Count(h.screen(), "pasted") == 2 })
}

func TestTheTabTakesTheScreensSize(t *testing.T) {
	h := newHarness(t, 90, 20, Options{})
	h.a.HandleEvent(uv.WindowSizeEvent{Width: 110, Height: 30})
	h.w, h.h = 110, 30
	h.waitSized()
	if got := h.a.tab(); got.Width != 110-DefaultSidebarWidth-1 || got.Height != 28 {
		t.Fatalf("tab size %dx%d", got.Width, got.Height)
	}
}

// --- prefix, navigate and resize modes ---

func TestPrefixSplitsAndMovesFocus(t *testing.T) {
	h := newHarness(t, 100, 18, Options{HideSidebar: true})
	first := h.focused()

	h.press("ctrl+b")
	if h.a.ui.mode != keymap.ModePrefix {
		t.Fatalf("mode %s after the prefix", h.a.ui.mode)
	}
	h.golden("prefix")

	h.press("%")
	h.waitFor("a split", func() bool { return len(h.panes()) == 2 && h.focused() == h.panes()[1] })
	if h.a.ui.mode != keymap.ModeTerminal {
		t.Fatalf("one key after the prefix returns to terminal mode, got %s", h.a.ui.mode)
	}
	h.press("ctrl+b", `"`)
	h.waitFor("a second split", func() bool { return len(h.panes()) == 3 })
	h.press("ctrl+b", "h")
	h.waitFor("focus on the left pane", func() bool { return h.focused() == first })
	h.waitSized()
	h.golden("splits")
}

func TestPrefixTwiceSendsThePrefix(t *testing.T) {
	h := newHarness(t, 60, 10, Options{HideSidebar: true})
	h.press("ctrl+b", "ctrl+b")
	if h.a.ui.mode != keymap.ModeTerminal {
		t.Fatalf("mode %s", h.a.ui.mode)
	}
	h.waitText("^B") // cat's terminal echoes the control character
}

func TestNavigateModeIsSticky(t *testing.T) {
	h := newHarness(t, 100, 14, Options{HideSidebar: true})
	h.press("ctrl+b", "%")
	h.waitFor("a split", func() bool { return len(h.panes()) == 2 })
	left, right := h.panes()[0], h.panes()[1]

	h.press("ctrl+b", "space")
	h.golden("navigate")
	h.press("h")
	h.waitFor("focus left", func() bool { return h.focused() == left })
	h.press("l")
	h.waitFor("focus right", func() bool { return h.focused() == right })
	if h.a.ui.mode != keymap.ModeNavigate {
		t.Fatalf("navigate mode ended after moving: %s", h.a.ui.mode)
	}
	h.press("m") // unbound keys do nothing, and are not typed
	h.press("esc")
	if h.a.ui.mode != keymap.ModeTerminal {
		t.Fatalf("esc leaves navigate mode: %s", h.a.ui.mode)
	}
}

func TestResizeModeMovesTheBorder(t *testing.T) {
	h := newHarness(t, 100, 14, Options{HideSidebar: true})
	h.press("ctrl+b", "%")
	h.waitFor("a split", func() bool { return len(h.panes()) == 2 })
	left := h.panes()[0]
	before := h.a.geometry()[left].W

	h.press("ctrl+b", "h", "ctrl+b", "r")
	h.golden("resize")
	h.press("l", "l", "l")
	h.waitFor("a wider left pane", func() bool { return h.a.geometry()[left].W >= before+5 })
	h.press("esc")
}

func TestZoomFillsTheTab(t *testing.T) {
	h := newHarness(t, 80, 12, Options{HideSidebar: true})
	h.press("ctrl+b", "%")
	h.waitFor("a split", func() bool { return len(h.panes()) == 2 })
	h.press("ctrl+b", "z")
	h.waitFor("zoom", func() bool { return h.a.tab().Zoomed == h.focused() })
	h.golden("zoom")
	h.press("ctrl+b", "z")
	h.waitFor("unzoom", func() bool { return h.a.tab().Zoomed == "" })
}

// --- tabs and environments ---

func TestTabsCreateAndSwitch(t *testing.T) {
	h := newHarness(t, 80, 10, Options{HideSidebar: true})
	first := h.a.tab().ID
	h.press("ctrl+b", "c")
	h.waitFor("a second tab", func() bool { return len(h.a.envTabs()) == 2 && h.a.tab().ID != first })
	h.press("ctrl+b", "1")
	h.waitFor("the first tab", func() bool { return h.a.tab().ID == first })
	h.press("ctrl+b", "n")
	h.waitFor("the next tab", func() bool { return h.a.tab().ID != first })
	h.press("ctrl+b", "p")
	h.waitFor("back again", func() bool { return h.a.tab().ID == first })
}

func TestRenameTabPrompt(t *testing.T) {
	h := newHarness(t, 80, 12, Options{HideSidebar: true})
	h.press("ctrl+b", ",")
	if _, ok := h.a.ui.overlay.(*prompt); !ok {
		t.Fatalf("overlay %T", h.a.ui.overlay)
	}
	h.press("ctrl+u")
	h.typeText("build")
	h.golden("prompt")
	h.press("enter")
	h.waitFor("the new name", func() bool { return h.a.tab().Name == "build" })
}

func TestClosePaneAsksFirst(t *testing.T) {
	h := newHarness(t, 80, 12, Options{HideSidebar: true})
	h.press("ctrl+b", "%")
	h.waitFor("a split", func() bool { return len(h.panes()) == 2 })
	h.press("ctrl+b", "x")
	h.golden("confirm")
	h.press("n")
	if len(h.panes()) != 2 || h.a.ui.overlay != nil {
		t.Fatal("n keeps the pane")
	}
	h.press("ctrl+b", "x", "y")
	h.waitFor("one pane", func() bool { return len(h.panes()) == 1 })
}

func TestNextEnvironmentAndSidebar(t *testing.T) {
	h := newHarness(t, 90, 12, Options{})
	ctx := context.Background()
	if _, err := h.c.EnvironmentCreate(ctx, envReq("web", t.TempDir())); err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "web", Name: "dev"}); err != nil {
		t.Fatal(err)
	}
	h.waitText("web")
	h.press("ctrl+b", ")")
	h.waitFor("the web environment", func() bool { return h.a.ws.envID == "web" && h.a.tab() != nil })
	h.waitSized()
	h.golden("environments")

	// Clicking an environment in the sidebar switches back.
	h.clickHit(compositor.HitEnvironment, "api")
	h.waitFor("api again", func() bool { return h.a.ws.envID == "api" })
}

func TestNewTabWithoutEnvironmentCreatesOne(t *testing.T) {
	c := startDaemon(t)
	dir := filepath.Join(t.TempDir(), "my project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := startApp(t, c, 70, 10, dir, Options{HideSidebar: true})
	h.waitText("No environments yet")
	h.press("ctrl+b", "c")
	h.waitFor("an environment for the directory", func() bool { return h.a.ws.envID == "my-project" && h.a.tab() != nil })
}

// --- copy mode ---

func TestCopyModeYanksALine(t *testing.T) {
	h := newHarness(t, 80, 12, Options{HideSidebar: true})
	h.typeLines("one", "two")

	h.press("ctrl+b", "[")
	h.waitFor("copy mode", func() bool { return h.a.ui.copy != nil && h.a.ui.copy.c != nil })
	h.press("k")
	h.golden("copy")
	h.press("V", "y")
	if h.a.ui.mode != keymap.ModeTerminal || h.a.ui.copy != nil {
		t.Fatal("yank leaves copy mode")
	}
	if h.a.ui.clip != "two" {
		t.Fatalf("copied %q", h.a.ui.clip)
	}
	if out := h.a.TakeOutput(); len(out) != 1 || out[0] != ansi.SetSystemClipboard("two") {
		t.Fatalf("OSC 52 output %q", out)
	}

	// The paste key types it back.
	h.press("ctrl+b", "]")
	h.waitFor("the paste", func() bool { return strings.Count(h.screen(), "two") == 3 })
}

func TestCopyModeSearch(t *testing.T) {
	h := newHarness(t, 80, 12, Options{HideSidebar: true})
	h.typeLines("alpha beta", "gamma")
	h.press("ctrl+b", "[")
	h.waitFor("copy mode", func() bool { return h.a.ui.copy != nil && h.a.ui.copy.c != nil })
	h.press("?")
	h.typeText("beta")
	h.press("enter")
	if p := h.a.ui.copy.c.Cursor(); p.Col != 6 {
		t.Fatalf("cursor %+v after searching back for beta", p)
	}
	h.press("v", "e", "y")
	if h.a.ui.clip != "beta" {
		t.Fatalf("copied %q", h.a.ui.clip)
	}
}

func TestEditScrollbackOpensAPopup(t *testing.T) {
	t.Setenv("VISUAL", "tail -n 20 -f")
	h := newHarness(t, 80, 16, Options{HideSidebar: true})
	h.typeLines("remember me")
	h.press("ctrl+b", "e")
	h.waitFor("the popup", func() bool { t := h.a.tab(); return t != nil && len(t.Popups) == 1 })
	popup := h.a.tab().Popups[0].Pane
	t.Cleanup(func() {
		if p := h.a.ws.snap.pane(popup); p != nil && p.Process != nil && len(p.Process.Args) > 4 {
			_ = os.RemoveAll(p.Process.Args[len(p.Process.Args)-1])
		}
	})
	h.waitFor("the scrollback in the editor", func() bool { return strings.Count(h.screen(), "│remember me") == 2 })
}

// --- overlays ---

func TestHelpFilters(t *testing.T) {
	h := newHarness(t, 80, 16, Options{HideSidebar: true})
	h.press("ctrl+b", "?")
	h.typeText("zoom")
	h.golden("help")
	h.press("esc")
	if h.a.ui.overlay != nil {
		t.Fatal("esc closes help")
	}
}

func TestGotoPickerJumpsToAPane(t *testing.T) {
	h := newHarness(t, 90, 16, Options{HideSidebar: true})
	ctx := context.Background()
	created, err := h.api.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "api", Name: "logs"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.PaneRename(ctx, created.Pane.ID, "tailer"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.TabFocus(ctx, h.a.envTabs()[0].ID); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the tailer", func() bool { return h.a.ws.snap.pane(created.Pane.ID) != nil && h.a.tab().Name == "main" })

	h.press("ctrl+b", "w")
	h.typeText("tail")
	h.golden("picker")
	h.press("enter")
	h.waitFor("the logs tab", func() bool { return h.a.tab().ID == created.Tab.ID && h.focused() == created.Pane.ID })
}

func TestOverviewListsAgentsAndGoesToOne(t *testing.T) {
	h := newHarness(t, 100, 14, Options{HideSidebar: true})
	h.press("ctrl+b", "%")
	h.waitFor("a split", func() bool { return len(h.panes()) == 2 })
	left := h.panes()[0]
	h.typeLines("preview me")

	// Ages are relative to now: freeze it at the newest agent's start.
	var newest time.Time
	for _, p := range h.a.ws.snap.procs {
		if p.StartedAt.After(newest) {
			newest = p.StartedAt
		}
	}
	h.a.now = func() time.Time { return newest }

	h.press("ctrl+b", "O")
	h.waitFor("the preview", func() bool { return strings.Count(h.screen(), "preview me") >= 1 })
	h.golden("overview")
	h.press("k", "enter")
	h.waitFor("the first pane", func() bool { return h.a.ui.overview == nil && h.focused() == left })
}

// --- mouse ---

func TestMouseFocusesAndSelects(t *testing.T) {
	h := newHarness(t, 100, 14, Options{HideSidebar: true})
	h.press("ctrl+b", "%")
	h.waitFor("a split", func() bool { return len(h.panes()) == 2 })
	left, right := h.panes()[0], h.panes()[1]
	h.waitSized()

	lx, ly := h.paneCell(left, 1, 1)
	h.click(lx, ly, 0)
	h.waitFor("focus on the clicked pane", func() bool { return h.focused() == left })

	h.typeLines("drag to copy")
	x1, y1 := h.paneCell(left, 0, 0)
	x2, y2 := h.paneCell(left, 3, 0)
	h.drag(x1, y1, x2, y2)
	if h.a.ui.clip != "drag" || h.a.ui.copy != nil {
		t.Fatalf("drag copied %q (copy mode %v)", h.a.ui.clip, h.a.ui.copy != nil)
	}

	// A double click copies the word.
	wx, wy := h.paneCell(left, 6, 0)
	h.click(wx, wy, 0)
	h.click(wx, wy, 0)
	if h.a.ui.clip != "to" {
		t.Fatalf("double click copied %q", h.a.ui.clip)
	}

	// Dragging the border between the panes resizes them.
	before := h.a.geometry()[left].W
	area := h.a.regions().Panes
	bx := area.Min.X + h.a.geometry()[left].W
	h.drag(bx, area.Min.Y+3, bx+6, area.Min.Y+3)
	h.waitFor("a wider left pane", func() bool { return h.a.geometry()[left].W >= before+5 })
	_ = right
}

func TestMouseWheelScrollsIntoHistory(t *testing.T) {
	h := newHarness(t, 60, 8, Options{HideSidebar: true})
	for i := range 12 {
		h.typeLines("line" + string(rune('a'+i)))
	}
	x, y := h.paneCell(h.focused(), 2, 2)
	h.a.HandleEvent(uv.MouseWheelEvent(uv.Mouse{X: x, Y: y, Button: uv.MouseWheelUp}))
	h.waitFor("copy mode with history", func() bool { return h.a.ui.copy != nil && h.a.ui.copy.c != nil })
	if h.a.ui.copy.c.AtBottom() {
		t.Fatal("the wheel scrolled up into history")
	}
	for range 10 {
		h.a.HandleEvent(uv.MouseWheelEvent(uv.Mouse{X: x, Y: y, Button: uv.MouseWheelDown}))
	}
	if h.a.ui.copy != nil {
		t.Fatal("scrolling back to the bottom leaves copy mode")
	}
}

func TestMouseOnTabsAndSidebar(t *testing.T) {
	h := newHarness(t, 90, 12, Options{})
	first := h.a.tab().ID
	h.clickHit(compositor.HitNewTab, "")
	h.waitFor("a second tab", func() bool { return len(h.a.envTabs()) == 2 && h.a.tab().ID != first })
	h.clickHit(compositor.HitTab, first)
	h.waitFor("the first tab", func() bool { return h.a.tab().ID == first })

	h.clickHit(compositor.HitSidebarHeader, panelEnvs)
	if !h.a.ui.collapsed[panelEnvs] {
		t.Fatal("clicking a panel header collapses it")
	}

	// Drag the sidebar's edge.
	edge := h.a.regions().Sidebar.Max.X
	h.drag(edge, 5, edge+8, 5)
	if h.a.ui.sidebarW != DefaultSidebarWidth+8 {
		t.Fatalf("sidebar width %d", h.a.ui.sidebarW)
	}
	h.waitSized()
}

// --- narrow screens ---

func TestNarrowScreenSidebarIsAMenu(t *testing.T) {
	h := newHarness(t, 50, 12, Options{})
	if h.a.ui.sidebar {
		t.Fatal("a narrow screen opens on the panes")
	}
	h.press("ctrl+b", "b")
	if !h.a.sidebarFull() {
		t.Fatal("the sidebar fills a narrow screen")
	}
	h.golden("mobile-sidebar")
	h.press("down", "enter") // the agent: back to its pane
	if h.a.ui.sidebar {
		t.Fatal("choosing a row closes the menu")
	}
}

// --- following the daemon ---

func TestFollowsChangesFromOtherClients(t *testing.T) {
	h := newHarness(t, 90, 12, Options{HideSidebar: true})
	if _, err := h.api.PaneSplit(context.Background(), pane.SplitRequest{Pane: h.focused(), Direction: layout.Down}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the split, without input", func() bool { return len(h.panes()) == 2 })
}

func TestExitedPaneStaysWithItsStatus(t *testing.T) {
	h := newHarness(t, 70, 10, Options{HideSidebar: true})
	h.typeLines("bye")
	h.press("ctrl+d") // cat exits
	h.waitText("[exited 0]")
	h.golden("exited")
}

func TestUnreachableDaemon(t *testing.T) {
	c := client.NewService(client.Config{SocketPath: filepath.Join(t.TempDir(), "none.sock")})
	t.Cleanup(func() { _ = c.Close() })
	a := New(context.Background(), c, Options{Clipboard: ClipboardOff})
	t.Cleanup(a.Close)
	a.HandleEvent(uv.WindowSizeEvent{Width: 70, Height: 10})
	a.Start()
	h := &harness{t: t, c: c, a: a, w: 70, h: 10}
	h.waitText("Cannot reach the hive daemon")
	h.press("ctrl+b", "d")
	if !a.Quit() {
		t.Fatal("detach quits")
	}
}

// --- helpers ---

func envReq(id, dir string) environment.CreateRequest {
	return environment.CreateRequest{ID: id, Root: dir}
}

// paneCell is the screen cell of a pane's cell (x, y).
func (h *harness) paneCell(id string, x, y int) (int, int) {
	h.screen() // hits come from the last frame
	r := h.a.geometry()[id]
	area := h.a.regions().Panes
	return area.Min.X + r.X + x, area.Min.Y + r.Y + y
}

// clickHit clicks the first clickable area of a kind (and ID, if set).
func (h *harness) clickHit(kind compositor.HitKind, id string) {
	h.t.Helper()
	h.screen()
	for _, hit := range h.a.last.Hits {
		if hit.Kind == kind && (id == "" || hit.ID == id) {
			h.click(hit.Rect.Min.X, hit.Rect.Min.Y, 0)
			return
		}
	}
	h.t.Fatalf("nothing of kind %d %q on screen:\n%s", kind, id, h.screen())
}
