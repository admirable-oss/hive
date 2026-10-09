package vt

import "strings"

// Color is a packed terminal colour. The zero value is the terminal's
// default colour; the top byte says how to read the rest.
type Color uint32

const (
	colorKindShift       = 24
	colorIndexed   Color = 1 << colorKindShift
	colorRGB       Color = 2 << colorKindShift
	colorValueMask Color = 1<<colorKindShift - 1
	colorKindMask        = ^colorValueMask
	DefaultColor   Color = 0
)

// Indexed returns palette colour i (0–15 are the basic ANSI colours).
func Indexed(i uint8) Color { return colorIndexed | Color(i) }

// RGB returns a 24-bit colour.
func RGB(r, g, b uint8) Color { return colorRGB | Color(r)<<16 | Color(g)<<8 | Color(b) }

// IsDefault reports whether c is the terminal's default colour.
func (c Color) IsDefault() bool { return c == DefaultColor }

// Index returns the palette index of an indexed colour.
func (c Color) Index() (uint8, bool) { return uint8(c), c&colorKindMask == colorIndexed }

// RGB returns the components of a 24-bit colour.
func (c Color) RGB() (r, g, b uint8, ok bool) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c), c&colorKindMask == colorRGB
}

// Attr is a set of text attributes.
type Attr uint8

const (
	AttrBold Attr = 1 << iota
	AttrFaint
	AttrItalic
	AttrBlink
	AttrRapidBlink
	AttrReverse
	AttrConceal
	AttrStrikethrough
)

// Underline is the underline style (none, single, double, curly, dotted,
// dashed), numbered as in SGR 4:n.
type Underline uint8

// Style is everything about a cell except its text.
type Style struct {
	Fg, Bg, UnderlineColor Color
	Attrs                  Attr
	Underline              Underline
}

// Cell is one column of the screen. A blank cell has empty Content and
// Width 1. A wide character (CJK, most emoji) occupies its own cell with
// Width 2 and is followed by a continuation cell with Width 0.
type Cell struct {
	Content string
	Width   uint8
	Style   Style
	// Link is the URL of an OSC 8 hyperlink over the cell, if any.
	Link string
}

// MaxLinkLen bounds a hyperlink's URL, as terminals do; longer ones are
// dropped.
const MaxLinkLen = 2048

// CleanLink returns url if it is safe to pass to another terminal inside
// an OSC 8 sequence (no control characters, not too long), else "".
func CleanLink(url string) string {
	if len(url) > MaxLinkLen {
		return ""
	}
	for i := range len(url) {
		if c := url[i]; c < 0x20 || c == 0x7f {
			return ""
		}
	}
	return url
}

// Blank is an empty, unstyled cell.
var Blank = Cell{Width: 1}

// IsBlank reports whether c shows nothing: no text and no visible style.
func (c Cell) IsBlank() bool {
	return (c.Content == "" || c.Content == " ") && c.Width == 1 && c.Style == Style{} && c.Link == ""
}

// CursorShape is the cursor's appearance.
type CursorShape uint8

const (
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
)

// Cursor is the cursor's position and appearance.
type Cursor struct {
	X, Y   int
	Hidden bool
	Shape  CursorShape
	Blink  bool
}

// Modes are the terminal modes a client must mirror for keyboard and mouse
// input to reach the application in the form it expects.
type Modes uint32

const (
	ModeAppCursorKeys  Modes = 1 << iota // DECCKM: arrows send ESC O A
	ModeAppKeypad                        // DECKPAM
	ModeBracketedPaste                   // 2004
	ModeFocusEvents                      // 1004
	ModeMouseX10                         // 9
	ModeMouseNormal                      // 1000
	ModeMouseButton                      // 1002
	ModeMouseAny                         // 1003
	ModeMouseUTF8                        // 1005
	ModeMouseSGR                         // 1006
	ModeMouseURXVT                       // 1015
	ModeAltScreen                        // 1049 / 1047 / 47
)

// Size limits. Real terminals stay far below them; they keep a corrupt or
// hostile size from allocating gigabytes of cells.
const (
	MaxCols = 2000
	MaxRows = 1000
)

// ClampSize limits a terminal size to 1..MaxCols by 1..MaxRows.
func ClampSize(cols, rows int) (int, int) {
	return min(max(cols, 1), MaxCols), min(max(rows, 1), MaxRows)
}

// Screen is a snapshot of a terminal: its cells, cursor, title and modes.
type Screen struct {
	Cols, Rows int
	Lines      [][]Cell // Rows lines of Cols cells each
	Cursor     Cursor
	Title      string
	Modes      Modes
	// Bells counts BEL characters received so far, so a client can ring
	// once per new bell.
	Bells uint32
}

// NewScreen returns a blank screen.
func NewScreen(cols, rows int) *Screen {
	s := &Screen{}
	s.Reset(cols, rows)
	return s
}

// Reset blanks the screen at a new size.
func (s *Screen) Reset(cols, rows int) {
	s.Cols, s.Rows = cols, rows
	s.Lines = make([][]Cell, rows)
	for y := range s.Lines {
		s.Lines[y] = blankLine(cols)
	}
	s.Cursor = Cursor{}
}

func blankLine(cols int) []Cell {
	l := make([]Cell, cols)
	for x := range l {
		l[x] = Blank
	}
	return l
}

// Clone returns a deep copy of s. Cell contents are immutable strings, so
// copying the cells is enough.
func (s *Screen) Clone() *Screen {
	c := *s
	c.Lines = make([][]Cell, len(s.Lines))
	for y, l := range s.Lines {
		c.Lines[y] = append([]Cell(nil), l...)
	}
	return &c
}

// LineText returns line y as plain text with trailing blanks removed.
func (s *Screen) LineText(y int) string {
	return LineText(s.Lines[y])
}

// Text returns the screen as plain text, one line per row, with trailing
// blanks removed from each line and trailing empty lines dropped.
func (s *Screen) Text() string {
	lines := make([]string, s.Rows)
	last := -1
	for y := range s.Lines {
		lines[y] = s.LineText(y)
		if lines[y] != "" {
			last = y
		}
	}
	return strings.Join(lines[:last+1], "\n")
}

// LineText returns cells as plain text with trailing blanks removed.
func LineText(l []Cell) string {
	var b strings.Builder
	for _, c := range l {
		switch {
		case c.Width == 0:
			// continuation of a wide character
		case c.Content == "":
			b.WriteByte(' ')
		default:
			b.WriteString(c.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}
