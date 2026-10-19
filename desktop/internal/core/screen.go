package core

import (
	"strconv"
	"strings"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// ScreenUpdate is what the frontend draws a pane from: every line (Full),
// or the lines that changed since the previous update.
type ScreenUpdate struct {
	Pane string `json:"pane"`
	// Seq orders updates of one pane; a gap means the frontend must ask for
	// the whole screen again (Hub.Refresh).
	Seq    uint64      `json:"seq"`
	Full   bool        `json:"full"`
	Cols   int         `json:"cols"`
	Rows   int         `json:"rows"`
	Cursor CursorState `json:"cursor"`
	// Modes are the terminal's modes (see the Mode constants), which say
	// how keys and pastes are encoded.
	Modes uint32 `json:"modes"`
	Title string `json:"title,omitempty"`
	Lines []Line `json:"lines"`
	// Ended says the pane's process ended and no more updates come.
	Ended bool `json:"ended,omitempty"`
}

// EventImage carries an ImageUpdate.
const EventImage = "hive:image"

// ImageUpdate is a change to the images over a pane (Kitty graphics an
// agent printed): show one, remove one image's placements, or clear all.
type ImageUpdate struct {
	Pane string `json:"pane"`
	Op   string `json:"op"` // place, delete, clear
	// Key names a placement; Image the image it shows.
	Key   string `json:"key,omitempty"`
	Image uint32 `json:"image,omitempty"`
	// X, Y, Cols and Rows place it on the screen, in cells.
	X    int `json:"x"`
	Y    int `json:"y"`
	Cols int `json:"cols"`
	Rows int `json:"rows"`
	// Src is the image as a data: URL of a PNG Hive encoded itself.
	Src string `json:"src,omitempty"`
}

// The terminal modes an update carries.
const (
	ModeAppCursorKeys  = uint32(hiveapi.ModeAppCursorKeys)
	ModeBracketedPaste = uint32(hiveapi.ModeBracketedPaste)
	ModeAltScreen      = uint32(hiveapi.ModeAltScreen)
)

// CursorState is the cursor.
type CursorState struct {
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Visible bool   `json:"visible"`
	Shape   string `json:"shape"` // block, underline, bar
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
	Text  string `json:"t"`
	Width int    `json:"w"`
	// Fg and Bg are "" (the theme's), a palette index ("0"–"255") or
	// "#rrggbb".
	Fg string `json:"f,omitempty"`
	Bg string `json:"b,omitempty"`
	// Attrs are the cell attributes (bold 1, faint 2, italic 4, blink 8,
	// rapid blink 16, reverse 32, conceal 64, strikethrough 128), with the
	// underline style shifted 8 bits left.
	Attrs uint16 `json:"a,omitempty"`
	// Link is an OSC 8 hyperlink.
	Link string `json:"l,omitempty"`
}

func colorString(c hiveapi.Color) string {
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

// encodeLine turns a line of cells into runs, dropping trailing blanks.
func encodeLine(y int, cells []hiveapi.Cell) Line {
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
			Fg: colorString(c.Style.Fg), Bg: colorString(c.Style.Bg),
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

var cursorShapes = map[hiveapi.CursorShape]string{0: "block", 1: "underline", 2: "bar"}

// update encodes lines ys of screen s (all of them with full).
func update(pane string, seq uint64, s *hiveapi.TerminalScreen, full bool, ys []int) ScreenUpdate {
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
			u.Lines = append(u.Lines, encodeLine(y, s.Lines[y]))
		}
	}
	return u
}
