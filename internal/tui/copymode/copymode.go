// Package copymode is the scrollback cursor: vim motions over a pane's
// history and screen, frozen when copy mode starts, with / and ? search,
// character and line selection, and yanking the selection as text.
//
// It knows nothing about keys or drawing: the multiplexer maps keys to
// keymap actions, calls Do, and draws View with the compositor.
package copymode

import (
	"strings"
	"unicode"

	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/vt"
)

// Pos is a position in the buffer: a line (0 is the oldest history line)
// and a cell column.
type Pos struct{ Line, Col int }

func (p Pos) before(q Pos) bool { return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col) }

// Selection is how text is being selected.
type Selection uint8

const (
	SelectNone Selection = iota
	SelectChars
	SelectLines
)

// Copy is one copy-mode session.
type Copy struct {
	lines  [][]vt.Cell
	texts  []lineText // search text per line, built lazily
	width  int
	height int

	top    int // first line shown
	cursor Pos
	want   int // the column vertical motions aim for

	sel    Selection
	anchor Pos

	query   string
	forward bool
	match   *Pos // current match start, for highlighting
}

// New starts copy mode over lines (history then screen) in a width×height
// view, with the cursor where the terminal's cursor was: screenCursor is
// relative to the last height lines.
func New(lines [][]vt.Cell, width, height int, screenCursor vt.Cursor) *Copy {
	if len(lines) == 0 {
		lines = [][]vt.Cell{nil}
	}
	c := &Copy{lines: lines, texts: make([]lineText, len(lines)), width: max(width, 1), height: max(height, 1)}
	c.top = max(len(lines)-c.height, 0)
	c.cursor = Pos{Line: min(c.top+max(screenCursor.Y, 0), len(lines)-1), Col: max(screenCursor.X, 0)}
	c.clampCol()
	c.want = c.cursor.Col
	return c
}

// FromText builds buffer lines from plain text (history fetched from the
// daemon).
func FromText(lines []string) [][]vt.Cell {
	out := make([][]vt.Cell, len(lines))
	for i, l := range lines {
		for _, r := range l {
			w := 1
			if isWide(r) {
				w = 2
			}
			out[i] = append(out[i], vt.Cell{Content: string(r), Width: uint8(w)})
			if w == 2 {
				out[i] = append(out[i], vt.Cell{Width: 0})
			}
		}
	}
	return out
}

// isWide is a cheap East Asian width check for history text.
func isWide(r rune) bool {
	return r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f300 && r <= 0x1f64f) || (r >= 0x1f900 && r <= 0x1f9ff) ||
		(r >= 0x20000 && r <= 0x3fffd))
}

// Resize changes the view's size, keeping the cursor visible.
func (c *Copy) Resize(width, height int) {
	c.width, c.height = max(width, 1), max(height, 1)
	c.scrollToCursor()
}

// Cursor returns the cursor position.
func (c *Copy) Cursor() Pos { return c.cursor }

// Top returns the first line shown.
func (c *Copy) Top() int { return c.top }

// Lines returns the number of lines in the buffer.
func (c *Copy) Lines() int { return len(c.lines) }

// Selecting returns the selection mode.
func (c *Copy) Selecting() Selection { return c.sel }

// Query returns the last search.
func (c *Copy) Query() string { return c.query }

// lineLen is the number of cells in a line, without trailing blanks.
func (c *Copy) lineLen(y int) int {
	l := c.lines[y]
	n := len(l)
	for n > 0 && (l[n-1].Content == "" || l[n-1].Content == " ") && l[n-1].Width != 0 {
		n--
	}
	return n
}

// clampCol keeps the cursor on a line's text (or its first column) and off
// the right half of a wide character.
func (c *Copy) clampCol() {
	n := c.lineLen(c.cursor.Line)
	c.cursor.Col = min(max(c.cursor.Col, 0), max(n-1, 0))
	l := c.lines[c.cursor.Line]
	for c.cursor.Col > 0 && c.cursor.Col < len(l) && l[c.cursor.Col].Width == 0 {
		c.cursor.Col--
	}
}

