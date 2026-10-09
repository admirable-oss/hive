package tui_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/x/ansi"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui"
	"github.com/admirable-oss/hive/internal/tui/bee"
)

type fakeClient struct {
	pingErr   error
	envs      []environment.Environment
	procs     []process.Process
	stoppedID string

	inputMu    sync.Mutex
	inputs     map[string][]byte // keystrokes received per process, in arrival order
	inputCalls int
	inputErr   error
	inputDelay func() // runs before each delivery is recorded; simulates latency

	events func() (*client.EventStream, error)
	frames func() (*client.FrameStream, error)
}

func (f *fakeClient) Ping(_ context.Context) error {
	return f.pingErr
}

func (f *fakeClient) Status(_ context.Context) (client.Status, error) {
	return client.Status{Status: "running"}, nil
}

func (f *fakeClient) Shutdown(context.Context, bool) error { return nil }

func (f *fakeClient) Close() error { return nil }

func (f *fakeClient) TerminalFrames(context.Context, client.ViewRequest) (*client.FrameStream, error) {
	if f.frames != nil {
		return f.frames()
	}
	return nil, errors.New("no frames in this fake")
}

func (f *fakeClient) TerminalSnapshot(context.Context, client.SnapshotRequest) (client.Snapshot, error) {
	return client.Snapshot{}, nil
}

func (f *fakeClient) Events(context.Context, ...string) (*client.EventStream, error) {
	if f.events != nil {
		return f.events()
	}
	return nil, errors.New("no events in this fake")
}

func (f *fakeClient) EnvironmentList(_ context.Context) ([]environment.Environment, error) {
	return f.envs, nil
}

func (f *fakeClient) EnvironmentCreate(_ context.Context, req environment.CreateRequest) (environment.Environment, error) {
	env := environment.Environment{ID: req.ID, Path: "/tmp/" + req.ID}
	f.envs = append(f.envs, env)
	return env, nil
}

func (f *fakeClient) EnvironmentUpdate(_ context.Context, req environment.UpdateRequest) (environment.Environment, error) {
	return f.EnvironmentGet(context.Background(), req.ID)
}

