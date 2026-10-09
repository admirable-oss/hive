package mux

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/tui/theme"
)

// Timeouts for daemon calls made by the UI.
const (
	callTimeout = 5 * time.Second
	loadTimeout = 5 * time.Second
)

// Msg is the result of background work, handled on the loop goroutine.
type Msg any

// App is the multiplexer's state and logic. One goroutine (the loop) owns
// every field below the channels: it handles terminal events and the
// messages background work posts back, and draws frames. Work that talks
// to the daemon runs in goroutines started by spawn, so the loop never
// waits on the network.
type App struct {
	c    client.Client
	api  client.Workspace
	opts Options
	km   *keymap.Keymap
	comp *compositor.Compositor
	now  func() time.Time

	ctx     context.Context
	cancel  context.CancelFunc
	msgs    chan Msg
	wake    chan struct{} // a pane's screen changed; buffered, coalesced
	bg      sync.WaitGroup
	pending atomic.Int64 // spawned work not yet posted back (tests settle on it)

	// Owned by the loop goroutine.
	theme         theme.Theme
	width, height int
	sized         bool // the terminal reported its size
	ws            workspaceState
	ui            uiState
	views         map[string]*view // live screens, by process ID
	cache         map[string]*screenCache
	out           []string // raw sequences for the outer terminal (OSC 52)
	last          compositor.Result
	dirty         bool
	urgent        bool      // the next frame answers input: draw it now
	lastInput     time.Time // the last key, paste or click
	quit          bool
}

// uiState is what the user is doing: mode, overlays, selections.
type uiState struct {
	mode      keymap.Mode
	prefixKey uv.Key // the key that armed prefix mode, for send-prefix
	copy      *copyState
	overlay   overlay
	overview  *overview
	toasts    []toast
	clip      string // the last copied text, for paste

	sidebar   bool
	sidebarW  int
	sidebarAt int             // keyboard selection while the sidebar fills the screen
	collapsed map[string]bool // sidebar panels

	mouse mouseState
}

// New returns an App for c; its background work ends with ctx. Call
// Start before handling events and Close when done.
func New(ctx context.Context, c client.Client, opts Options) *App {
	opts = opts.withDefaults()
	ctx, cancel := context.WithCancel(ctx)
	a := &App{
		c:      c,
		api:    client.NewWorkspace(c),
		opts:   opts,
		km:     opts.Keymap,
		comp:   compositor.New(),
		now:    time.Now,
		ctx:    ctx,
		cancel: cancel,
		msgs:   make(chan Msg, 256),
		wake:   make(chan struct{}, 1),
		views:  map[string]*view{},
		cache:  map[string]*screenCache{},
		width:  80,
		height: 24,
		dirty:  true,
	}
	a.theme = opts.resolveTheme(true)
	a.ui.mode = keymap.ModeTerminal
	a.ui.sidebar = !opts.HideSidebar
	a.ui.sidebarW = opts.SidebarWidth
	a.ui.collapsed = map[string]bool{}
	a.ws.claims = map[string]size{}
	a.ws.claiming = map[string]bool{}
	for _, w := range opts.Warnings {
		a.toast(compositor.ToastWarning, w)
	}
	return a
}

// Start subscribes to daemon events and reads the workspace.
func (a *App) Start() {
	a.subscribe()
	a.refresh()
}

// Close stops background work and every pane stream, and waits for them.
func (a *App) Close() {
	a.cancel()
	for id, v := range a.views {
		v.close()
		delete(a.views, id)
	}
	a.bg.Wait()
}

// Msgs delivers the results of background work; the loop passes each to
// Handle.
func (a *App) Msgs() <-chan Msg { return a.msgs }

// Wake fires when a pane's screen changed and a frame is due.
func (a *App) Wake() <-chan struct{} { return a.wake }

// Quit reports whether the user asked to leave.
func (a *App) Quit() bool { return a.quit }

// spawn runs f in the background and hands its result to the loop. A nil
// result posts nothing.
func (a *App) spawn(f func(ctx context.Context) Msg) {
	a.pending.Add(1)
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		defer a.pending.Add(-1)
		if m := f(a.ctx); m != nil {
			a.post(m)
		}
	}()
}

// post hands m to the loop, unless the app is closing.
func (a *App) post(m Msg) {
	select {
	case a.msgs <- m:
	case <-a.ctx.Done():
	}
}

// after posts m to the loop after d. Timers are not pending work: Settle
// does not wait for them.
func (a *App) after(d time.Duration, m Msg) {
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			a.post(m)
		case <-a.ctx.Done():
		}
	}()
}

// signal tells the loop a pane's screen changed.
func (a *App) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// callDoneMsg reports a finished daemon call.
type callDoneMsg struct {
	what string
	err  error
	done func(error)
}

// call runs a workspace change in the background. Failures become error
// toasts; either way done (if set) runs on the loop, and the workspace is
// read again, so the screen follows even when events are not arriving.
func (a *App) call(what string, f func(ctx context.Context) error, done func(error)) {
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		return callDoneMsg{what: what, err: f(ctx), done: done}
	})
}

// Handle processes one message from Msgs.
func (a *App) Handle(m Msg) {
	a.dirty = true
	switch m := m.(type) {
	case callDoneMsg:
		if m.err != nil && a.ctx.Err() == nil {
			a.toast(compositor.ToastError, m.what+": "+errText(m.err))
		}
		if m.done != nil {
			m.done(m.err)
		}
		a.refresh()
	case loadedMsg:
		a.loaded(m)
	case eventsMsg:
		a.handleEvents(m)
	case claimedMsg:
		delete(a.ws.claiming, m.tab)
		if m.err != nil {
			delete(a.ws.claims, m.tab)
		}
	case copyLoadedMsg:
		a.copyLoaded(m)
	case previewLogsMsg:
		if a.ui.overview != nil {
			a.ui.overview.setLogs(m)
		}
	case toastExpiredMsg:
		a.expireToasts()
	case beeTickMsg:
		a.beeTick()
	case func():
		m()
	}
}

// Settle handles messages until no background work is left. Tests use it
// in place of the loop.
func (a *App) Settle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		select {
		case m := <-a.msgs:
			a.Handle(m)
			continue
		default:
		}
		if a.pending.Load() == 0 && len(a.msgs) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case m := <-a.msgs:
			a.Handle(m)
		case <-time.After(time.Millisecond):
		}
	}
}

// TakeOutput returns raw sequences queued for the outer terminal.
func (a *App) TakeOutput() []string {
	out := a.out
	a.out = nil
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
