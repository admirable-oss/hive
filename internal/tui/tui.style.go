package tui

import "github.com/charmbracelet/lipgloss"

// Palette. Hive gold is the brand accent; everything else stays quiet.
var (
	ColorYellow     = lipgloss.Color("#F5B942") // Hive gold: running, focus
	ColorPurple     = lipgloss.Color("#8B70FF") // awaiting
	ColorMuted      = lipgloss.Color("#606066") // labels, headers
	ColorText       = lipgloss.Color("#A0A0A5") // body text
	ColorTextBright = lipgloss.Color("#EDEDED") // selected text
	ColorBorder     = lipgloss.Color("#1F1F24") // grid lines
	ColorGreen      = lipgloss.Color("#27C93F") // success
	ColorRed        = lipgloss.Color("#FF5F56") // failure
	ColorDotYellow  = lipgloss.Color("#FFBD2E") // window dot
)

var (
	StyleDotRed    = lipgloss.NewStyle().Foreground(ColorRed)
	StyleDotYellow = lipgloss.NewStyle().Foreground(ColorDotYellow)
	StyleDotGreen  = lipgloss.NewStyle().Foreground(ColorGreen)

	StyleBorder         = lipgloss.NewStyle().Foreground(ColorBorder)
	StyleTitle          = lipgloss.NewStyle().Foreground(ColorText).Bold(true)
	StyleHeader         = lipgloss.NewStyle().Foreground(ColorMuted).Bold(true)
	StyleSelectedText   = lipgloss.NewStyle().Foreground(ColorTextBright).Bold(true)
	StyleUnselectedText = lipgloss.NewStyle().Foreground(ColorText)
	StyleMuted          = lipgloss.NewStyle().Foreground(ColorMuted)
	StyleGold           = lipgloss.NewStyle().Foreground(ColorYellow)
	StyleGoldBold       = lipgloss.NewStyle().Foreground(ColorYellow).Bold(true)
	StyleGreen          = lipgloss.NewStyle().Foreground(ColorGreen)
	StyleRed            = lipgloss.NewStyle().Foreground(ColorRed)
	StylePurple         = lipgloss.NewStyle().Foreground(ColorPurple)

	StyleKeyBadge = lipgloss.NewStyle().Foreground(ColorMuted).Background(ColorBorder).Padding(0, 1)
	StyleKeyLabel = lipgloss.NewStyle().Foreground(ColorText)
)
