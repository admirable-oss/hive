// Package tui is the interactive dashboard (Bubble Tea). It talks to the
// daemon through the client.Client contract only, so it runs unchanged
// against a real daemon or a test fake: events drive refreshes (with polling
// as the fallback), and the selected agent's screen streams in as frames.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
)

// Run starts the dashboard and blocks until the user quits.
func Run(c client.Client) error {
	m := NewModel(c)
	defer m.Close()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