func (c *Copy) scrollToCursor() {
	if c.cursor.Line < c.top {
		c.top = c.cursor.Line
	}
	if c.cursor.Line >= c.top+c.height {
		c.top = c.cursor.Line - c.height + 1
	}
	c.top = min(max(c.top, 0), max(len(c.lines)-c.height, 0))
}

func (c *Copy) moveLine(to int) {
	c.cursor.Line = min(max(to, 0), len(c.lines)-1)
	c.cursor.Col = c.want
	c.clampCol()
	c.scrollToCursor()
}

func (c *Copy) setCol(col int) {
	c.cursor.Col = col
	c.clampCol()
	c.want = c.cursor.Col
}

// Do performs a copy-mode action. done reports that copy mode should end;
// yanked is the selected text when the action copied it.
func (c *Copy) Do(a keymap.Action) (done bool, yanked string) {
	switch a {
	case keymap.CopyLeft:
		c.setCol(c.cursor.Col - 1)
		for c.cursor.Col > 0 && c.lines[c.cursor.Line][c.cursor.Col].Width == 0 {
			c.setCol(c.cursor.Col - 1)
		}
	case keymap.CopyRight:
		next := c.cursor.Col + 1
		if l := c.lines[c.cursor.Line]; c.cursor.Col < len(l) && l[c.cursor.Col].Width == 2 {
			next++
		}
		c.setCol(next)
	case keymap.CopyUp:
		c.moveLine(c.cursor.Line - 1)
	case keymap.CopyDown:
		c.moveLine(c.cursor.Line + 1)
	case keymap.CopyLineStart:
		c.setCol(0)
	case keymap.CopyLineEnd:
		c.setCol(c.lineLen(c.cursor.Line) - 1)
		c.want = 1 << 30 // stay at the end on vertical moves, as in vim
	case keymap.CopyFirstNonBlank:
		l := c.lines[c.cursor.Line]
		x := 0
		for x < c.lineLen(c.cursor.Line) && classOf(l[x]) == classSpace {
			x++
		}
		c.setCol(x)
	case keymap.CopyTop:
		c.cursor = Pos{}
		c.want = 0
		c.scrollToCursor()
	case keymap.CopyBottom:
		c.cursor.Line = len(c.lines) - 1
		c.setCol(0)
		c.scrollToCursor()
	case keymap.CopyScreenTop:
		c.moveLine(c.top)
	case keymap.CopyScreenMiddle:
		c.moveLine(c.top + min(c.height, len(c.lines)-c.top)/2)
	case keymap.CopyScreenBottom:
		c.moveLine(c.top + min(c.height, len(c.lines)-c.top) - 1)
	case keymap.CopyHalfUp:
		c.page(-c.height / 2)
	case keymap.CopyHalfDown:
		c.page(c.height / 2)
	case keymap.CopyPageUp:
		c.page(-c.height)
	case keymap.CopyPageDown:
		c.page(c.height)
	case keymap.CopyWordNext:
		c.wordNext()
	case keymap.CopyWordPrev:
		c.wordPrev()
	case keymap.CopyWordEnd:
		c.wordEnd()
	case keymap.CopySearchNext:
		c.searchAgain(c.forward)
	case keymap.CopySearchPrev:
		c.searchAgain(!c.forward)
	case keymap.CopySelect:
		c.toggle(SelectChars)
	case keymap.CopySelectLine:
		c.toggle(SelectLines)
	case keymap.CopyYank:
		if c.sel == SelectNone {
			// Nothing selected: copy the cursor's line, as tmux does.
			c.sel, c.anchor = SelectLines, c.cursor
		}
		return true, c.SelectionText()
	case keymap.CopyExit:
		if c.sel != SelectNone {
			c.sel = SelectNone // the first esc clears the selection
			return false, ""
		}
		return true, ""
	}
	return false, ""
}

