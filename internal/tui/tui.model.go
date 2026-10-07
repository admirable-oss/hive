package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

const (
	pollInterval = 500 * time.Millisecond
	logTail      = 30 // lines fetched for the log panel
)

// RefreshMsg carries a fresh process list from the daemon.
type RefreshMsg struct {
	Connected bool
	Processes []process.Process
	Err       error
}

// LogsMsg carries the recent output of one process.
type LogsMsg struct {
	ProcessID string
	Logs      string
}

type pollTickMsg struct{}

type attachFinishedMsg struct{ Err error }

// Model is the dashboard state. Bubble Tea passes it by value, so every
// update returns a new copy; the maps are shared, which is fine because only
// Update writes to them.
type Model struct {
	Client        client.Client
	Bee           bee.Model
	Width         int
	Height        int
	Connected     bool
	Processes     []process.Process // every agent, oldest first
	SelectedProc  int               // index into Processes
	Interactive   bool              // keystrokes go to the selected agent's terminal
	Quitting      bool
	WorkspacePath string
	StartTime     time.Time

	selectedID string              // keeps the selection on the same agent across refreshes
	logLines   map[string][]string // sanitised output per process ID
}

func NewModel(c client.Client) Model {
	return Model{
		Client:        c,
		Bee:           bee.New(),
		Width:         100,
		Height:        28,
		WorkspacePath: displayCwd(),
		StartTime:     time.Now(),
		logLines:      make(map[string][]string),
	}
}

// displayCwd shows the working directory with $HOME collapsed to ~.
func displayCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "~"
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(cwd, home) {
		return "~" + strings.TrimPrefix(cwd, home)
	}
	return filepath.Base(cwd)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(bee.Tick(m.Bee), m.refresh, pollTick())
}

// refresh is a tea.Cmd that fetches every process in one call. A failed call
// means the daemon is unreachable.
func (m Model) refresh() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	procs, err := m.Client.ProcessList(ctx, "")
	if err != nil {
		return RefreshMsg{Err: err}
	}
	return RefreshMsg{Connected: true, Processes: procs}
}

func (m Model) fetchLogsCmd(procID string) tea.Cmd {
	if procID == "" {
		return nil
	}
	c := m.Client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		logs, _ := c.ProcessLogs(ctx, procID, logTail)
		return LogsMsg{ProcessID: procID, Logs: logs}
	}
}

func pollTick() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return pollTickMsg{} })
}

// CurrentProcess returns the selected process, or nil when there is none.
func (m Model) CurrentProcess() *process.Process {
	if m.SelectedProc < 0 || m.SelectedProc >= len(m.Processes) {
		return nil
	}
	return &m.Processes[m.SelectedProc]
}

// selectIndex moves the selection (wrapping around) and fetches its logs.
func (m *Model) selectIndex(i int) tea.Cmd {
	n := len(m.Processes)
	if n == 0 {
		return nil
	}
	m.SelectedProc = (i%n + n) % n
	m.selectedID = m.Processes[m.SelectedProc].ID
	return m.fetchLogsCmd(m.selectedID)
}

// reanchor keeps the selection on the same agent after the list changes;
// otherwise a new agent would shift rows and steal the selection (and, in
// interactive mode, the user's keystrokes).
func (m *Model) reanchor() {
	for i, p := range m.Processes {
		if p.ID == m.selectedID {
			m.SelectedProc = i
			return
		}
	}
	if len(m.Processes) == 0 {
		m.SelectedProc, m.selectedID = 0, ""
		return
	}
	m.SelectedProc = min(max(m.SelectedProc, 0), len(m.Processes)-1)
	m.selectedID = m.Processes[m.SelectedProc].ID
}
