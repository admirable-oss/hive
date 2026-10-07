package bee

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TickMsg is sent by the animation timer to trigger the next frame.
type TickMsg struct{}

// Tick returns a Bubble Tea command that fires TickMsg after the model's FrameDelay.
func Tick(m Model) tea.Cmd {
	return tea.Tick(m.FrameDelay(), func(_ time.Time) tea.Msg {
		return TickMsg{}
	})
}
