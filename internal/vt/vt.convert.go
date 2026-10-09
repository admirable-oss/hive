package vt

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// convertLine converts one emulator line into dst (same length), returning
// whether anything changed. Wide characters keep their width; the cell after
// one becomes a continuation cell (Width 0).
func convertLine(dst []Cell, src func(x int) *uv.Cell) bool {
	changed := false
	prevWide := false
	for x := range dst {
		c := convertCell(src(x), prevWide)
		prevWide = c.Width == 2
		if c != dst[x] {
			dst[x] = c
			changed = true
		}
	}
	return changed
}

func convertCell(c *uv.Cell, afterWide bool) Cell {
	if c == nil {
		if afterWide {
			return Cell{}
		}
		return Blank
	}
	out := Cell{Content: c.Content, Width: uint8(min(max(c.Width, 0), 2)), Style: convertStyle(&c.Style)}
	switch {
	case out.Width == 0 && afterWide:
		out.Content = ""
	case out.Width == 0:
		// An unwritten cell; treat it as blank.
		out.Content, out.Width = "", 1
	case out.Content == " ":
		out.Content = ""
	}
	return out
}

func convertStyle(s *uv.Style) Style {
	return Style{
		Fg:             convertColor(s.Fg),
		Bg:             convertColor(s.Bg),
		UnderlineColor: convertColor(s.UnderlineColor),
		Attrs:          Attr(s.Attrs),
		Underline:      Underline(s.Underline),
	}
}

func convertColor(c color.Color) Color {
	switch v := c.(type) {
	case nil:
		return DefaultColor
	case ansi.BasicColor:
		return Indexed(uint8(v))
	case ansi.IndexedColor:
		return Indexed(uint8(v))
	case ansi.TrueColor: //nolint:staticcheck // still produced by older x/ansi users; kept for safety
		return RGB(uint8(v>>16), uint8(v>>8), uint8(v))
	case ansi.RGBColor:
		return RGB(v.R, v.G, v.B)
	case color.RGBA:
		return RGB(v.R, v.G, v.B)
	default:
		r, g, b, _ := v.RGBA()
		return RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
	}
}
