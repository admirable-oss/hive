// Package termview turns terminal frames into the JSON screens that
// Hive's graphical clients draw: lines as runs of styled text, the cursor
// and the terminal's modes. The desktop app and the editor bridge (`hive
// editor serve`) send the same updates, and sdk/termview draws them.
package termview

import (
	"strconv"
	"strings"
	"sync"

	"github.com/admirable-oss/hive/internal/vt"
)

// ScreenUpdate is what a client draws a terminal from: every line (Full),
// or the lines that changed since the previous update.
type ScreenUpdate struct {
	Pane string `json:"pane" desc:"the pane (or view) the screen belongs to"`
	// Seq orders updates of one screen; a gap means the client must ask
	// for the whole screen again.
	Seq    uint64      `json:"seq" desc:"orders the updates of one screen; a gap means ask for the whole screen again"`
	Full   bool        `json:"full" desc:"every line is in this update"`
	Cols   int         `json:"cols"`
	Rows   int         `json:"rows"`
	Cursor CursorState `json:"cursor"`
	// Modes are the terminal's modes (see the Mode constants), which say
	// how keys and pastes are encoded.
	Modes uint32 `json:"modes" desc:"the terminal's modes: application cursor keys 1, bracketed paste 4, focus events 8, mouse reports 16–512, alternate screen 1024"`
	Title string `json:"title,omitempty"`
	Lines []Line `json:"lines"`
	// Ended says the process ended and no more updates come.
	Ended bool `json:"ended,omitempty" desc:"the process ended: no more updates come"`
}

// The terminal modes an update carries.
const (
	ModeAppCursorKeys  = uint32(vt.ModeAppCursorKeys)
	ModeBracketedPaste = uint32(vt.ModeBracketedPaste)
	ModeAltScreen      = uint32(vt.ModeAltScreen)
)

// CursorState is the cursor.
type CursorState struct {
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Visible bool   `json:"visible"`
	Shape   string `json:"shape" desc:"block, underline or bar"`
	Blink   bool   `json:"blink,omitempty"`
}

// Line is one screen line as runs of cells styled alike.
type Line struct {
	Y    int   `json:"y"`
	Runs []Run `json:"runs"`
}

// Run is text in one style. Width is how many columns it takes, so wide
// characters stay aligned whatever the font does.
type Run struct {
	Text  string `json:"t" desc:"the text"`
	Width int    `json:"w" desc:"the columns it takes"`
	// Fg and Bg are "" (the theme's), a palette index ("0"–"255") or
	// "#rrggbb".
	Fg string `json:"f,omitempty" desc:"foreground: empty (the theme's), a palette index or #rrggbb"`
	Bg string `json:"b,omitempty" desc:"background, as the foreground"`
	// Attrs are the cell attributes (bold 1, faint 2, italic 4, blink 8,
	// rapid blink 16, reverse 32, conceal 64, strikethrough 128), with the
	// underline style shifted 8 bits left.
	Attrs uint16 `json:"a,omitempty" desc:"bold 1, faint 2, italic 4, blink 8, rapid blink 16, reverse 32, conceal 64, strikethrough 128; the underline style shifted 8 bits left"`
	// Link is an OSC 8 hyperlink.
	Link string `json:"l,omitempty" desc:"an OSC 8 hyperlink"`
}

// ColorString is a colour as a ScreenUpdate carries it.
func ColorString(c vt.Color) string {
	if c.IsDefault() {
		return ""
	}
	if i, ok := c.Index(); ok {
		return strconv.Itoa(int(i))
	}
	if r, g, b, ok := c.RGB(); ok {
		const hex = "0123456789abcdef"
		return string([]byte{'#', hex[r>>4], hex[r&15], hex[g>>4], hex[g&15], hex[b>>4], hex[b&15]})
	}
	return ""
}

