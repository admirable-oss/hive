package compositor

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/admirable-oss/hive/internal/vt"
)

// styles converts vt styles to ultraviolet ones. Screens repeat a handful
// of styles, and boxing a colour into an interface allocates, so each
// distinct style is converted once.
type styles struct {
	cache map[vt.Style]uv.Style
}

func (s *styles) get(v vt.Style) uv.Style {
	if v == (vt.Style{}) {
		return uv.Style{}
	}
	if st, ok := s.cache[v]; ok {
		return st
	}
	if s.cache == nil || len(s.cache) > 4096 {
		s.cache = map[vt.Style]uv.Style{} // a program cycling through colours
	}
	st := uv.Style{
		Fg:             toColor(v.Fg),
		Bg:             toColor(v.Bg),
		UnderlineColor: toColor(v.UnderlineColor),
		Attrs:          uint8(v.Attrs),
		Underline:      uv.Underline(v.Underline),
	}
	s.cache[v] = st
	return st
}

func toColor(c vt.Color) color.Color {
	if c.IsDefault() {
		return nil
	}
	if i, ok := c.Index(); ok {
		if i < 16 {
			return ansi.BasicColor(i)
		}
		return ansi.IndexedColor(i)
	}
	r, g, b, _ := c.RGB()
	return color.RGBA{R: r, G: g, B: b, A: 0xff}
}

// Span is a run of text in one style.
type Span struct {
	Text  string
	Style uv.Style
}

// Spans is a line of styled text.
type Spans []Span

// Width returns the display width of the spans.
func (ss Spans) Width() int {
	w := 0
	for _, s := range ss {
		w += uniseg.StringWidth(s.Text)
	}
	return w
}

// drawText writes s at (x, y), at most maxW columns, and returns the column
// after the last cell written. A wide character that does not fit is
// replaced by a space.
func drawText(scr uv.Screen, x, y, maxW int, s string, st uv.Style) int {
	end := x + maxW
	state := -1
	rest := s
	for len(rest) > 0 && x < end {
		var gr string
		var w int
		gr, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
		switch {
		case gr == "\t":
			gr, w = " ", 1
		case w == 0:
			continue // combining marks without a base, control characters
		}
		if x+w > end {
			scr.SetCell(x, y, &uv.Cell{Content: " ", Width: 1, Style: st})
			x++
			break
		}
		scr.SetCell(x, y, &uv.Cell{Content: gr, Width: w, Style: st})
		x += w
	}
	return x
}

// drawSpans writes spans at (x, y) within maxW columns.
func drawSpans(scr uv.Screen, x, y, maxW int, ss Spans) int {
	end := x + maxW
	for _, s := range ss {
		if x >= end {
			break
		}
		x = drawText(scr, x, y, end-x, s.Text, s.Style)
	}
	return x
}

// fill paints a rectangle with blanks in style st.
func fill(scr uv.Screen, r uv.Rectangle, st uv.Style) {
	r = r.Intersect(scr.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			scr.SetCell(x, y, &uv.Cell{Content: " ", Width: 1, Style: st})
		}
	}
}

// Truncate shortens s to at most w columns, ending in "…" when cut.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if uniseg.StringWidth(s) <= w {
		return s
	}
	out, used, state := "", 0, -1
	rest := s
	for len(rest) > 0 {
		var gr string
		var gw int
		gr, rest, gw, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if used+gw > w-1 {
			break
		}
		out += gr
		used += gw
	}
	return out + "…"
}