// page moves the view and the cursor by n lines.
func (c *Copy) page(n int) {
	c.top = min(max(c.top+n, 0), max(len(c.lines)-c.height, 0))
	c.moveLine(c.cursor.Line + n)
}

func (c *Copy) toggle(s Selection) {
	switch c.sel {
	case s:
		c.sel = SelectNone
	case SelectNone:
		c.sel, c.anchor = s, c.cursor
	default:
		c.sel = s // switch between char and line selection, keeping the anchor
	}
}

// Scroll moves the view by n lines (the mouse wheel), dragging the cursor
// along when it would leave the view.
func (c *Copy) Scroll(n int) {
	c.top = min(max(c.top+n, 0), max(len(c.lines)-c.height, 0))
	switch {
	case c.cursor.Line < c.top:
		c.moveLine(c.top)
	case c.cursor.Line >= c.top+c.height:
		c.moveLine(c.top + c.height - 1)
	}
}

// AtBottom reports whether the view shows the end of the buffer (the live
// screen), where scrolling down would leave copy mode.
func (c *Copy) AtBottom() bool { return c.top >= len(c.lines)-c.height }

// MoveTo puts the cursor at a view position (a mouse click).
func (c *Copy) MoveTo(x, y int) {
	c.cursor.Line = min(max(c.top+y, 0), len(c.lines)-1)
	c.setCol(x)
}

// SelectFrom starts a character selection at a view position (a mouse
// drag's start), replacing any selection.
func (c *Copy) SelectFrom(x, y int) {
	c.MoveTo(x, y)
	c.sel, c.anchor = SelectChars, c.cursor
}

// SelectWord selects the word at a view position (a double click).
func (c *Copy) SelectWord(x, y int) {
	c.MoveTo(x, y)
	l := c.lines[c.cursor.Line]
	if c.cursor.Col >= len(l) {
		return
	}
	cls := classOf(l[c.cursor.Col])
	start, end := c.cursor.Col, c.cursor.Col
	for start > 0 && classOf(l[start-1]) == cls {
		start--
	}
	for end+1 < c.lineLen(c.cursor.Line) && classOf(l[end+1]) == cls {
		end++
	}
	c.sel, c.anchor = SelectChars, Pos{Line: c.cursor.Line, Col: start}
	c.setCol(end)
}

// --- words ---

type class uint8

const (
	classSpace class = iota
	classWord
	classPunct
)

func classOf(cell vt.Cell) class {
	if cell.Width == 0 {
		return classWord // the right half of a wide character
	}
	r := []rune(cell.Content)
	switch {
	case len(r) == 0 || unicode.IsSpace(r[0]):
		return classSpace
	case unicode.IsLetter(r[0]) || unicode.IsDigit(r[0]) || r[0] == '_':
		return classWord
	}
	return classPunct
}

// classAt is the class of a position; past the end of a line is space, so
// line breaks separate words.
func (c *Copy) classAt(p Pos) class {
	l := c.lines[p.Line]
	if p.Col >= c.lineLen(p.Line) {
		return classSpace
	}
	return classOf(l[p.Col])
}

// step moves p one cell forward (or back), across lines; ok is false at
// either end of the buffer.
func (c *Copy) step(p Pos, forward bool) (Pos, bool) {
	if forward {
		if p.Col+1 < max(c.lineLen(p.Line), 1) {
			return Pos{p.Line, p.Col + 1}, true
		}
		if p.Line+1 < len(c.lines) {
			return Pos{p.Line + 1, 0}, true
		}
		return p, false
	}
	if p.Col > 0 {
		return Pos{p.Line, min(p.Col-1, max(c.lineLen(p.Line)-1, 0))}, true
	}
	if p.Line > 0 {
		return Pos{p.Line - 1, max(c.lineLen(p.Line-1)-1, 0)}, true
	}
	return p, false
}

