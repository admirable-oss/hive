package tui

import (
	"context"
	"os"
	"os/exec"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width, m.Height = msg.Width, msg.Height
		return m, nil

	case bee.TickMsg:
		m.Bee.Tick()
		return m, bee.Tick(m.Bee)

	case pollTickMsg:
		return m, tea.Batch(pollTick(), m.refresh, m.fetchLogsCmd(m.selectedID))

	case LogsMsg:
		if msg.ProcessID != "" {
			m.logLines[msg.ProcessID] = sanitizeLog(msg.Logs)
		}
		return m, nil

	case RefreshMsg:
		return m.applyRefresh(msg), nil

	case attachFinishedMsg:
		return m, m.refresh

	case tea.KeyMsg:
		if m.Interactive {
			return m.updateInteractive(msg)
		}
		return m.updateNavigation(msg)
	}
	return m, nil
}

func (m Model) applyRefresh(msg RefreshMsg) Model {
	m.Connected = msg.Connected
	if !msg.Connected {
		m.Bee.SetState(bee.StateDisconnected)
		return m
	}

	m.Processes = msg.Processes
	m.reanchor()

	running := slices.ContainsFunc(m.Processes, func(p process.Process) bool { return p.Status == process.StatusRunning })
	if running {
		m.Bee.SetState(bee.StateActive)
	} else {
		m.Bee.SetState(bee.StateIdle)
	}
	if cur := m.CurrentProcess(); m.Interactive && (cur == nil || cur.Status != process.StatusRunning) {
		m.Interactive = false // the agent we were typing into is gone
	}
	return m
}

// updateInteractive forwards keys to the selected agent's terminal; Esc
// hands control back to the dashboard.
func (m Model) updateInteractive(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		m.Interactive = false
		return m, nil
	}
	cur := m.CurrentProcess()
	data := keyToBytes(msg)
	if cur == nil || cur.Status != process.StatusRunning || len(data) == 0 {
		return m, nil
	}
	c, id := m.Client, cur.ID
	return m, tea.Batch(
		func() tea.Msg {
			_ = c.TerminalInput(context.Background(), id, data)
			return nil
		},
		m.fetchLogsCmd(id),
	)
}

func (m Model) updateNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.CurrentProcess()
	running := cur != nil && cur.Status == process.StatusRunning

	switch msg.String() {
	case "ctrl+c", "q":
		m.Quitting = true
		return m, tea.Quit

	case "up", "k", "shift+tab":
		return m, m.selectIndex(m.SelectedProc - 1)

	case "down", "j", "tab":
		return m, m.selectIndex(m.SelectedProc + 1)

	case "r":
		return m, m.refresh

	case "enter":
		if running {
			m.Interactive = true
			return m, m.fetchLogsCmd(cur.ID)
		}
		return m, m.selectIndex(m.SelectedProc + 1)

	case "a":
		// Full-screen attach: hand the terminal to `hive terminal attach`.
		if running {
			exe, err := os.Executable()
			if err != nil {
				exe = os.Args[0]
			}
			return m, tea.ExecProcess(exec.Command(exe, "terminal", "attach", cur.ID), func(err error) tea.Msg {
				return attachFinishedMsg{Err: err}
			})
		}

	case "s":
		if running {
			c, id := m.Client, cur.ID
			return m, func() tea.Msg {
				_ = c.ProcessStop(context.Background(), id)
				return m.refresh()
			}
		}
	}
	return m, nil
}
