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

func (f *fakeClient) ProcessLogs(_ context.Context, id string, _ int) (string, error) {
	if id == "p1" {
		return "› read  src/auth · 214 files\n› plan  rotate session tokens on refresh\n› edit  src/auth/middleware.ts  +9 -3", nil
	}
	if id == "p2" {
		return "› scan  test/auth · 42 suites\n› pass  all 42 test suites passed", nil
	}
	return "", nil
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
	if !strings.Contains(view, "hive") {
		t.Errorf("expected view to contain hive")
	}
	if !strings.Contains(view, "CELL") {
		t.Errorf("expected CELL header")
	}
	if !strings.Contains(view, "still running") {
		t.Errorf("expected still running badge in telemetry")
	}
}

func TestTUI_ParallelAgentsSwitching(t *testing.T) {
	fc := &fakeClient{
		envs: []environment.Environment{
			{ID: "acme/api", Path: "/workspace/acme/api"},
		},
		procs: map[string][]process.Process{
			"acme/api": {
				{ID: "p1", EnvironmentID: "acme/api", Command: "claude", Args: []string{"auth-refactor"}, Status: process.StatusRunning},
				{ID: "p2", EnvironmentID: "acme/api", Command: "codex", Args: []string{"flaky-tests"}, Status: process.StatusRunning},
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
		t.Errorf("expected bee to be StateActive due to running processes, got %s", m.Bee.State)
	}

	// Initial selection is process 0 (claude / auth-refactor)
	if m.SelectedProc != 0 {
		t.Errorf("expected SelectedProc 0, got %d", m.SelectedProc)
	}

	// Update logs for p1
	updated, _ = m.Update(tui.LogsMsg{
		ProcessID: "p1",
		Logs:      "› read  src/auth · 214 files\n› edit  src/auth/middleware.ts  +9 -3",
	})
	m = updated.(tui.Model)

	view := m.View()
	if !strings.Contains(view, "auth-refactor") {
		t.Errorf("expected auth-refactor in view")
	}
	if !strings.Contains(view, "flaky-tests") {
		t.Errorf("expected flaky-tests in view")
	}
	if !strings.Contains(view, "ATTACHED · AUTH-REFACTOR") {
		t.Errorf("expected attached header for auth-refactor")
	}
	if !strings.Contains(view, "src/auth/middleware.ts") {
		t.Errorf("expected claude logs in view")
	}

	// Tab switch to process 1 (codex / flaky-tests)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(tui.Model)
	if m.SelectedProc != 1 {
		t.Errorf("expected SelectedProc 1 after Tab, got %d", m.SelectedProc)
	}
	if cmd == nil {
		t.Errorf("expected fetchLogsCmd to be scheduled")
	}

	// Deliver logs for p2
	updated, _ = m.Update(tui.LogsMsg{
		ProcessID: "p2",
		Logs:      "› scan  test/auth · 42 suites\n› pass  all 42 test suites passed",
	})
	m = updated.(tui.Model)

	view2 := m.View()
	if !strings.Contains(view2, "ATTACHED · FLAKY-TESTS") {
		t.Errorf("expected attached header for flaky-tests")
	}
	if !strings.Contains(view2, "test/auth") {
		t.Errorf("expected codex logs in view")
	}

	// Down arrow wraps or moves
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(tui.Model)
	if m.SelectedProc != 0 { // wraps back to 0
		t.Errorf("expected SelectedProc to wrap to 0, got %d", m.SelectedProc)
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
}
