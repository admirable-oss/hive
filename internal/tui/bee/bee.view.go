package bee

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Palette struct {
	Yellow     lipgloss.Color
	Wing       lipgloss.Color
	Antennae   lipgloss.Color
	Dark       lipgloss.Color
	StripeDark lipgloss.Color
}

var ActivePalette = Palette{
	Yellow:     lipgloss.Color("#F5B942"),
	Wing:       lipgloss.Color("#BDBDBB"),
	Antennae:   lipgloss.Color("#9B9BA1"),
	Dark:       lipgloss.Color("#0B0B0C"),
	StripeDark: lipgloss.Color("#1A1A1D"),
}

var DisconnectedPalette = Palette{
	Yellow:     lipgloss.Color("#6A5B3D"),
	Wing:       lipgloss.Color("#4A4A4D"),
	Antennae:   lipgloss.Color("#555558"),
	Dark:       lipgloss.Color("#0B0B0C"),
	StripeDark: lipgloss.Color("#111113"),
}

func (m Model) View() string {
	palette := ActivePalette
	if m.State == StateDisconnected {
		palette = DisconnectedPalette
	}

	frame := FoldedFrame
	if m.State != StateDisconnected {
		frame = FlapFrames[m.FrameIndex%TotalFrames]
	}

	styleYellow := lipgloss.NewStyle().Foreground(palette.Yellow)
	styleWing := lipgloss.NewStyle().Foreground(palette.Wing)
	styleWingFull := lipgloss.NewStyle().Foreground(palette.Wing).Background(palette.Wing)
	styleAntennae := lipgloss.NewStyle().Foreground(palette.Antennae)
	styleYellowFull := lipgloss.NewStyle().Foreground(palette.Yellow).Background(palette.Yellow)
	styleEye := lipgloss.NewStyle().Foreground(palette.Yellow).Background(palette.Dark)
	styleStripe := lipgloss.NewStyle().Foreground(palette.Dark).Background(palette.Yellow)
	styleHeadAccent := lipgloss.NewStyle().Foreground(palette.Antennae).Background(palette.Yellow)
	styleDark := lipgloss.NewStyle().Foreground(palette.Dark)

	renderWing := func(w string) string {
		var out strings.Builder
		for _, r := range w {
			switch r {
			case '▀':
				out.WriteString(styleWingFull.Render("▀"))
			case '▄':
				out.WriteString(styleWing.Render("▄"))
			default:
				out.WriteRune(r)
			}
		}
		return out.String()
	}

	// Line 0: [Wing0L] + Antennae + [Wing0R]
	l0 := renderWing(frame.Row0.Left) +
		styleYellow.Render("▀") + styleAntennae.Render("▄") +
		"      " +
		styleAntennae.Render("▄") + styleYellow.Render("▀") +
		renderWing(frame.Row0.Right)

	// Line 1: [Wing1L] + Head + [Wing1R]
	l1 := renderWing(frame.Row1.Left) +
		" " +
		styleYellow.Render("▄") + styleHeadAccent.Render("▀") +
		styleYellow.Render("▄▄▄▄") +
		styleHeadAccent.Render("▀") + styleYellow.Render("▄") +
		" " +
		renderWing(frame.Row1.Right)

	// Line 2: [Wing2L] + Eyes/Head + [Wing2R]
	l2 := renderWing(frame.Row2.Left) +
		styleYellowFull.Render("▀▀") +
		styleEye.Render("▀") +
		styleYellowFull.Render("▀▀▀▀") +
		styleEye.Render("▀") +
		styleYellowFull.Render("▀▀") +
		renderWing(frame.Row2.Right)

	// Line 3: [Wing3L] + Abdomen stripe 1 + [Wing3R]
	l3 := renderWing(frame.Row3.Left) +
		styleYellowFull.Render("▀▀") +
		styleStripe.Render("▀") +
		styleYellowFull.Render("▀▀▀▀") +
		styleStripe.Render("▀") +
		styleYellowFull.Render("▀▀") +
		renderWing(frame.Row3.Right)

	// Line 4: Legs + Main stripe
	l4 := " " +
		styleYellow.Render("▄▄") +
		styleYellowFull.Render("▀") +
		styleStripe.Render("▀▀▀▀▀▀▀▀") +
		styleYellowFull.Render("▀") +
		styleYellow.Render("▄▄") +
		" "

	// Line 5: Abdomen lower tip
	l5 := "    " +
		styleYellowFull.Render("▀") +
		styleDark.Render("▀") +
		styleStripe.Render("▀") +
		styleDark.Render("▀▀") +
		styleStripe.Render("▀") +
		styleDark.Render("▀") +
		styleYellowFull.Render("▀") +
		"    "

	// Line 6: Feet
	l6 := "    " +
		styleYellow.Render("▀ ▀  ▀ ▀") +
		"    "

	return strings.Join([]string{l0, l1, l2, l3, l4, l5, l6}, "\n")
}
