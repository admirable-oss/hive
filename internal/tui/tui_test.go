package tui_test

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

type fakeClient struct {
	pingErr   error
	envs      []environment.Environment
	procs     map[string][]process.Process
	stoppedID string
}

func (f *fakeClient) Ping(_ context.Context) error {
	return f.pingErr
}

func (f *fakeClient) Status(_ context.Context) (client.Status, error) {
	return client.Status{Status: "running"}, nil
}

func (f *fakeClient) Shutdown(_ context.Context) error {
	return nil
}

func (f *fakeClient) EnvironmentList(_ context.Context) ([]environment.Environment, error) {
	return f.envs, nil
}

func (f *fakeClient) EnvironmentCreate(_ context.Context, id string) (environment.Environment, error) {
	env := environment.Environment{ID: id, Path: "/tmp/" + id}
	f.envs = append(f.envs, env)
	return env, nil
}

func (f *fakeClient) EnvironmentGet(_ context.Context, id string) (environment.Environment, error) {
	for _, e := range f.envs {
		if e.ID == id {
			return e, nil
		}
	}
	return environment.Environment{}, nil
}

func (f *fakeClient) EnvironmentRemove(_ context.Context, _ string) error {
	return nil
}

func (f *fakeClient) ProcessStart(_ context.Context, envID, command string, args []string) (process.Process, error) {
	return process.Process{ID: "p1", EnvironmentID: envID, Command: command, Args: args, Status: process.StatusRunning}, nil
}

func (f *fakeClient) ProcessStartRequest(_ context.Context, req process.StartRequest) (process.Process, error) {
	return process.Process{ID: "p1", EnvironmentID: req.EnvironmentID, Command: req.Command, Args: req.Args, Status: process.StatusRunning}, nil
}

func (f *fakeClient) ProcessGet(_ context.Context, id string) (process.Process, error) {
	return process.Process{ID: id, Status: process.StatusRunning}, nil
}

func (f *fakeClient) ProcessList(_ context.Context, envID string) ([]process.Process, error) {
	return f.procs[envID], nil
}

func (f *fakeClient) ProcessStop(_ context.Context, id string) error {
	f.stoppedID = id
	return nil
}

func (f *fakeClient) TerminalAttach(_ context.Context, _ string, _ io.Reader, _ io.Writer) error {
	return nil
}

func (f *fakeClient) TerminalResize(_ context.Context, _ string, _, _ uint16) error {
	return nil
}

func (f *fakeClient) TerminalInput(_ context.Context, _ string, _ []byte) error {
	return nil
}

func TestTUI_EmptyState(t *testing.T) {
	fc := &fakeClient{}
	m := tui.NewModel(fc)

	view := m.View()
	if !strings.Contains(view, "HIVE 0.0.1") {
		t.Errorf("expected view to contain HIVE 0.0.1")
	}
	if !strings.Contains(view, "The hive is empty.") {
		t.Errorf("expected empty state message")
	}
}

func TestTUI_ColumnsAndNavigation(t *testing.T) {
	fc := &fakeClient{
		envs: []environment.Environment{
			{ID: "flyrank", Path: "/workspace/flyrank"},
			{ID: "admirable", Path: "/workspace/admirable"},
		},
		procs: map[string][]process.Process{
			"flyrank": {
				{ID: "p1", EnvironmentID: "flyrank", Command: "claude", Status: process.StatusRunning},
				{ID: "p2", EnvironmentID: "flyrank", Command: "tests", Status: process.StatusExited},
			},
		},
	}

	m := tui.NewModel(fc)

	// Refresh message received
	updated, _ := m.Update(tui.RefreshMsg{
		Connected:    true,
		Environments: fc.envs,
		Processes:    fc.procs,
	})
	m = updated.(tui.Model)

	if !m.Connected {
		t.Errorf("expected model to be connected")
	}
	if m.Bee.State != bee.StateActive {
		t.Errorf("expected bee to be StateActive due to running process, got %s", m.Bee.State)
	}

	view := m.View()
	if !strings.Contains(view, "ENVIRONMENTS") {
		t.Errorf("expected ENVIRONMENTS header")
	}
	if !strings.Contains(view, "PROCESSES") {
		t.Errorf("expected PROCESSES header")
	}
	if !strings.Contains(view, "flyrank") {
		t.Errorf("expected flyrank in view")
	}
	if !strings.Contains(view, "claude") {
		t.Errorf("expected claude in view")
	}
	if !strings.Contains(view, "running") {
		t.Errorf("expected running status in view")
	}

	// Move down environment
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(tui.Model)
	if m.SelectedEnv != 1 {
		t.Errorf("expected SelectedEnv 1, got %d", m.SelectedEnv)
	}

	// Move back up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(tui.Model)
	if m.SelectedEnv != 0 {
		t.Errorf("expected SelectedEnv 0, got %d", m.SelectedEnv)
	}

	// Switch focus to processes using Tab
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(tui.Model)
	if m.Focus != tui.FocusProcesses {
		t.Errorf("expected FocusProcesses after Tab")
	}

	// Select next process
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(tui.Model)
	if m.SelectedProc != 1 {
		t.Errorf("expected SelectedProc 1, got %d", m.SelectedProc)
	}

	// Inspect process on Enter
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(tui.Model)
	if m.Inspecting == nil {
		t.Fatalf("expected inspecting process to not be nil")
	}
	if m.Inspecting.ID != "p2" {
		t.Errorf("expected inspecting process p2, got %s", m.Inspecting.ID)
	}

	inspectView := m.View()
	if !strings.Contains(inspectView, "PROCESS DETAIL") {
		t.Errorf("expected PROCESS DETAIL in view")
	}

	// Esc exits inspection
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(tui.Model)
	if m.Inspecting != nil {
		t.Errorf("expected inspecting to be nil after esc")
	}

	// Esc returns focus to environments
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(tui.Model)
	if m.Focus != tui.FocusEnvironments {
		t.Errorf("expected FocusEnvironments after second esc")
	}
}

func TestTUI_DisconnectedBeeState(t *testing.T) {
	fc := &fakeClient{}
	m := tui.NewModel(fc)

	updated, _ := m.Update(tui.RefreshMsg{
		Connected: false,
	})
	m = updated.(tui.Model)

	if m.Bee.State != bee.StateDisconnected {
		t.Errorf("expected bee to be StateDisconnected, got %s", m.Bee.State)
	}

	view := m.View()
	if !strings.Contains(view, "disconnected") {
		t.Errorf("expected disconnected message in footer")
	}
}
