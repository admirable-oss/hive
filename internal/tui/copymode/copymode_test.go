package copymode_test

import (
	"testing"

	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/copymode"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/vt"
)

func buffer(lines ...string) [][]vt.Cell { return copymode.FromText(lines) }

func do(c *copymode.Copy, actions ...keymap.Action) {
	for _, a := range actions {
		c.Do(a)
	}
}

func at(t *testing.T, c *copymode.Copy, line, col int) {
	t.Helper()
	if p := c.Cursor(); p.Line != line || p.Col != col {
		t.Fatalf("cursor = %d:%d, want %d:%d", p.Line, p.Col, line, col)
	}
}

func TestStartsAtTheTerminalCursorAndMoves(t *testing.T) {
	// 6 lines, a 3-line view: the screen is lines 3-5.
	c := copymode.New(buffer("one", "two two", "three", "$ ls", "a.txt b.txt", "$ "), 20, 3, vt.Cursor{X: 2, Y: 2})
	at(t, c, 5, 0) // column clamped to the line's text ("$ " is "$")
	if c.Top() != 3 {
		t.Fatalf("top = %d", c.Top())
	}
	do(c, keymap.CopyUp)
	at(t, c, 4, 0)
	do(c, keymap.CopyLineEnd, keymap.CopyUp)
	at(t, c, 3, 3) // $ keeps to the end of shorter lines
	do(c, keymap.CopyTop)
	at(t, c, 0, 0)
	if c.Top() != 0 {
		t.Fatalf("view follows the cursor: top = %d", c.Top())
	}
	do(c, keymap.CopyBottom)
	at(t, c, 5, 0)
	do(c, keymap.CopyPageUp)
	at(t, c, 2, 0)
	do(c, keymap.CopyScreenBottom)
	at(t, c, 2, 0)
	do(c, keymap.CopyHalfDown)
	at(t, c, 3, 0)
	do(c, keymap.CopyRight, keymap.CopyRight, keymap.CopyRight, keymap.CopyRight)
	at(t, c, 3, 3)
}

func TestWordMotions(t *testing.T) {
	c := copymode.New(buffer("foo.bar  baz", "", "next line"), 40, 3, vt.Cursor{})
	do(c, keymap.CopyTop)
	steps := []struct {
		a         keymap.Action
		line, col int
	}{
		{keymap.CopyWordNext, 0, 3}, // foo|.bar
		{keymap.CopyWordNext, 0, 4}, // .|bar
		{keymap.CopyWordNext, 0, 9}, // baz
		{keymap.CopyWordNext, 1, 0}, // the empty line is a word
		{keymap.CopyWordNext, 2, 0}, // next
		{keymap.CopyWordEnd, 2, 3},  // nex|t
		{keymap.CopyWordEnd, 2, 8},  // lin|e
		{keymap.CopyWordPrev, 2, 5}, // |line
		{keymap.CopyWordPrev, 2, 0}, // |next
		{keymap.CopyWordPrev, 0, 9}, // back over the empty line to baz
		{keymap.CopyFirstNonBlank, 0, 0},
	}
	for i, s := range steps {
		c.Do(s.a)
		if p := c.Cursor(); p.Line != s.line || p.Col != s.col {
			t.Fatalf("step %d (%s): cursor %d:%d, want %d:%d", i, s.a, p.Line, p.Col, s.line, s.col)
		}
	}
}

