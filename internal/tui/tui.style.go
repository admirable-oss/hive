package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Palette from reference design
	ColorBg         = lipgloss.Color("#0B0B0C")
	ColorYellow     = lipgloss.Color("#F5B942") // Hive gold
	ColorPurple     = lipgloss.Color("#8B70FF") // Awaiting
	ColorMuted      = lipgloss.Color("#606066") // Labels, borders, headers
	ColorText       = lipgloss.Color("#A0A0A5") // Body text
	ColorTextBright = lipgloss.Color("#EDEDED") // Highlighted / active text
	ColorBorder     = lipgloss.Color("#1F1F24") // Thin grid borders
	ColorGreen      = lipgloss.Color("#27C93F") // Complete / success / dot
	ColorRed        = lipgloss.Color("#FF5F56") // Failed / dot
	ColorDotYellow  = lipgloss.Color("#FFBD2E") // Window dot yellow

	// Styles
	StyleDotRed    = lipgloss.NewStyle().Foreground(ColorRed)
	StyleDotYellow = lipgloss.NewStyle().Foreground(ColorDotYellow)
	StyleDotGreen  = lipgloss.NewStyle().Foreground(ColorGreen)

	StyleBorder = lipgloss.NewStyle().Foreground(ColorBorder)

	StyleTitle = lipgloss.NewStyle().
			Foreground(ColorText).
			Bold(true)

	StyleHeader = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Bold(true)

	StyleSelectedText = lipgloss.NewStyle().
				Foreground(ColorTextBright).
				Bold(true)

	StyleUnselectedText = lipgloss.NewStyle().
				Foreground(ColorText)

	StyleMuted = lipgloss.NewStyle().
			Foreground(ColorMuted)

	StyleGold = lipgloss.NewStyle().
			Foreground(ColorYellow)

	StyleGoldBold = lipgloss.NewStyle().
			Foreground(ColorYellow).
			Bold(true)

	StyleGreen = lipgloss.NewStyle().
			Foreground(ColorGreen)

	StylePurple = lipgloss.NewStyle().
			Foreground(ColorPurple)

	// Status styles
	StyleStatusRunning = lipgloss.NewStyle().
				Foreground(ColorYellow)

	StyleStatusAwaiting = lipgloss.NewStyle().
				Foreground(ColorPurple)

	StyleStatusComplete = lipgloss.NewStyle().
				Foreground(ColorMuted)

	StyleStatusFailed = lipgloss.NewStyle().
				Foreground(ColorRed)

	// Log lines
	StyleLogPrompt = lipgloss.NewStyle().
			Foreground(ColorMuted)

	StyleLogAction = lipgloss.NewStyle().
			Foreground(ColorTextBright)

	StyleLogFile = lipgloss.NewStyle().
			Foreground(ColorText)

	StyleLogDiff = lipgloss.NewStyle().
			Foreground(ColorYellow)

	// Keybadge for footer
	StyleKeyBadge = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Background(ColorBorder).
			Padding(0, 1)

	StyleKeyLabel = lipgloss.NewStyle().
			Foreground(ColorText)
)
