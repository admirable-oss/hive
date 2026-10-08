// Package tui is the interactive dashboard (Bubble Tea). It polls the daemon
// through the client.Client contract only, so it runs unchanged against a
// real daemon or a test fake.
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