func TestSearch(t *testing.T) {
	c := copymode.New(buffer("error: one", "fine", "Error: two", "ok"), 40, 4, vt.Cursor{Y: 3})
	if !c.Search("error", false) {
		t.Fatal("no match")
	}
	at(t, c, 2, 0) // smart case: lower-case query matches "Error"
	do(c, keymap.CopySearchNext)
	at(t, c, 0, 0) // n repeats in the same direction (up)
	do(c, keymap.CopySearchNext)
	at(t, c, 2, 0) // and wraps around
	do(c, keymap.CopySearchPrev)
	at(t, c, 0, 0)
	if !c.Search("Error", true) {
		t.Fatal("case-sensitive search")
	}
	at(t, c, 2, 0)
	if c.Search("missing", true) {
		t.Fatal("found something that is not there")
	}
	_, hl, _ := c.View()
	if count(hl, compositor.HighlightMatch)+count(hl, compositor.HighlightCurrentMatch) != 0 {
		t.Fatal("a failed search shows no matches")
	}
	c.Search("o", true)
	_, hl, _ = c.View()
	// "error: one"(2) "fine"(0) "Error: two"(2) "ok"(1)
	if n := count(hl, compositor.HighlightMatch) + count(hl, compositor.HighlightCurrentMatch); n != 5 {
		t.Fatalf("%d matches highlighted, want 5: %+v", n, hl)
	}
	if count(hl, compositor.HighlightCurrentMatch) != 1 {
		t.Fatal("one current match")
	}
}

func count(hl []compositor.Highlight, k compositor.HighlightKind) int {
	n := 0
	for _, h := range hl {
		if h.Kind == k {
			n++
		}
	}
	return n
}

func TestSelectAndYank(t *testing.T) {
	c := copymode.New(buffer("alpha beta  ", "gamma", "delta"), 40, 3, vt.Cursor{})
	do(c, keymap.CopyTop, keymap.CopyWordNext, keymap.CopySelect, keymap.CopyDown, keymap.CopyRight)
	if got := c.SelectionText(); got != "beta\ngamma" { // the cursor stops at the end of gamma
		t.Fatalf("selection = %q", got)
	}
	do(c, keymap.CopySelectLine)
	if got := c.SelectionText(); got != "alpha beta\ngamma" {
		t.Fatalf("line selection = %q (trailing blanks are dropped)", got)
	}
	// esc clears the selection first, then leaves.
	if done, _ := c.Do(keymap.CopyExit); done || c.Selecting() != copymode.SelectNone {
		t.Fatal("first esc clears the selection")
	}
	if done, _ := c.Do(keymap.CopyExit); !done {
		t.Fatal("second esc leaves")
	}

	// y without a selection copies the cursor's line.
	c = copymode.New(buffer("first", "second"), 40, 2, vt.Cursor{Y: 1})
	done, text := c.Do(keymap.CopyYank)
	if !done || text != "second" {
		t.Fatalf("yank = %v %q", done, text)
	}
}

func TestMouseSelectionAndWideCharacters(t *testing.T) {
	c := copymode.New(buffer("日本 text here", "more"), 40, 2, vt.Cursor{})
	c.SelectWord(8, 0) // inside "text"... "日本 " is 5 cells, "text" is 5-8
	if got := c.SelectionText(); got != "text" {
		t.Fatalf("double click selects %q", got)
	}
	c.SelectFrom(1, 0) // the right half of 日: snaps to its start
	at(t, c, 0, 0)
	c.MoveTo(2, 0)
	if got := c.SelectionText(); got != "日本" {
		t.Fatalf("drag selects %q", got)
	}
	do(c, keymap.CopyRight)
	at(t, c, 0, 4) // over the wide character in one step
}

func TestScrollAndView(t *testing.T) {
	var lines []string
	for range 100 {
		lines = append(lines, "line")
	}
	c := copymode.New(buffer(lines...), 10, 10, vt.Cursor{Y: 9})
	if !c.AtBottom() {
		t.Fatal("starts at the bottom")
	}
	c.Scroll(-30)
	if c.Top() != 60 || c.Cursor().Line != 69 || c.AtBottom() {
		t.Fatalf("after scrolling up: top %d cursor %+v", c.Top(), c.Cursor())
	}
	view, hl, cur := c.View()
	if len(view) != 10 || cur.Y != 9 || !cur.Hidden {
		t.Fatalf("view: %d lines, cursor %+v", len(view), cur)
	}
	if count(hl, compositor.HighlightCursor) != 1 {
		t.Fatal("copy mode draws its own cursor")
	}
	c.Resize(10, 5)
	if c.Cursor().Line-c.Top() >= 5 {
		t.Fatal("resize keeps the cursor in view")
	}
	if line, total := c.Position(); line != 70 || total != 100 {
		t.Fatalf("position %d/%d", line, total)
	}
}
