package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
)

// Run starts the interactive Hive TUI program.
func Run(c client.Client) error {
	model := NewModel(c)
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
