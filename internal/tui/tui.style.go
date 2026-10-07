package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	ColorYellow     = lipgloss.Color("#F5B942")
	ColorText       = lipgloss.Color("#BDBDBD")
	ColorTextBright = lipgloss.Color("#EDEDED")
	ColorMuted      = lipgloss.Color("#77777A")
	ColorDark       = lipgloss.Color("#333336")
	ColorBorder     = lipgloss.Color("#26262B")
	ColorGreen      = lipgloss.Color("#48C774")
	ColorRed        = lipgloss.Color("#F14668")
	ColorBg         = lipgloss.Color("#0B0B0C")

	// Text styles
	StyleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorTextBright)

	StyleSubtitle = lipgloss.NewStyle().
			Foreground(ColorText)

	StyleMuted = lipgloss.NewStyle().
			Foreground(ColorMuted)

	StyleAccent = lipgloss.NewStyle().
			Foreground(ColorYellow)

	StyleIndicator = lipgloss.NewStyle().
			Foreground(ColorYellow).
			Bold(true)

	StyleSelectedText = lipgloss.NewStyle().
				Foreground(ColorTextBright).
				Bold(true)

	StyleUnselectedText = lipgloss.NewStyle().
				Foreground(ColorText)

	StyleHeader = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Bold(true)

	StyleBorder = lipgloss.NewStyle().
			Foreground(ColorBorder)

	StyleStatusRunning = lipgloss.NewStyle().
				Foreground(ColorYellow)

	StyleStatusExited = lipgloss.NewStyle().
				Foreground(ColorMuted)

	StyleStatusFailed = lipgloss.NewStyle().
				Foreground(ColorRed)

	StyleConnected = lipgloss.NewStyle().
			Foreground(ColorGreen)

	StyleDisconnected = lipgloss.NewStyle().
				Foreground(ColorRed)
)
