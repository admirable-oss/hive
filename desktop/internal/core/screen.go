package core

import (
	"github.com/admirable-oss/hive/internal/termview"
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

// The encoding is internal/termview's, which the editor bridge sends too;
// these types stay here because the frontend's bindings are made from
// them.

func colorString(c hiveapi.Color) string { return termview.ColorString(c) }

// encodeLine turns a line of cells into runs, dropping trailing blanks.
func encodeLine(y int, cells []hiveapi.Cell) Line { return lineOf(termview.EncodeLine(y, cells)) }

func lineOf(l termview.Line) Line {
	ln := Line{Y: l.Y, Runs: make([]Run, len(l.Runs))}
	for i, r := range l.Runs {
		ln.Runs[i] = Run(r)
	}
	return ln
}

// update encodes lines ys of screen s (all of them with full).
func update(pane string, seq uint64, s *hiveapi.TerminalScreen, full bool, ys []int) ScreenUpdate {
	u := termview.Encode(pane, seq, s, full, ys)
	out := ScreenUpdate{
		Pane: u.Pane, Seq: u.Seq, Full: u.Full, Cols: u.Cols, Rows: u.Rows, Cursor: CursorState(u.Cursor),
		Modes: u.Modes, Title: u.Title, Lines: make([]Line, len(u.Lines)), Ended: u.Ended,
	}
	for i, l := range u.Lines {
		out.Lines[i] = lineOf(l)
	}
	return out
}