func (c *Copy) jump(p Pos) {
	c.cursor = p
	c.clampCol()
	c.want = c.cursor.Col
	c.scrollToCursor()
}

// wordNext is vim's w: the start of the next word.
func (c *Copy) wordNext() {
	p := c.cursor
	start := c.classAt(p)
	ok := true
	startLine := p.Line
	for ok && c.classAt(p) == start && start != classSpace && p.Line == startLine {
		p, ok = c.step(p, true)
	}
	for ok && c.classAt(p) == classSpace {
		if p.Line != startLine && c.lineLen(p.Line) == 0 {
			break // an empty line is a word
		}
		p, ok = c.step(p, true)
	}
	if ok {
		c.jump(p)
	}
}

// wordEnd is vim's e: the end of this or the next word.
func (c *Copy) wordEnd() {
	p, ok := c.step(c.cursor, true)
	for ok && c.classAt(p) == classSpace {
		p, ok = c.step(p, true)
	}
	if !ok {
		return
	}
	cls := c.classAt(p)
	for {
		next, ok := c.step(p, true)
		if !ok || next.Line != p.Line || c.classAt(next) != cls {
			break
		}
		p = next
	}
	c.jump(p)
}

// wordPrev is vim's b: the start of this or the previous word.
func (c *Copy) wordPrev() {
	p, ok := c.step(c.cursor, false)
	for ok && c.classAt(p) == classSpace {
		p, ok = c.step(p, false)
	}
	if !ok {
		c.jump(p)
		return
	}
	cls := c.classAt(p)
	for {
		prev, ok := c.step(p, false)
		if !ok || prev.Line != p.Line || c.classAt(prev) != cls {
			break
		}
		p = prev
	}
	c.jump(p)
}

// --- search ---

// lineText is a line as text, with the cell column of each byte.
type lineText struct {
	built bool
	text  string
	col   []int // byte offset → cell column
}

func (c *Copy) text(y int) *lineText {
	t := &c.texts[y]
	if t.built {
		return t
	}
	var b strings.Builder
	for x, cell := range c.lines[y] {
		if cell.Width == 0 {
			continue
		}
		s := cell.Content
		if s == "" {
			s = " "
		}
		for range len(s) {
			t.col = append(t.col, x)
		}
		b.WriteString(s)
	}
	t.text, t.built = b.String(), true
	return t
}

// smartCase searches case-insensitively unless the query has a capital.
func smartCase(hay, query string) (string, string) {
	if strings.ToLower(query) == query {
		return strings.ToLower(hay), query
	}
	return hay, query
}

// find returns the first match of query after (or before) from, wrapping
// around the buffer.
func (c *Copy) find(query string, from Pos, forward bool) (Pos, bool) {
	n := len(c.lines)
	for i := 0; i <= n; i++ {
		y := ((from.Line-i)%n + n) % n
		if forward {
			y = (from.Line + i) % n
		}
		t := c.text(y)
		hay, q := smartCase(t.text, query)
		if forward {
			start := 0
			if i == 0 {
				start = c.byteAfter(t, from.Col)
				if start < 0 {
					continue
				}
			}
			if j := strings.Index(hay[start:], q); j >= 0 {
				return Pos{y, t.col[start+j]}, true
			}
			continue
		}
		end := len(hay)
		if i == 0 {
			end = c.byteAt(t, from.Col)
		}
		if i == n {
			end = len(hay) // wrapped back to the starting line
		}
		if j := strings.LastIndex(hay[:end], q); j >= 0 {
			return Pos{y, t.col[j]}, true
		}
	}
	return Pos{}, false
}

// byteAt is the first byte of column col (len(text) past the end).
func (c *Copy) byteAt(t *lineText, col int) int {
	for b, x := range t.col {
		if x >= col {
			return b
		}
	}
	return len(t.text)
}

// byteAfter is the first byte after column col, or -1 past the end.
func (c *Copy) byteAfter(t *lineText, col int) int {
	for b, x := range t.col {
		if x > col {
			return b
		}
	}
	return -1
}

