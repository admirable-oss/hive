package compositor_test

import (
	"flag"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/theme"
	"github.com/admirable-oss/hive/internal/vt"
)

var update = flag.Bool("update", false, "rewrite golden files")

// render draws s and returns the screen as text, plus a second block that
// marks every cell drawn in the theme's accent colour with '*' (the focused
// pane's border and title, the active tab).
func render(t *testing.T, s *compositor.Scene) (string, compositor.Result) {
	t.Helper()
	buf := uv.NewScreenBuffer(s.Width, s.Height)
	res := compositor.New().Draw(buf, s)
	var text, accent strings.Builder
	for y := 0; y < s.Height; y++ {
		var line, mask strings.Builder
		for x := 0; x < s.Width; {
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
			m := " "
			if c.Style.Fg != nil && sameColor(c.Style.Fg, s.Theme.Accent) {
				m = "*"
			}
			mask.WriteString(strings.Repeat(m, max(c.Width, 1)))
			x += max(c.Width, 1)
		}
		text.WriteString(strings.TrimRight(line.String(), " ") + "\n")
		accent.WriteString(strings.TrimRight(mask.String(), " ") + "\n")
	}
	out := text.String()
	if res.Cursor != nil {
		out += "cursor: " + itoa(res.Cursor.X) + "," + itoa(res.Cursor.Y) + "\n"
	}
	return out + "--- accent ---\n" + accent.String(), res
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from %s (go test -update rewrites it)\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

// screen builds terminal content from text lines.
func screen(lines ...string) [][]vt.Cell {
	out := make([][]vt.Cell, len(lines))
	for y, l := range lines {
		for _, r := range l {
			out[y] = append(out[y], vt.Cell{Content: string(r), Width: 1})
		}
	}
	return out
}

func th() theme.Theme { t, _ := theme.Lookup("catppuccin"); return t }

// scene lays out a tree in a width×height screen with the given sidebar.
func scene(width, height, sidebar int, root *layout.Node, focus string, content map[string][][]vt.Cell) *compositor.Scene {
	reg := compositor.Plan(width, height, sidebar)
	s := &compositor.Scene{Width: width, Height: height, Theme: th(), Regions: reg}
	rects := layout.Geometry(root, layout.Rect{W: reg.Panes.Dx(), H: reg.Panes.Dy()}, "")
	for _, id := range root.Panes() {
		p := compositor.Pane{ID: id, Rect: rects[id], Title: id, Focused: id == focus, Lines: content[id]}
		if id == focus {
			p.Cursor = &vt.Cursor{X: 2, Y: 1}
		}
		s.Panes = append(s.Panes, p)
	}
	s.TabBar = compositor.TabBar{
		Left: []compositor.Span{{Text: "api"}},
		Tabs: []compositor.Tab{{ID: "t1", Label: "1:agents", Active: true}, {ID: "t2", Label: "2:logs", Activity: true}},
	}
	return s
}

func split(t *testing.T, root *layout.Node, target string, d layout.Direction, id string) *layout.Node {
	t.Helper()
	n, err := layout.Split(root, target, d, 0, id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSinglePane(t *testing.T) {
	s := scene(40, 8, 0, layout.Leaf("claude"), "claude", map[string][][]vt.Cell{
		"claude": screen("$ claude", "> hello"),
	})
	out, res := render(t, s)
	golden(t, "single", out)
	if res.Cursor == nil || res.Cursor.X != 2 || res.Cursor.Y != 3 {
		t.Fatalf("cursor = %+v, want 2,3 (pane origin 0,2)", res.Cursor)
	}
}

func TestSplitsBordersAndTitles(t *testing.T) {
	// a │ b
	//   ├──
	//   │ c
	root := split(t, layout.Leaf("a"), "a", layout.Right, "b")
	root = split(t, root, "b", layout.Down, "c")
	s := scene(40, 12, 0, root, "c", map[string][][]vt.Cell{
		"a": screen("left side", "line 2"),
		"b": screen("top right"),
		"c": screen("bottom right", "a much longer line that gets cut at the border"),
	})
	out, _ := render(t, s)
	golden(t, "splits", out)
}

func TestFourWayJunction(t *testing.T) {
	// a │ b
	// ──┼──
	// c │ d
	root := split(t, layout.Leaf("a"), "a", layout.Down, "c")
	root = split(t, root, "a", layout.Right, "b")
	root = split(t, root, "c", layout.Right, "d")
	s := scene(31, 11, 0, root, "a", nil)
	out, _ := render(t, s)
	golden(t, "four", out)
}

func TestSidebarAndHits(t *testing.T) {
	s := scene(80, 10, 18, layout.Leaf("p1"), "p1", map[string][][]vt.Cell{"p1": screen("hi")})
	s.Sidebar = &compositor.Sidebar{Rows: []compositor.SidebarRow{
		{Header: true, Spans: []compositor.Span{{Text: "▾ Environments"}}},
		{ID: "api", Kind: compositor.HitEnvironment, Selected: true, Spans: []compositor.Span{{Text: "api  main ↑1"}}},
		{ID: "web", Kind: compositor.HitEnvironment, Spans: []compositor.Span{{Text: "web"}}},
		{Header: true, Spans: []compositor.Span{{Text: "▾ Agents"}}},
		{ID: "p1", Kind: compositor.HitAgent, Spans: []compositor.Span{{Text: "● claude"}}},
	}}
	out, res := render(t, s)
	golden(t, "sidebar", out)
	hits := map[[2]int]compositor.HitKind{
		{3, 1}: compositor.HitEnvironment, {3, 4}: compositor.HitAgent, {3, 8}: compositor.HitSidebar,
		{18, 4}: compositor.HitSidebarBorder, {25, 0}: compositor.HitTab, {25, 5}: compositor.HitPane, {25, 1}: compositor.HitPaneTitle,
	}
	for pos, want := range hits {
		if h, _ := res.HitAt(pos[0], pos[1]); h.Kind != want {
			t.Errorf("hit at %v = %+v, want kind %d", pos, h, want)
		}
	}
	if h, _ := res.HitAt(3, 1); h.ID != "api" {
		t.Errorf("environment row hit = %+v", h)
	}
}

func TestMobileSidebarTakesTheScreen(t *testing.T) {
	reg := compositor.Plan(50, 20, 24)
	if reg.Sidebar.Dx() != 50 || !reg.Panes.Empty() {
		t.Fatalf("on a narrow screen the sidebar is a single full-width column: %+v", reg)
	}
	reg = compositor.Plan(120, 40, 24)
	if reg.Sidebar.Dx() != 24 || reg.Panes.Min.X != 25 || reg.Panes.Dx() != 95 || reg.Panes.Dy() != 38 {
		t.Fatalf("regions = %+v", reg)
	}
}

func TestPopupOverlayAndToast(t *testing.T) {
	s := scene(50, 16, 0, layout.Leaf("main"), "", map[string][][]vt.Cell{"main": screen("background text")})
	s.Popups = []compositor.Pane{{
		ID: "pop", Rect: layout.Rect{X: 10, Y: 3, W: 30, H: 5}, Title: "popup", Focused: true,
		Lines: screen("inside the popup"), Cursor: &vt.Cursor{X: 0, Y: 1},
	}}
	s.Toasts = []compositor.Toast{{Text: "copied 12 lines", Kind: compositor.ToastSuccess}}
	out, res := render(t, s)
	golden(t, "popup", out)
	if res.Cursor == nil || res.Cursor.X != 10 || res.Cursor.Y != 6 {
		t.Fatalf("the popup's cursor wins: %+v", res.Cursor)
	}
}

func TestPickerOverlay(t *testing.T) {
	s := scene(50, 14, 0, layout.Leaf("main"), "main", nil)
	s.Overlays = []compositor.Overlay{{
		Title: "Go to", Width: 36, Height: 9,
		Input:    &compositor.Input{Prompt: "› ", Text: "cla", Cursor: 3},
		Lines:    []compositor.Spans{{{Text: "claude  api"}}, {{Text: "claude  web"}}, {{Text: "codex   api"}}},
		Selected: 1,
		Footer:   []compositor.Span{{Text: "⏎ go  esc close"}},
	}}
	out, res := render(t, s)
	golden(t, "picker", out)
	if res.Cursor == nil || res.Cursor.Shape != vt.CursorBar {
		t.Fatalf("the input owns the cursor: %+v", res.Cursor)
	}
	if h, ok := res.HitAt(20, 6); !ok || h.Kind != compositor.HitOverlayLine || h.Index != 1 {
		t.Fatalf("hit = %+v", h)
	}
}

func TestHighlightsDimAndPlaceholder(t *testing.T) {
	root := split(t, layout.Leaf("copy"), "copy", layout.Right, "dead")
	s := scene(40, 8, 0, root, "copy", map[string][][]vt.Cell{
		"copy": screen("select me please", "second"),
	})
	s.Panes[0].Highlights = []compositor.Highlight{{Y: 0, X1: 7, X2: 9, Kind: compositor.HighlightSelection}, {Y: 1, X1: 0, X2: 3, Kind: compositor.HighlightMatch}}
	s.Panes[1].Dim = true
	s.Panes[1].Placeholder = "exited (status 1)"
	s.Panes[1].Badges = []compositor.Span{{Text: "[exited]"}}
	buf := uv.NewScreenBuffer(40, 8)
	compositor.New().Draw(buf, s)
	// Row 0 of the left pane is screen row 2.
	if c := buf.CellAt(7, 2); c.Style.Bg == nil {
		t.Errorf("selected cell has no background: %+v", c.Style)
	}
	if c := buf.CellAt(6, 2); c.Style.Bg != nil {
		t.Errorf("unselected cell is highlighted: %+v", c.Style)
	}
	out, _ := render(t, s)
	golden(t, "highlights", out)
}

func TestWideCharactersAndStyles(t *testing.T) {
	lines := [][]vt.Cell{{
		{Content: "日", Width: 2},
		{Width: 0},
		{Content: "x", Width: 1, Style: vt.Style{Fg: vt.Indexed(1), Attrs: vt.AttrBold}},
		{Content: "y", Width: 1, Style: vt.Style{Bg: vt.RGB(1, 2, 3)}},
	}}
	s := scene(12, 5, 0, layout.Leaf("w"), "", map[string][][]vt.Cell{"w": lines})
	// A 3-column pane cuts the second wide character rather than splitting it.
	s.Panes[0].Lines = append(slices.Clone(lines), []vt.Cell{{Content: "a", Width: 1}, {Content: "b", Width: 1}, {Content: "日", Width: 2}, {Width: 0}})
	s.Panes[0].Rect.W = 3
	buf := uv.NewScreenBuffer(12, 5)
	compositor.New().Draw(buf, s)
	if c := buf.CellAt(0, 2); c.Content != "日" || c.Width != 2 {
		t.Fatalf("wide cell = %+v", c)
	}
	if c := buf.CellAt(2, 2); c.Content != "x" || c.Style.Attrs&uv.AttrBold == 0 || c.Style.Fg == nil {
		t.Fatalf("styled cell = %+v", c)
	}
	if c := buf.CellAt(2, 3); c.Content != " " {
		t.Fatalf("a wide character that does not fit is not drawn: %+v", c)
	}
}

func TestTruncate(t *testing.T) {
	for in, want := range map[string]string{"hello": "hello", "hello world": "hell…", "日本語です": "日本…"} {
		if got := compositor.Truncate(in, 5); got != want {
			t.Errorf("Truncate(%q, 5) = %q, want %q", in, got, want)
		}
	}
}

func TestOverlayWithSidePreview(t *testing.T) {
	s := scene(60, 10, 0, layout.Leaf("main"), "main", nil)
	s.Overlays = []compositor.Overlay{{
		Title: "Overview", Width: 60, Height: 10, ListWidth: 20,
		Lines:    []compositor.Spans{{{Text: "AGENT"}}, {{Text: "● claude"}}, {{Text: "○ codex"}}},
		Selected: 1,
		Footer:   []compositor.Span{{Text: "⏎ go"}},
		Side:     &compositor.Pane{ID: "claude", Title: "claude", Lines: screen("> fix the tests", "working…")},
	}}
	out, res := render(t, s)
	golden(t, "overview", out)
	if h, ok := res.HitAt(5, 2); !ok || h.Kind != compositor.HitOverlayLine || h.Index != 1 {
		t.Fatalf("list hit = %+v", h)
	}
	if h, ok := res.HitAt(30, 1); !ok || h.Kind != compositor.HitPane || h.ID != "claude" {
		t.Fatalf("preview hit = %+v", h)
	}
}
