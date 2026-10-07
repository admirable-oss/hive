package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

type Focus int

const (
	FocusEnvironments Focus = iota
	FocusProcesses
)

type RefreshMsg struct {
	Connected    bool
	Environments []environment.Environment
	Processes    map[string][]process.Process
	Err          error
}

type Model struct {
	Client       client.Client
	Bee          bee.Model
	Width        int
	Height       int
	Connected    bool
	Environments []environment.Environment
	Processes    map[string][]process.Process
	SelectedEnv  int
	SelectedProc int
	Focus        Focus
	Inspecting   *process.Process
	StatusMsg    string
	Quitting     bool
}

func NewModel(c client.Client) Model {
	return Model{
		Client:       c,
		Bee:          bee.New(),
		Width:        80,
		Height:       24,
		Processes:    make(map[string][]process.Process),
		Focus:        FocusEnvironments,
		SelectedEnv:  0,
		SelectedProc: 0,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		bee.Tick(m.Bee),
		m.fetchDataCmd(),
	)
}

func (m Model) fetchDataCmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
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