func (f *fakeClient) Call(context.Context, string, any, any) error {
	return errors.New("no workspace API in this fake")
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

func (f *fakeClient) ProcessStart(_ context.Context, req process.StartRequest) (process.Process, error) {
	return process.Process{ID: "p1", EnvironmentID: req.EnvironmentID, Command: req.Command, Args: req.Args, Status: process.StatusRunning}, nil
}

func (f *fakeClient) ProcessGet(_ context.Context, id string) (process.Process, error) {
	return process.Process{ID: id, Status: process.StatusRunning}, nil
}

func (f *fakeClient) ProcessList(_ context.Context, envID string) ([]process.Process, error) {
	var out []process.Process
	for _, p := range f.procs {
		if envID == "" || p.EnvironmentID == envID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeClient) ProcessStop(_ context.Context, id string) error {
	f.stoppedID = id
	return nil
}

func (f *fakeClient) ProcessLogs(_ context.Context, req process.LogsRequest) (client.Logs, error) {
	switch req.ID {
	case "p1":
		return client.Logs{Logs: "› read  src/auth · 214 files\n› plan  rotate session tokens on refresh\n› edit  src/auth/middleware.ts  +9 -3"}, nil
	case "p2":
		return client.Logs{Logs: "› scan  test/auth · 42 suites\n› pass  all 42 test suites passed"}, nil
	}
	return client.Logs{}, nil
}

func (f *fakeClient) ProcessLogsStream(context.Context, process.LogsRequest, io.Writer) error {
	return nil
}

func (f *fakeClient) TerminalAttach(context.Context, client.ViewRequest) (*client.Attachment, error) {
	return nil, errors.New("no attach in this fake")
}

func (f *fakeClient) TerminalResize(_ context.Context, _ string, _, _ uint16) error {
	return nil
}

func (f *fakeClient) TerminalInput(_ context.Context, id string, data []byte) error {
	if f.inputDelay != nil {
		f.inputDelay()
	}
	f.inputMu.Lock()
	defer f.inputMu.Unlock()
	if f.inputErr != nil {
		return f.inputErr
	}
	if f.inputs == nil {
		f.inputs = map[string][]byte{}
	}
	f.inputs[id] = append(f.inputs[id], data...)
	f.inputCalls++
	return nil
}

func (f *fakeClient) input(id string) string {
	f.inputMu.Lock()
	defer f.inputMu.Unlock()
	return string(f.inputs[id])
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
	if !strings.Contains(view, "uptime") {
		t.Errorf("expected uptime in telemetry")
	}
}

func TestTUI_ParallelAgentsSwitching(t *testing.T) {
	fc := &fakeClient{
		envs: []environment.Environment{
			{ID: "acme/api", Path: "/workspace/acme/api"},
		},
		procs: []process.Process{
			{ID: "p1", EnvironmentID: "acme/api", Command: "claude", Args: []string{"auth-refactor"}, Status: process.StatusRunning},
			{ID: "p2", EnvironmentID: "acme/api", Command: "codex", Args: []string{"flaky-tests"}, Status: process.StatusRunning},
		},
	}

	m := tui.NewModel(fc)

	// Refresh message received
	updated, _ := m.Update(tui.RefreshMsg{
		Connected: true,
		Processes: fc.procs,
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

func TestTUI_EnterKeySafe(t *testing.T) {
	fc := &fakeClient{
		envs: []environment.Environment{{ID: "env1"}},
		procs: []process.Process{
			{ID: "p1", Command: "claude", Status: process.StatusRunning},
			{ID: "p2", Command: "codex", Status: process.StatusRunning},
		},
	}
	m := tui.NewModel(fc)
	updated, _ := m.Update(tui.RefreshMsg{
		Connected: true,
		Processes: fc.procs,
	})
	m = updated.(tui.Model)

	if m.SelectedProc != 0 {
		t.Fatalf("expected initial SelectedProc 0, got %d", m.SelectedProc)
	}

	// Pressing Enter on running process enters Interactive mode
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(tui.Model)
	if !m.Interactive {
		t.Errorf("expected m.Interactive to be true after Enter on running process")
	}
	if cmd == nil {
		t.Errorf("expected fetchLogsCmd after Enter")
	}

	// In interactive mode, view highlights the panel
	v := m.View()
	if !strings.Contains(v, "INPUT ACTIVE") && !strings.Contains(v, "INTERACTIVE") {
		t.Errorf("expected view to indicate interactive control")
	}

	// Sending a keystroke forwards input
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(tui.Model)
	if cmd == nil {
		t.Errorf("expected cmd after sending interactive key")
	}

	// Pressing Esc exits interactive mode
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(tui.Model)
	if m.Interactive {
		t.Errorf("expected m.Interactive to be false after Esc")
	}
}

func TestTUI_ResponsiveTerminalSizes(t *testing.T) {
	fc := &fakeClient{
		envs: []environment.Environment{{ID: "acme-api"}},
		procs: []process.Process{
			{ID: "p1", Command: "claude", Args: []string{"auth-refactor"}, Status: process.StatusRunning},
			{ID: "p2", Command: "codex", Args: []string{"flaky-tests"}, Status: process.StatusRunning},
		},
	}
	m := tui.NewModel(fc)
	updated, _ := m.Update(tui.RefreshMsg{
		Connected: true,
		Processes: fc.procs,
	})
	m = updated.(tui.Model)

	// Test various terminal dimensions
	sizes := []struct {
		w int
		h int
	}{
		{80, 24},
		{100, 30},
		{130, 40},
		{160, 50},
	}

	for _, sz := range sizes {
		m.Width = sz.w
		m.Height = sz.h
		view := m.View()

		if view == "" {
			t.Errorf("expected non-empty view for %dx%d", sz.w, sz.h)
		}

		lines := strings.Split(view, "\n")
		for lineIdx, line := range lines {
			w := ansi.StringWidth(line)
			if w > sz.w {
				t.Errorf("size %dx%d line %d exceeds width: got %d, max %d: %q", sz.w, sz.h, lineIdx, w, sz.w, line)
			}
		}
	}
}