// Search looks for query from the cursor, forward (/) or backward (?), and
// moves the cursor to the match. It reports whether there was one.
func (c *Copy) Search(query string, forward bool) bool {
	if query == "" {
		return false
	}
	c.query, c.forward = query, forward
	return c.searchAgain(forward)
}

func (c *Copy) searchAgain(forward bool) bool {
	if c.query == "" {
		return false
	}
	p, ok := c.find(c.query, c.cursor, forward)
	if !ok {
		c.match = nil
		return false
	}
	c.match = &p
	c.jump(p)
	return true
}

// --- selection and view ---

// bounds returns the selection's start and end (inclusive), ordered.
func (c *Copy) bounds() (Pos, Pos) {
	a, b := c.anchor, c.cursor
	if b.before(a) {
		a, b = b, a
	}
	if c.sel == SelectLines {
		a.Col = 0
		b.Col = 1 << 30
	}
	return a, b
}

// SelectionText returns the selected text, lines joined by "\n" with
// trailing blanks removed.
func (c *Copy) SelectionText() string {
	if c.sel == SelectNone {
		return ""
	}
	a, b := c.bounds()
	var out []string
	for y := a.Line; y <= b.Line; y++ {
		l := c.lines[y]
		x1, x2 := 0, len(l)
		if y == a.Line {
			x1 = min(a.Col, len(l))
		}
		if y == b.Line {
			end := b.Col + 1
			if b.Col < len(l) && l[b.Col].Width == 2 {
				end++
			}
			x2 = min(end, len(l))
		}
		out = append(out, vt.LineText(l[x1:max(x1, x2)]))
	}
	return strings.Join(out, "\n")
}

// View returns what to draw: the visible lines, the selection and search
// highlights, and the cursor, all relative to the view.
func (c *Copy) View() ([][]vt.Cell, []compositor.Highlight, vt.Cursor) {
	end := min(c.top+c.height, len(c.lines))
	lines := c.lines[c.top:end]
	var hl []compositor.Highlight
	if c.sel != SelectNone {
		a, b := c.bounds()
		for y := max(a.Line, c.top); y <= b.Line && y < end; y++ {
			x1, x2 := 0, c.width
			if y == a.Line {
				x1 = a.Col
			}
			if y == b.Line && c.sel == SelectChars {
				x2 = b.Col + 1
			}
			hl = append(hl, compositor.Highlight{Y: y - c.top, X1: x1, X2: x2, Kind: compositor.HighlightSelection})
		}
	}
	if c.query != "" {
		for y := c.top; y < end; y++ {
			t := c.text(y)
			hay, q := smartCase(t.text, c.query)
			for off := 0; ; {
				j := strings.Index(hay[off:], q)
				if j < 0 || q == "" {
					break
				}
				start := off + j
				x1 := t.col[start]
				x2 := c.width
				if start+len(q) < len(t.col) {
					x2 = t.col[start+len(q)]
				} else if len(t.col) > 0 {
					x2 = t.col[len(t.col)-1] + 1
				}
				kind := compositor.HighlightMatch
				if c.match != nil && c.match.Line == y && c.match.Col == x1 {
					kind = compositor.HighlightCurrentMatch
				}
				hl = append(hl, compositor.Highlight{Y: y - c.top, X1: x1, X2: x2, Kind: kind})
				off = start + max(len(q), 1)
			}
		}
	}
	hl = append(hl, compositor.Highlight{Y: c.cursor.Line - c.top, X1: c.cursor.Col, X2: c.cursor.Col + 1, Kind: compositor.HighlightCursor})
	return lines, hl, vt.Cursor{X: c.cursor.Col, Y: c.cursor.Line - c.top, Hidden: true}
}

// Position describes the view for the status line: "[12/340]".
func (c *Copy) Position() (line, total int) { return c.cursor.Line + 1, len(c.lines) }
