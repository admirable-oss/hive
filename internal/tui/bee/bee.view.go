package bee

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/tui/compositor"
)

type Palette struct {
	Yellow     color.Color
	Wing       color.Color
	Antennae   color.Color
	Dark       color.Color
	StripeDark color.Color
}

func rgb(v uint32) color.Color {
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

var ActivePalette = Palette{
	Yellow:     rgb(0xF5B942),
	Wing:       rgb(0xBDBDBB),
	Antennae:   rgb(0x9B9BA1),
	Dark:       rgb(0x0B0B0C),
	StripeDark: rgb(0x1A1A1D),
}

var DisconnectedPalette = Palette{
	Yellow:     rgb(0x6A5B3D),
	Wing:       rgb(0x4A4A4D),
	Antennae:   rgb(0x555558),
	Dark:       rgb(0x0B0B0C),
	StripeDark: rgb(0x111113),
}

// Width and Height are the art's size in cells.
const (
	Width  = 16
	Height = 7
)

// Lines draws the bee for the compositor: Height lines of Width cells.
func (m Model) Lines() []compositor.Spans {
	palette := ActivePalette
	if m.State == StateDisconnected {
		palette = DisconnectedPalette
	}

	frame := FoldedFrame
	if m.State != StateDisconnected {
		frame = FlapFrames[m.FrameIndex%TotalFrames]
	}

	style := func(fg, bg color.Color) func(string) compositor.Span {
		return func(s string) compositor.Span { return compositor.Span{Text: s, Style: uv.Style{Fg: fg, Bg: bg}} }
	}
	plain := func(s string) compositor.Span { return compositor.Span{Text: s} }
	styleYellow := style(palette.Yellow, nil)
	styleWing := style(palette.Wing, nil)
	styleWingFull := style(palette.Wing, palette.Wing)
	styleAntennae := style(palette.Antennae, nil)
	styleYellowFull := style(palette.Yellow, palette.Yellow)
	styleEye := style(palette.Yellow, palette.Dark)
	styleStripe := style(palette.Dark, palette.Yellow)
	styleHeadAccent := style(palette.Antennae, palette.Yellow)
	styleDark := style(palette.Dark, nil)

	renderWing := func(w string) compositor.Spans {
		var out compositor.Spans
		for _, r := range w {
			switch r {
			case '▀':
				out = append(out, styleWingFull("▀"))
			case '▄':
				out = append(out, styleWing("▄"))
			default:
				out = append(out, plain(string(r)))
			}
		}
		return out
	}
	line := func(parts ...any) compositor.Spans {
		var out compositor.Spans
		for _, p := range parts {
			switch p := p.(type) {
			case compositor.Span:
				out = append(out, p)
			case compositor.Spans:
				out = append(out, p...)
			}
		}
		return out
	}

	return []compositor.Spans{
		// Wings and antennae.
		line(renderWing(frame.Row0.Left), styleYellow("▀"), styleAntennae("▄"), plain("      "),
			styleAntennae("▄"), styleYellow("▀"), renderWing(frame.Row0.Right)),
		// Head.
		line(renderWing(frame.Row1.Left), plain(" "), styleYellow("▄"), styleHeadAccent("▀"), styleYellow("▄▄▄▄"),
			styleHeadAccent("▀"), styleYellow("▄"), plain(" "), renderWing(frame.Row1.Right)),
		// Eyes.
		line(renderWing(frame.Row2.Left), styleYellowFull("▀▀"), styleEye("▀"), styleYellowFull("▀▀▀▀"),
			styleEye("▀"), styleYellowFull("▀▀"), renderWing(frame.Row2.Right)),
		// First stripe.
		line(renderWing(frame.Row3.Left), styleYellowFull("▀▀"), styleStripe("▀"), styleYellowFull("▀▀▀▀"),
			styleStripe("▀"), styleYellowFull("▀▀"), renderWing(frame.Row3.Right)),
		// Legs and the main stripe.
		line(plain(" "), styleYellow("▄▄"), styleYellowFull("▀"), styleStripe("▀▀▀▀▀▀▀▀"), styleYellowFull("▀"),
			styleYellow("▄▄"), plain(" ")),
		// The abdomen's tip.
		line(plain("    "), styleYellowFull("▀"), styleDark("▀"), styleStripe("▀"), styleDark("▀▀"),
			styleStripe("▀"), styleDark("▀"), styleYellowFull("▀"), plain("    ")),
		// Feet.
		line(plain("    "), styleYellow("▀ ▀  ▀ ▀"), plain("    ")),
	}
}
