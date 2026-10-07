package tui

import (
	"context"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

type AttachFinishedMsg struct {
	Err error
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		return m, nil

	case bee.TickMsg:
		m.Bee.Tick()
		return m, bee.Tick(m.Bee)

	case RefreshMsg:
		m.Connected = msg.Connected
		if msg.Connected {
			m.Environments = msg.Environments
			m.Processes = msg.Processes

			// Check if any process is running across all environments
			hasRunning := false
			for _, procs := range m.Processes {
				for _, p := range procs {
					if p.Status == process.StatusRunning {
						hasRunning = true
						break
					}
				}
				if hasRunning {
					break
				}
			}

			if hasRunning {
				m.Bee.SetState(bee.StateActive)
			} else {
				m.Bee.SetState(bee.StateIdle)
			}
		} else {
			m.Bee.SetState(bee.StateDisconnected)
		}

		// Keep selections within bounds
		if m.SelectedEnv >= len(m.Environments) {
			m.SelectedEnv = max(0, len(m.Environments)-1)
		}
		currentProcs := m.currentProcesses()
		if m.SelectedProc >= len(currentProcs) {
			m.SelectedProc = max(0, len(currentProcs)-1)
		}
		return m, nil

	case AttachFinishedMsg:
		// Re-fetch data when returning from terminal attach
		return m, m.fetchDataCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.Inspecting != nil {
				m.Inspecting = nil
				return m, nil
			}
			m.Quitting = true
			return m, tea.Quit

		case "esc":
			if m.Inspecting != nil {
				m.Inspecting = nil
				return m, nil
			}
			if m.Focus == FocusProcesses {
				m.Focus = FocusEnvironments
				return m, nil
			}

		case "r":
			return m, m.fetchDataCmd()

		case "tab":
			if m.Focus == FocusEnvironments {
				if len(m.currentProcesses()) > 0 {
					m.Focus = FocusProcesses
				}
			} else {
				m.Focus = FocusEnvironments
			}
			return m, nil

		case "up", "k":
			if m.Focus == FocusEnvironments {
				if m.SelectedEnv > 0 {
					m.SelectedEnv--
					m.SelectedProc = 0
				}
			} else {
				if m.SelectedProc > 0 {
					m.SelectedProc--
				}
			}
			return m, nil

		case "down", "j":
			if m.Focus == FocusEnvironments {
				if m.SelectedEnv < len(m.Environments)-1 {
					m.SelectedEnv++
					m.SelectedProc = 0
				}
			} else {
				if m.SelectedProc < len(m.currentProcesses())-1 {
					m.SelectedProc++
				}
			}
			return m, nil

		case "left", "h":
			m.Focus = FocusEnvironments
			return m, nil

		case "right", "l":
			if len(m.currentProcesses()) > 0 {
				m.Focus = FocusProcesses
			}
			return m, nil

		case "enter":
			if m.Focus == FocusEnvironments {
				if len(m.currentProcesses()) > 0 {
					m.Focus = FocusProcesses
				}
			} else {
				// Inspect current process
				procs := m.currentProcesses()
				if len(procs) > 0 && m.SelectedProc < len(procs) {
					p := procs[m.SelectedProc]
					m.Inspecting = &p
				}
			}
			return m, nil

		case "a":
			// Attach to process
			procs := m.currentProcesses()
			var target *process.Process
			if m.Inspecting != nil {
				target = m.Inspecting
			} else if m.Focus == FocusProcesses && len(procs) > 0 && m.SelectedProc < len(procs) {
				target = &procs[m.SelectedProc]
			}

			if target != nil && target.Status == process.StatusRunning {
				cmd := exec.Command(os.Args[0], "terminal", "attach", target.ID)
				return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
					return AttachFinishedMsg{Err: err}
				})
			}

		case "s":
			// Stop process
			procs := m.currentProcesses()
			var target *process.Process
			if m.Inspecting != nil {
				target = m.Inspecting
			} else if m.Focus == FocusProcesses && len(procs) > 0 && m.SelectedProc < len(procs) {
				target = &procs[m.SelectedProc]
			}

			if target != nil && target.Status == process.StatusRunning {
				procID := target.ID
				return m, func() tea.Msg {
					_ = m.Client.ProcessStop(context.Background(), procID)
					return RefreshMsg{Connected: true}
				}
			}
		}
	}

	return m, nil
}

func (m Model) currentProcesses() []process.Process {
	if len(m.Environments) == 0 || m.SelectedEnv >= len(m.Environments) {
		return nil
	}
	env := m.Environments[m.SelectedEnv]
	return m.Processes[env.ID]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
