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

	case LogPollTickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, m.pollLogsTickCmd())
		cmds = append(cmds, m.fetchDataCmd())
		if cur := m.CurrentProcess(); cur != nil {
			cmds = append(cmds, m.fetchLogsCmd(cur.ID))
		}
		return m, tea.Batch(cmds...)

	case LogsMsg:
		if msg.ProcessID != "" {
			m.LogCache[msg.ProcessID] = msg.Logs
			if cur := m.CurrentProcess(); cur != nil && cur.ID == msg.ProcessID {
				m.ActiveLogs = msg.Logs
			}
		}
		return m, nil

	case RefreshMsg:
		m.Connected = msg.Connected
		if msg.Connected {
			m.Environments = msg.Environments
			m.Processes = msg.Processes

			// Flatten all processes
			var all []process.Process
			hasRunning := false
			for _, env := range m.Environments {
				for _, p := range m.Processes[env.ID] {
					all = append(all, p)
					if p.Status == process.StatusRunning {
						hasRunning = true
					}
				}
			}
			m.AllProcesses = all

			if hasRunning {
				m.Bee.SetState(bee.StateActive)
			} else {
				m.Bee.SetState(bee.StateIdle)
			}
		} else {
			m.Bee.SetState(bee.StateDisconnected)
		}

		if len(m.AllProcesses) > 0 {
			if m.SelectedProc >= len(m.AllProcesses) {
				m.SelectedProc = len(m.AllProcesses) - 1
			}
			if m.SelectedProc < 0 {
				m.SelectedProc = 0
			}
			if cur := m.CurrentProcess(); cur != nil {
				m.ActiveLogs = m.LogCache[cur.ID]
			}
		}
		return m, nil

	case AttachFinishedMsg:
		return m, m.fetchDataCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.Quitting = true
			return m, tea.Quit

		case "up", "k":
			if len(m.AllProcesses) > 0 {
				if m.SelectedProc > 0 {
					m.SelectedProc--
				} else {
					m.SelectedProc = len(m.AllProcesses) - 1
				}
				if cur := m.CurrentProcess(); cur != nil {
					m.ActiveLogs = m.LogCache[cur.ID]
					return m, m.fetchLogsCmd(cur.ID)
				}
			}
			return m, nil

		case "down", "j":
			if len(m.AllProcesses) > 0 {
				if m.SelectedProc < len(m.AllProcesses)-1 {
					m.SelectedProc++
				} else {
					m.SelectedProc = 0
				}
				if cur := m.CurrentProcess(); cur != nil {
					m.ActiveLogs = m.LogCache[cur.ID]
					return m, m.fetchLogsCmd(cur.ID)
				}
			}
			return m, nil

		case "tab":
			if len(m.AllProcesses) > 0 {
				m.SelectedProc = (m.SelectedProc + 1) % len(m.AllProcesses)
				if cur := m.CurrentProcess(); cur != nil {
					m.ActiveLogs = m.LogCache[cur.ID]
					return m, m.fetchLogsCmd(cur.ID)
				}
			}
			return m, nil

		case "shift+tab":
			if len(m.AllProcesses) > 0 {
				m.SelectedProc = (m.SelectedProc - 1 + len(m.AllProcesses)) % len(m.AllProcesses)
				if cur := m.CurrentProcess(); cur != nil {
					m.ActiveLogs = m.LogCache[cur.ID]
					return m, m.fetchLogsCmd(cur.ID)
				}
			}
			return m, nil

		case "r":
			return m, m.fetchDataCmd()

		case "enter", "a":
			if cur := m.CurrentProcess(); cur != nil && cur.Status == process.StatusRunning {
				cmd := exec.Command(os.Args[0], "terminal", "attach", cur.ID)
				return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
					return AttachFinishedMsg{Err: err}
				})
			}

		case "s":
			if cur := m.CurrentProcess(); cur != nil && cur.Status == process.StatusRunning {
				procID := cur.ID
				return m, func() tea.Msg {
					_ = m.Client.ProcessStop(context.Background(), procID)
					return RefreshMsg{Connected: true}
				}
			}
		}
	}

	return m, nil
}
