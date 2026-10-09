package mux

import (
	"context"
	"sync"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/vt"
)

// view is the live screen of one agent's terminal, streamed from the daemon
// as frames. Its reader goroutine applies frames under mu and wakes the
// loop; the loop draws while holding mu. Keystrokes and resizes go through
// one ordered queue, so a resize always reaches the daemon before the keys
// typed after it.
type view struct {
	procID string

	mu     sync.Mutex
	screen *vt.Screen // nil until the first frame (or a cached screen)
	ended  bool       // the stream ended: the agent exited or the daemon went away
	failed bool       // the stream could not be opened

	ops    chan viewOp
	cancel context.CancelFunc
	done   chan struct{}

	// Loop goroutine only.
	size size // the size this view asked for; zero follows the terminal
}

// viewOp is one queued keystroke batch or resize.
type viewOp struct {
	data   []byte
	resize *size
}

// screenCache keeps the last screen of a closed view, shown while a new
// view of the same agent waits for its first frame (switching tabs back).
type screenCache struct {
	screen *vt.Screen
}

// openView starts streaming procID's terminal at the given size (zero: the
// terminal's own size, for previews that must not resize it).
func (a *App) openView(procID string, sz size, cached *vt.Screen) *view {
	ctx, cancel := context.WithCancel(a.ctx)
	v := &view{
		procID: procID,
		screen: cached,
		ops:    make(chan viewOp, 256),
		cancel: cancel,
		done:   make(chan struct{}),
		size:   sz,
	}
	c, wake := a.c, a.signal
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		defer close(v.done)
		v.run(ctx, c, wake, sz)
	}()
	return v
}

// run streams the view; sz is its size at opening (v.size belongs to the
// loop).
func (v *view) run(ctx context.Context, c client.Client, wake func(), sz size) {
	req := client.ViewRequest{ProcessID: v.procID, Width: uint16(sz.w), Height: uint16(sz.h)}
	fs, err := c.TerminalFrames(ctx, req)
	if err != nil {
		v.mu.Lock()
		v.failed, v.ended = true, true
		v.mu.Unlock()
		wake()
		v.drain(ctx)
		return
	}
	stop := context.AfterFunc(ctx, func() { _ = fs.Close() })
	defer stop()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			f, err := fs.Next()
			if err != nil {
				v.mu.Lock()
				v.ended = true
				v.mu.Unlock()
				wake()
				return
			}
			v.mu.Lock()
			if v.screen == nil {
				v.screen = &vt.Screen{}
			}
			v.screen.Apply(f)
			v.mu.Unlock()
			wake()
		}
	}()

	for {
		select {
		case op := <-v.ops:
			switch {
			case op.resize != nil:
				rctx, cancel := context.WithTimeout(ctx, callTimeout)
				_ = fs.Resize(rctx, uint16(op.resize.w), uint16(op.resize.h))
				cancel()
			case len(op.data) > 0:
				_, _ = fs.Write(op.data)
			}
		case <-ctx.Done():
			_ = fs.Close()
			<-readerDone
			return
		case <-readerDone:
			v.drain(ctx)
			return
		}
	}
}

// drain discards queued input until the view closes, so senders never
// block on a view with no stream.
func (v *view) drain(ctx context.Context) {
	for {
		select {
		case <-v.ops:
		case <-ctx.Done():
			return
		}
	}
}

// send queues keystrokes for the agent.
func (v *view) send(ctx context.Context, data []byte) {
	if len(data) == 0 {
		return
	}
	select {
	case v.ops <- viewOp{data: data}:
	case <-ctx.Done():
	}
}

// resize asks for the agent's terminal to take sz while this view types.
func (v *view) resize(ctx context.Context, sz size) {
	if sz == v.size || sz.w <= 0 || sz.h <= 0 {
		return
	}
	v.size = sz
	select {
	case v.ops <- viewOp{resize: &sz}:
	case <-ctx.Done():
	}
}

