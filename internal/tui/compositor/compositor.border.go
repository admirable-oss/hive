package compositor

import (
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
)

// Directions a border cell connects to.
const (
	up uint8 = 1 << iota
	down
	left
	right
)

// boxChars maps a set of connections to a line-drawing character.
var boxChars = [16]string{
	0:                        " ",
	up:                       "╵",
	down:                     "╷",
	up | down:                "│",
	left:                     "╴",
	right:                    "╶",
	left | right:             "─",
	down | right:             "┌",
	down | left:              "┐",
	up | right:               "└",
	up | left:                "┘",
	up | down | right:        "├",
	up | down | left:         "┤",
	down | left | right:      "┬",
	up | left | right:        "┴",
	up | down | left | right: "┼",
}

// borders is a grid of line connections over a tab's area, including its
// title row. Coordinates are relative to the pane area: the title row is
// y = -1.
type borders struct {
	w, h   int // pane area size
	conn   []uint8
	active []bool
}

func newBorders(w, h int) *borders {
	n := w * (h + 1)
	return &borders{w: w, h: h, conn: make([]uint8, n), active: make([]bool, n)}
}

func (b *borders) at(x, y int) int { return (y+1)*b.w + x }

func (b *borders) inside(x, y int) bool { return x >= 0 && x < b.w && y >= -1 && y < b.h }

// hline joins (x1, y) to (x2, y).
func (b *borders) hline(x1, x2, y int, act bool) {
	x1, x2 = max(x1, 0), min(x2, b.w-1)
	for x := x1; x <= x2; x++ {
		if !b.inside(x, y) {
			continue
		}
		i := b.at(x, y)
		if x > x1 {
			b.conn[i] |= left
		}
		if x < x2 {
			b.conn[i] |= right
		}
		if x1 == x2 {
			b.conn[i] |= left | right
		}
		b.active[i] = b.active[i] || act
	}
}

// vline joins (x, y1) to (x, y2).
func (b *borders) vline(x, y1, y2 int, act bool) {
	y1, y2 = max(y1, -1), min(y2, b.h-1)
	for y := y1; y <= y2; y++ {
		if !b.inside(x, y) {
			continue
		}
		i := b.at(x, y)
		if y > y1 {
			b.conn[i] |= up
		}
		if y < y2 {
			b.conn[i] |= down
		}
		b.active[i] = b.active[i] || act
	}
}

// addPane draws the lines around pane rect r: the line above it (its title
// row) and its left edge; for the focused pane also its right and bottom
// edges, so all four sides can be highlighted.
func (b *borders) addPane(r layout.Rect, focused bool) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	bottom := min(r.Y+r.H, b.h-1) // reaches the separator row below, if any
	b.hline(r.X-1, r.X+r.W, r.Y-1, focused)
	if r.X > 0 {
		b.vline(r.X-1, r.Y-1, bottom, focused)
	}
	if !focused {
		return
	}
	if r.X+r.W < b.w {
		b.vline(r.X+r.W, r.Y-1, bottom, true)
	}
	if r.Y+r.H < b.h {
		b.hline(r.X-1, r.X+r.W, r.Y+r.H, true)
	}
}

// draw paints the lines at origin (ox, oy), the top-left cell of the pane
// area.
func (b *borders) draw(scr uv.Screen, ox, oy int, normal, accent uv.Style) {
	for y := -1; y < b.h; y++ {
		for x := 0; x < b.w; x++ {
			i := b.at(x, y)
			c := b.conn[i]
			if c == 0 {
				continue
			}
			// A line end at the area's edge reads as a straight line.
			if c == left || c == right {
				c = left | right
			}
			if c == up || c == down {
				c = up | down
			}
			st := normal
			if b.active[i] {
				st = accent
			}
			scr.SetCell(ox+x, oy+y, &uv.Cell{Content: boxChars[c], Width: 1, Style: st})
		}
	}
}
