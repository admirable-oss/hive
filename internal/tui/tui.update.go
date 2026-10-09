package tui

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, ok := m.updateLive(msg); ok {
		return next, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width, m.Height = msg.Width, msg.Height
		return m, m.resizeView()

	case bee.TickMsg:
		m.Bee.Tick()
		return m, bee.Tick(m.Bee)

	case pollTickMsg:
		if err := m.input.TakeErr(); err != nil {
			m.notice, m.noticeAt = "keystrokes not delivered: "+err.Error(), time.Now()
		} else if m.notice != "" && time.Since(m.noticeAt) > noticeTTL {
			m.notice = ""
		}
		var refresh tea.Cmd
		if !m.eventsActive {
			refresh = m.refresh // events are down: poll
		}
		return m, tea.Batch(pollTick(), refresh, m.logsCmd())

	case LogsMsg:
		if msg.ProcessID != "" {
			m.logLines[msg.ProcessID] = sanitizeLog(msg.Logs)
		}
		return m, nil

	case RefreshMsg:
		m = m.applyRefresh(msg)
		return m, tea.Batch(m.syncView(), m.logsCmd())

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
	m.input.Enqueue(cur.ID, data)
	return m, m.logsCmd()
}

func (m Model) updateNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.CurrentProcess()
	running := cur != nil && cur.Status == process.StatusRunning

	switch msg.String() {
	case "ctrl+c", "q":
		m.Quitting = true
		return m, tea.Quit

	case "up", "k", "shift+tab":
		cmd := m.selectIndex(m.SelectedProc - 1) // mutates m; must run before m is returned
		return m, cmd

	case "down", "j", "tab":
		cmd := m.selectIndex(m.SelectedProc + 1)
		return m, cmd

	case "r":
		return m, m.refresh

	case "enter":
		if running {
			m.Interactive = true
			return m, tea.Batch(m.syncView(), m.logsCmd())
		}
		cmd := m.selectIndex(m.SelectedProc + 1)
		return m, cmd

	case "a":
		// Full-screen attach: hand the terminal to `hive terminal attach`.
		if running {
			exe, err := os.Executable()
			if err != nil {
				exe = os.Args[0]
			}
			// exe is this binary and the ID came from the daemon (16 hex chars).
			attach := exec.Command(exe, "terminal", "attach", cur.ID) //nolint:gosec,noctx // see above; the attach ends with the user
			return m, tea.ExecProcess(attach, func(err error) tea.Msg {
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