func (v *view) close() { v.cancel() }

// modes returns the terminal modes the agent set (mouse tracking, cursor
// keys, bracketed paste).
func (v *view) modes() vt.Modes {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.screen == nil {
		return 0
	}
	return v.screen.Modes
}

// clone copies the current screen (for copy mode and the cache).
func (v *view) clone() *vt.Screen {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.screen == nil {
		return nil
	}
	return v.screen.Clone()
}

func (v *view) isEnded() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ended
}

// --- which views are open ---

// wantViews lists the agents whose screens are on screen, with the size
// each view asks for.
func (a *App) wantViews() map[string]size {
	want := map[string]size{}
	s := a.ws.snap
	if s == nil {
		return want
	}
	if a.ui.overview != nil {
		if p := s.process(a.ui.overview.selID); p != nil && streamable(p) {
			want[p.ID] = size{} // a preview: never resize the agent
		}
		return want
	}
	for id, r := range a.geometry() {
		p := s.pane(id)
		if p == nil || p.Process == nil || !streamable(p.Process) || r.W <= 0 || r.H <= 0 {
			continue
		}
		want[p.ProcessID] = size{r.W, r.H}
	}
	return want
}

// streamable reports whether an agent has a live terminal to show.
func streamable(p *process.Process) bool { return p.Terminal && p.Active() }

// syncViews opens streams for agents that came on screen, resizes those
// whose pane changed size, and closes the rest (keeping their last screen
// for a moment).
func (a *App) syncViews() {
	want := a.wantViews()
	for id, v := range a.views {
		sz, ok := want[id]
		switch {
		case !ok:
			a.closeView(id)
		case v.isEnded():
			// The daemon restarted or the stream broke while the agent
			// still runs: open it again. This runs on workspace changes
			// only, so a stream that keeps failing cannot spin.
			cached := v.clone()
			a.closeView(id)
			delete(a.cache, id)
			a.views[id] = a.openView(id, sz, cached)
		case sz != (size{}):
			v.resize(a.ctx, sz)
		}
	}
	for id, sz := range want {
		if _, ok := a.views[id]; ok {
			continue
		}
		var cached *vt.Screen
		if c := a.cache[id]; c != nil {
			cached = c.screen
			delete(a.cache, id)
		}
		a.views[id] = a.openView(id, sz, cached)
	}
}

// closeView stops a stream and keeps its last screen for exited agents
// and quick returns.
func (a *App) closeView(id string) {
	v := a.views[id]
	if v == nil {
		return
	}
	if scr := v.clone(); scr != nil {
		a.cache[id] = &screenCache{screen: scr}
	}
	v.close()
	delete(a.views, id)
	a.trimCache()
}

// maxCachedScreens bounds the screens kept for agents not on screen.
const maxCachedScreens = 64

func (a *App) trimCache() {
	if len(a.cache) <= maxCachedScreens || a.ws.snap == nil {
		return
	}
	for id := range a.cache {
		if a.ws.snap.process(id) == nil {
			delete(a.cache, id) // the agent is gone
		}
	}
	for id := range a.cache {
		if len(a.cache) <= maxCachedScreens {
			break
		}
		delete(a.cache, id)
	}
}

// lockViews locks every view so the loop can read their screens; the
// readers wait meanwhile. It returns the unlock.
func (a *App) lockViews() func() {
	locked := make([]*view, 0, len(a.views))
	for _, v := range a.views {
		v.mu.Lock()
		locked = append(locked, v)
	}
	return func() {
		for _, v := range locked {
			v.mu.Unlock()
		}
	}
}

// screenOf returns the screen to draw for an agent: its live view's, or
// the cached last one. The caller holds lockViews.
func (a *App) screenOf(procID string) (scr *vt.Screen, ended bool) {
	if v := a.views[procID]; v != nil {
		return v.screen, v.ended
	}
	if c := a.cache[procID]; c != nil {
		return c.screen, true
	}
	return nil, false
}
