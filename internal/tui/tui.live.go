package tui

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/vt"
)

// resubscribeDelay is how long the dashboard waits before subscribing to
// events again after the stream ended (e.g. the daemon restarted). It polls
// meanwhile.
const resubscribeDelay = 2 * time.Second

// live holds the daemon streams the dashboard reads: events (instead of
// polling the process list) and the frames of the selected agent's terminal.
// Every copy of the Model shares it; Update is the only writer of the
// fields it reads back.
type live struct {
	c client.Client

	mu      sync.Mutex
	events  *client.EventStream
	view    *viewConn
	gen     int    // bumped whenever the view should change; stale streams are dropped
	opening string // the agent whose stream is being opened
}

// viewConn is the open frame stream of one agent's terminal.
type viewConn struct {
	gen       int
	processID string
	stream    *client.FrameStream
}

// send delivers keystrokes: through the selected agent's frame stream when
// it is open (so the agent's terminal takes the panel's size, as for any
// client that types), otherwise as a plain request.
func (l *live) send(ctx context.Context, processID string, data []byte) error {
	l.mu.Lock()
	v := l.view
	l.mu.Unlock()
	if v != nil && v.processID == processID {
		_, err := v.stream.Write(data)
		return err
	}
	return l.c.TerminalInput(ctx, processID, data)
}

func (l *live) close() {
	l.mu.Lock()
	events, view := l.events, l.view
	l.events, l.view = nil, nil
	l.mu.Unlock()
	if events != nil {
		_ = events.Close()
	}
	if view != nil {
		_ = view.stream.Close()
	}
}

// Messages from the stream commands.
type (
	eventsOpenedMsg struct{ stream *client.EventStream }
	eventsClosedMsg struct{ err error }
	eventMsg        struct{ ev event.Event }
	resubscribeMsg  struct{}
	framesOpenedMsg struct {
		gen       int
		processID string
		stream    *client.FrameStream
		err       error
	}
	frameMsg struct {
		gen       int
		processID string
		frame     *vt.Frame
	}
	framesClosedMsg struct {
		gen       int
		processID string
	}
)

// subscribe opens the event stream.
func (m Model) subscribe() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	es, err := m.Client.Events(ctx, "process.", "environment.")
	if err != nil {
		return eventsClosedMsg{err: err}
	}
	return eventsOpenedMsg{stream: es}
}

func waitEvent(es *client.EventStream) tea.Cmd {
	return func() tea.Msg {
		ev, err := es.Next()
		if err != nil {
			return eventsClosedMsg{err: err}
		}
		return eventMsg{ev: ev}
	}
}

func resubscribeLater() tea.Cmd {
	return tea.Tick(resubscribeDelay, func(time.Time) tea.Msg { return resubscribeMsg{} })
}

func waitFrame(gen int, processID string, fs *client.FrameStream) tea.Cmd {
	return func() tea.Msg {
		f, err := fs.Next()
		if err != nil {
			return framesClosedMsg{gen: gen, processID: processID}
		}
		return frameMsg{gen: gen, processID: processID, frame: f}
	}
}

// updateLive handles the stream messages.
func (m Model) updateLive(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case eventsOpenedMsg:
		m.live.mu.Lock()
		m.live.events = msg.stream
		m.live.mu.Unlock()
		m.eventsActive = true
		return m, tea.Batch(waitEvent(msg.stream), m.refresh), true

	case eventMsg:
		m.live.mu.Lock()
		es := m.live.events
		m.live.mu.Unlock()
		if es == nil {
			return m, nil, true
		}
		var cmd tea.Cmd
		if t := msg.ev.Type; t == event.Lost || strings.HasPrefix(t, "process.") || strings.HasPrefix(t, "environment.") {
			cmd = m.refresh
		}
		return m, tea.Batch(waitEvent(es), cmd), true

	case eventsClosedMsg:
		m.live.mu.Lock()
		m.live.events = nil
		m.live.mu.Unlock()
		m.eventsActive = false // polling takes over until the stream is back
		return m, resubscribeLater(), true

	case resubscribeMsg:
		return m, m.subscribe, true

	case framesOpenedMsg:
		m.live.mu.Lock()
		current := msg.gen == m.live.gen
		if current {
			m.live.opening = ""
		}
		if current && msg.err == nil {
			m.live.view = &viewConn{gen: msg.gen, processID: msg.processID, stream: msg.stream}
		}
		m.live.mu.Unlock()
		if !current {
			if msg.stream != nil {
				_ = msg.stream.Close()
			}
			return m, nil, true
		}
		if msg.err != nil {
			return m, nil, true
		}
		return m, waitFrame(msg.gen, msg.processID, msg.stream), true

	case frameMsg:
		m.live.mu.Lock()
		v := m.live.view
		m.live.mu.Unlock()
		if v == nil || v.gen != msg.gen {
			return m, nil, true // a stream we already replaced
		}
		scr := m.screens[msg.processID]
		if scr == nil {
			scr = &vt.Screen{}
			m.screens[msg.processID] = scr
		}
		scr.Apply(msg.frame)
		return m, waitFrame(msg.gen, msg.processID, v.stream), true

	case framesClosedMsg:
		m.live.mu.Lock()
		if m.live.view != nil && m.live.view.gen == msg.gen {
			m.live.view = nil
		}
		m.live.mu.Unlock()
		return m, m.refresh, true
	}
	return m, nil, false
}

// syncView points the frame stream at the selected agent: open one for a
// running terminal agent, close it otherwise.
func (m Model) syncView() tea.Cmd {
	cur := m.CurrentProcess()
	want := ""
	if cur != nil && cur.Terminal && cur.Status == process.StatusRunning {
		want = cur.ID
	}
	m.live.mu.Lock()
	v := m.live.view
	if v != nil && v.processID == want {
		m.live.mu.Unlock()
		return nil
	}
	// A stream for the wanted agent may already be opening.
	if want != "" && m.live.opening == want {
		m.live.mu.Unlock()
		return nil
	}
	m.live.gen++
	gen := m.live.gen
	m.live.view = nil
	m.live.opening = want
	m.live.mu.Unlock()

	var cmds []tea.Cmd
	if v != nil {
		cmds = append(cmds, func() tea.Msg { _ = v.stream.Close(); return nil })
	}
	if want != "" {
		w, h := logsPanelSize(m.Width, m.Height)
		c := m.Client
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			fs, err := c.TerminalFrames(ctx, client.ViewRequest{ProcessID: want, Width: uint16(w), Height: uint16(h)})
			return framesOpenedMsg{gen: gen, processID: want, stream: fs, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// resizeView tells the open view the panel's new size.
func (m Model) resizeView() tea.Cmd {
	m.live.mu.Lock()
	v := m.live.view
	m.live.mu.Unlock()
	if v == nil {
		return nil
	}
	w, h := logsPanelSize(m.Width, m.Height)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = v.stream.Resize(ctx, uint16(w), uint16(h))
		return nil
	}
}