// EncodeLine turns a line of cells into runs, dropping trailing blanks.
func EncodeLine(y int, cells []vt.Cell) Line {
	end := len(cells)
	for end > 0 && cells[end-1].IsBlank() {
		end--
	}
	ln := Line{Y: y, Runs: []Run{}}
	var cur *Run
	var text strings.Builder
	flush := func() {
		if cur != nil {
			cur.Text = text.String()
			ln.Runs = append(ln.Runs, *cur)
			cur = nil
			text.Reset()
		}
	}
	for _, c := range cells[:end] {
		if c.Width == 0 {
			continue // the right half of a wide character
		}
		r := Run{
			Fg: ColorString(c.Style.Fg), Bg: ColorString(c.Style.Bg),
			Attrs: uint16(c.Style.Attrs) | uint16(c.Style.Underline)<<8, Link: c.Link,
		}
		if cur == nil || cur.Fg != r.Fg || cur.Bg != r.Bg || cur.Attrs != r.Attrs || cur.Link != r.Link {
			flush()
			cur = &r
		}
		content := c.Content
		if content == "" {
			content = " "
		}
		text.WriteString(content)
		cur.Width += max(int(c.Width), 1)
	}
	flush()
	return ln
}

var cursorShapes = map[vt.CursorShape]string{0: "block", 1: "underline", 2: "bar"}

// Encode encodes lines ys of screen s (all of them with full).
func Encode(pane string, seq uint64, s *vt.Screen, full bool, ys []int) ScreenUpdate {
	u := ScreenUpdate{
		Pane: pane, Seq: seq, Full: full, Cols: s.Cols, Rows: s.Rows, Modes: uint32(s.Modes), Title: s.Title,
		Cursor: CursorState{X: s.Cursor.X, Y: s.Cursor.Y, Visible: !s.Cursor.Hidden, Shape: cursorShapes[s.Cursor.Shape], Blink: s.Cursor.Blink},
		Lines:  []Line{},
	}
	if u.Cursor.Shape == "" {
		u.Cursor.Shape = "block"
	}
	if full {
		ys = ys[:0]
		for y := range s.Lines {
			ys = append(ys, y)
		}
	}
	for _, y := range ys {
		if y >= 0 && y < len(s.Lines) {
			u.Lines = append(u.Lines, EncodeLine(y, s.Lines[y]))
		}
	}
	return u
}

// LineText is a line's text.
func LineText(l Line) string {
	var sb strings.Builder
	for _, r := range l.Runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}

// Screen applies a terminal's frames and hands out the updates that bring
// a client up to date, coalesced: however many frames arrive between two
// Takes, one update carries the lines they changed. It is safe for one
// goroutine applying frames and another taking updates.
type Screen struct {
	pane string

	mu     sync.Mutex
	screen *vt.Screen
	seq    uint64
	full   bool
	dirty  map[int]bool
	ended  bool
}

// NewScreen returns a screen whose updates name pane.
func NewScreen(pane string) *Screen { return &Screen{pane: pane, dirty: map[int]bool{}} }

// Apply takes a frame.
func (s *Screen) Apply(f *vt.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.screen == nil {
		s.screen = &vt.Screen{}
	}
	resized := f.Cols != s.screen.Cols || f.Rows != s.screen.Rows
	s.screen.Apply(f)
	if f.Keyframe || resized {
		s.full = true
	}
	for _, l := range f.Lines {
		s.dirty[l.Y] = true
	}
}

// End says the process ended: the next update says so, and is the last.
func (s *Screen) End() {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
}

// Refresh makes the next update carry the whole screen (a client lost
// track).
func (s *Screen) Refresh() {
	s.mu.Lock()
	s.full = true
	s.mu.Unlock()
}

// Take returns the update since the last one, if anything changed, and
// the lines it carries.
func (s *Screen) Take() (ScreenUpdate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.screen == nil {
		if s.ended {
			s.ended = false // said once
			return ScreenUpdate{Pane: s.pane, Ended: true, Lines: []Line{}}, true
		}
		return ScreenUpdate{}, false
	}
	if !s.full && len(s.dirty) == 0 && !s.ended {
		return ScreenUpdate{}, false
	}
	ys := make([]int, 0, len(s.dirty))
	for y := range s.dirty {
		ys = append(ys, y)
	}
	s.seq++
	u := Encode(s.pane, s.seq, s.screen, s.full, ys)
	u.Ended = s.ended
	s.full, s.dirty = false, map[int]bool{}
	return u, true
}

// Text returns the screen's lines as text.
func (s *Screen) Text() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.screen == nil {
		return nil
	}
	out := make([]string, len(s.screen.Lines))
	for y, cells := range s.screen.Lines {
		out[y] = LineText(EncodeLine(y, cells))
	}
	return out
}
