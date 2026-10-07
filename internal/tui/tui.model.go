package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

type RefreshMsg struct {
	Connected    bool
	Environments []environment.Environment
	Processes    map[string][]process.Process
	Err          error
}

type LogsMsg struct {
	ProcessID string
	Logs      string
}

type LogPollTickMsg struct{}

type Model struct {
	Client        client.Client
	Bee           bee.Model
	Width         int
	Height        int
	Connected     bool
	Environments  []environment.Environment
	Processes     map[string][]process.Process
	AllProcesses  []process.Process
	SelectedProc  int
	SelectedEnv   int
	ActiveLogs    string
	LogCache      map[string]string
	WorkspacePath string
	StartTime     time.Time
	Quitting      bool
}

func NewModel(c client.Client) Model {
	cwd, err := os.Getwd()
	displayPath := "~/acme/api"
	if err == nil {
		home, _ := os.UserHomeDir()
		if home != "" && strings.HasPrefix(cwd, home) {
			displayPath = "~" + strings.TrimPrefix(cwd, home)
		} else {
			displayPath = filepath.Base(cwd)
		}
	}

	return Model{
		Client:        c,
		Bee:           bee.New(),
		Width:         100,
		Height:        28,
		Processes:     make(map[string][]process.Process),
		LogCache:      make(map[string]string),
		WorkspacePath: displayPath,
		StartTime:     time.Now(),
		SelectedProc:  0,
		SelectedEnv:   0,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		bee.Tick(m.Bee),
		m.fetchDataCmd(),
		m.pollLogsTickCmd(),
	)
}

func (m Model) fetchDataCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := m.Client.Ping(ctx); err != nil {
			return RefreshMsg{Connected: false, Err: err}
		}

		envs, err := m.Client.EnvironmentList(ctx)
		if err != nil {
			return RefreshMsg{Connected: true, Err: err}
		}

		procs := make(map[string][]process.Process)
		for _, env := range envs {
			list, _ := m.Client.ProcessList(ctx, env.ID)
			procs[env.ID] = list
		}

		return RefreshMsg{
			Connected:    true,
			Environments: envs,
			Processes:    procs,
		}
	}
}

func (m Model) fetchLogsCmd(procID string) tea.Cmd {
	return func() tea.Msg {
		if procID == "" {
			return LogsMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		logs, err := m.Client.ProcessLogs(ctx, procID, 30)
		if err != nil {
			return LogsMsg{ProcessID: procID, Logs: ""}
		}
		return LogsMsg{ProcessID: procID, Logs: logs}
	}
}

func (m Model) pollLogsTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(_ time.Time) tea.Msg {
		return LogPollTickMsg{}
	})
}

// CurrentProcess returns the currently selected process.
func (m Model) CurrentProcess() *process.Process {
	if len(m.AllProcesses) == 0 {
		return nil
	}
	idx := m.SelectedProc
	if idx < 0 || idx >= len(m.AllProcesses) {
		idx = 0
	}
	return &m.AllProcesses[idx]
}
