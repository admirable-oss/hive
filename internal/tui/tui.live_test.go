package tui_test

import (
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui"
	"github.com/admirable-oss/hive/internal/vt"
)

// run executes cmd and every command it batches, feeding the resulting
// messages back into m, until nothing is left or the deadline passes. It
// returns the final model. Commands that block (stream reads) are run with
// a timeout and dropped if they do not finish.
func run(t *testing.T, m tea.Model, cmd tea.Cmd, steps int) tea.Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; i < steps && len(queue) > 0; i++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(300 * time.Millisecond):
			continue // a stream waiting for more data
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		queue = append(queue, next)
	}
	return m
}

func TestTUI_EventsDriveRefreshes(t *testing.T) {
	server, clientEnd := net.Pipe()
	defer server.Close()
	fc := &fakeClient{events: func() (*client.EventStream, error) { return client.NewEventStream(clientEnd), nil }}
	m := tui.NewModel(fc)
	defer m.Close()

	// Subscribing: nothing listed yet.
	model := run(t, m, m.Init(), 20)
	if len(model.(tui.Model).Processes) != 0 {
		t.Fatal("no processes yet")
	}

	// A process starts; the event makes the dashboard re-read the list.
	fc.procs = []process.Process{{ID: "p1", EnvironmentID: "env", Status: process.StatusRunning, Command: "agent"}}
	go func() {
		line, _ := json.Marshal(event.Event{Seq: 1, Type: event.ProcessStarted})
		_, _ = server.Write(append(line, '\n'))
	}()
	model = run(t, model, func() tea.Msg { return nil }, 1)
	// Feed the pending waitEvent command by re-running Init's stream reader.
	deadline := time.Now().Add(3 * time.Second)
	for len(model.(tui.Model).Processes) == 0 && time.Now().Before(deadline) {
		model = run(t, model, model.(tui.Model).Init(), 30)
	}
	if got := model.(tui.Model).Processes; len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("processes after the event = %+v", got)
	}
}

// frameServer serves a frame stream over a pipe: it sends the given screen
// text as a keyframe and records what the dashboard types.
func frameServer(t *testing.T, text string) (func() (*client.FrameStream, error), <-chan []byte) {
	t.Helper()
	typed := make(chan []byte, 16)
	return func() (*client.FrameStream, error) {
		server, clientEnd := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		go func() {
			term := vt.New(40, 6, vt.Options{})
			defer term.Close()
			_, _ = term.Write([]byte(text))
			var v vt.View
			if _, err := server.Write(vt.AppendFrame(nil, term.Frame(&v))); err != nil {
				return
			}
			buf := make([]byte, 64)
			for {
				n, err := server.Read(buf)
				if n > 0 {
					typed <- append([]byte(nil), buf[:n]...)
				}
				if err != nil {
					return
				}
			}
		}()
		return client.NewFrameStream(clientEnd, client.View{ID: "v1", Width: 40, Height: 6}), nil
	}, typed
}

func TestTUI_SelectedAgentScreenStreamsIn(t *testing.T) {
	frames, typed := frameServer(t, "\x1b[32mclaude>\x1b[0m refactoring auth")
	fc := &fakeClient{frames: frames}
	fc.procs = []process.Process{{ID: "p1", EnvironmentID: "env", Status: process.StatusRunning, Terminal: true, Command: "claude"}}
	m := tui.NewModel(fc)
	defer m.Close()

	var model tea.Model = m
	model, cmd := model.Update(tui.RefreshMsg{Connected: true, Processes: fc.procs})
	model = run(t, model, cmd, 30)
	if view := model.View(); !strings.Contains(view, "claude> refactoring auth") && !strings.Contains(stripANSI(view), "claude> refactoring auth") {
		t.Fatalf("the panel should show the agent's screen:\n%s", stripANSI(view))
	}

	// Typing goes through the open stream, in order.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "ls" {
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model.(tui.Model).Close()
	var got []byte
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case b := <-typed:
			got = append(got, b...)
		case <-timeout:
			t.Fatalf("typed %q through the stream, want %q", got, "ls")
		}
	}
	if string(got) != "ls" {
		t.Fatalf("typed %q, want %q", got, "ls")
	}
}

func TestTUI_StaleFrameStreamsAreIgnored(t *testing.T) {
	frames, _ := frameServer(t, "first agent")
	fc := &fakeClient{frames: frames}
	fc.procs = []process.Process{
		{ID: "p1", EnvironmentID: "env", Status: process.StatusRunning, Terminal: true},
		{ID: "p2", EnvironmentID: "env", Status: process.StatusRunning, Terminal: true},
	}
	m := tui.NewModel(fc)
	defer m.Close()
	var model tea.Model = m
	model, _ = model.Update(tui.RefreshMsg{Connected: true, Processes: fc.procs})
	// The selection moves on before the first stream's frames arrive; a
	// frame for an outdated stream must not paint the new selection.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(model.View()), "first agent") {
		t.Fatal("a stale stream painted the panel")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == 0x1b:
			in = true
		case in && (s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z'):
			in = false
		case !in:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

var _ io.Reader = (*client.Attachment)(nil)
