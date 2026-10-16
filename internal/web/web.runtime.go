package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/remote"
)

// Connection timing.
const (
	// lingerAfterLast keeps a runtime connected this long after its last
	// viewer leaves, so moving between pages does not reconnect.
	lingerAfterLast = time.Minute
	// statusEvery is how often a connected runtime is asked for its
	// status: what "last synchronised" means when nothing happens.
	statusEvery = 15 * time.Second
	minBackoff  = 500 * time.Millisecond
	maxBackoff  = 30 * time.Second
	// refusedRetry is how long to wait after the runtime refused the
	// credential (it may be replaced with `hive web runtime add`).
	refusedRetry = 30 * time.Second
	// maxWatches bounds live screen watches per runtime (the observer
	// socket allows 8 streams per connection; one carries events). Screens
	// past it are polled.
	maxWatches = 6
	pollEvery  = 2 * time.Second
)

// conn is the web observer's connection to one runtime. It connects only
// while someone looks (Acquire), and stays a minute after the last viewer
// leaves.
type conn struct {
	rt    Runtime
	dial  func(ctx context.Context) (net.Conn, error)
	model *Model
	log   *slog.Logger
	now   func() time.Time

	mu      sync.Mutex
	users   int
	cancel  context.CancelFunc
	done    chan struct{}
	linger  *time.Timer
	client  *observer.Client
	subs    map[chan struct{}]struct{}
	watches map[string]*paneWatch
}

func newConn(rt Runtime, log *slog.Logger) *conn {
	c := &conn{
		rt: rt, model: NewModel(), log: log.With("runtime", rt.Name), now: time.Now,
		subs: map[chan struct{}]struct{}{}, watches: map[string]*paneWatch{},
	}
	if rt.Host != "" {
		c.dial = remote.Target{Host: rt.Host, Session: rt.Session, SSH: rt.SSH, HivePath: rt.HivePath}.DialObserver
	}
	return c
}

// Acquire keeps the runtime connected until the returned func is called.
func (c *conn) Acquire() func() {
	c.mu.Lock()
	c.users++
	if c.linger != nil {
		c.linger.Stop()
		c.linger = nil
	}
	if c.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel, c.done = cancel, make(chan struct{})
		done := c.done
		go func() {
			defer close(done)
			c.run(ctx)
		}()
	}
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.users--
			if c.users == 0 && c.cancel != nil {
				c.linger = time.AfterFunc(lingerAfterLast, c.idle)
			}
		})
	}
}

// idle disconnects a runtime nobody looks at.
func (c *conn) idle() {
	c.mu.Lock()
	if c.users > 0 || c.cancel == nil {
		c.mu.Unlock()
		return
	}
	cancel, done := c.cancel, c.done
	c.cancel, c.linger = nil, nil
	c.mu.Unlock()
	cancel()
	<-done
	c.model.setState(StateIdle, "")
	c.notify()
}

// Close disconnects for good.
func (c *conn) Close() {
	c.mu.Lock()
	if c.linger != nil {
		c.linger.Stop()
	}
	cancel, done := c.cancel, c.done
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Subscribe is told (coalesced) whenever the model changes.
func (c *conn) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subs, ch)
		c.mu.Unlock()
	}
}

func (c *conn) notify() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// current is the live connection, or nil.
func (c *conn) current() *observer.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

var errOffline = errors.New("the runtime is not connected")

func (c *conn) run(ctx context.Context) {
	var (
		epoch   string
		since   *uint64
		backoff = minBackoff
	)
	for ctx.Err() == nil {
		state := StateConnecting
		if c.model.View().Loaded() {
			state = StateRetrying
		}
		c.model.setState(state, "")
		c.notify()
		connected, err := c.session(ctx, &epoch, &since)
		if ctx.Err() != nil {
			return
		}
		wait := backoff
		if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodePermissionDenied {
			c.model.setState(StateRefused, pe.Message)
			wait = refusedRetry
		} else {
			c.model.setState(StateRetrying, errText(err))
		}
		c.notify()
		if connected {
			backoff = minBackoff
		} else {
			backoff = min(2*backoff, maxBackoff)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func errText(err error) string {
	if err == nil || errors.Is(err, io.EOF) {
		return "the connection ended"
	}
	return err.Error()
}

// session follows the runtime over one connection until it drops.
func (c *conn) session(ctx context.Context, epoch *string, since **uint64) (bool, error) {
	token, err := os.ReadFile(c.rt.TokenFile)
	if err != nil {
		return false, err
	}
	cfg := observer.ClientConfig{Socket: c.rt.Socket, Dial: c.dial, Token: strings.TrimSpace(string(token)), Name: "hive-web"}
	cl, err := observer.Connect(ctx, cfg)
	if err != nil {
		return false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = cl.Close() })
	defer stop()
	defer cl.Close()
	c.mu.Lock()
	c.client = cl
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.client = nil
		c.mu.Unlock()
	}()

	req := observer.EventsRequest{Epoch: *epoch, Since: *since}
	stream, _, err := cl.Events(ctx, req)
	if err != nil {
		return true, err
	}
	defer stream.Close()
	snapshot := func() error {
		snap, err := cl.Snapshot(ctx)
		if err != nil {
			return err
		}
		*epoch, *since = snap.Epoch, &snap.Seq
		c.model.Apply(observer.Update{Snapshot: &snap}, c.now())
		return nil
	}
	if *since == nil {
		if err := snapshot(); err != nil {
			return true, err
		}
	}
	st, err := cl.Status(ctx)
	if err != nil {
		return true, err
	}
	c.model.synced(&st, c.now())
	c.model.setState(StateLive, "")
	c.notify()

	// The status, now and then: proof the runtime is still there.
	go func() {
		t := time.NewTicker(statusEvery)
		defer t.Stop()
		for {
			select {
			case <-cl.Done():
				return
			case <-t.C:
				st, err := cl.Status(ctx)
				if err != nil {
					_ = cl.Close()
					return
				}
				c.model.synced(&st, c.now())
				c.notify()
			}
		}
	}()
	for {
		ev, err := stream.Next()
		if err != nil {
			return true, err
		}
		if ev.Type == observer.Resync {
			if err := snapshot(); err != nil {
				return true, err
			}
			c.notify()
			continue
		}
		seq := ev.Seq
		*since = &seq
		c.model.Apply(observer.Update{Event: &ev}, c.now())
		if ev.Type == "process.output" {
			c.screenChanged(ev)
			continue
		}
		c.notify()
	}
}

// paneWatch is one pane's live screen, shared by everyone viewing it.
type paneWatch struct {
	mu      sync.Mutex
	users   int
	cancel  context.CancelFunc
	screen  observer.Screen
	have    bool
	err     string
	version uint64
	subs    map[chan struct{}]struct{}
	poke    chan struct{} // a polled watch refreshes now
}

func (w *paneWatch) notify() {
	for ch := range w.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Screen is a pane's screen as last seen, and whether there is one.
func (w *paneWatch) Screen() (observer.Screen, uint64, string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.screen, w.version, w.err, w.have
}

// WatchScreen follows pane's screen until the returned func is called;
// the channel is told when it changes.
func (c *conn) WatchScreen(pane string) (*paneWatch, <-chan struct{}, func()) {
	c.mu.Lock()
	w, ok := c.watches[pane]
	if !ok {
		w = &paneWatch{subs: map[chan struct{}]struct{}{}, poke: make(chan struct{}, 1)}
		c.watches[pane] = w
		ctx, cancel := context.WithCancel(context.Background())
		w.cancel = cancel
		live := len(c.watches) <= maxWatches
		go c.follow(ctx, pane, w, live)
	}
	c.mu.Unlock()
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	w.users++
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	var once sync.Once
	return w, ch, func() {
		once.Do(func() {
			w.mu.Lock()
			delete(w.subs, ch)
			w.users--
			last := w.users == 0
			w.mu.Unlock()
			if last {
				c.mu.Lock()
				if c.watches[pane] == w {
					delete(c.watches, pane)
				}
				c.mu.Unlock()
				w.cancel()
			}
		})
	}
}

// screenChanged pokes polled watches of the pane an output event names.
func (c *conn) screenChanged(ev observer.Event) {
	var o struct {
		PaneID string `json:"pane_id"`
	}
	if json.Unmarshal(ev.Data, &o) != nil {
		return
	}
	c.mu.Lock()
	w := c.watches[o.PaneID]
	c.mu.Unlock()
	if w != nil {
		select {
		case w.poke <- struct{}{}:
		default:
		}
	}
}

// follow keeps w current: through observe.watch when live, else by
// reading the screen when it changes (at most every pollEvery).
func (c *conn) follow(ctx context.Context, pane string, w *paneWatch, live bool) {
	set := func(scr *observer.Screen, errText string) {
		w.mu.Lock()
		if scr != nil {
			w.screen, w.have = *scr, true
		}
		w.err = errText
		w.version++
		w.notify()
		w.mu.Unlock()
	}
	for ctx.Err() == nil {
		cl := c.current()
		if cl == nil {
			set(nil, errOffline.Error())
			if !sleep(ctx, time.Second) {
				return
			}
			continue
		}
		scr, err := cl.Screen(ctx, observer.ScreenRequest{Pane: pane})
		if err != nil {
			set(nil, errText(err))
			wait := pollEvery
			if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code != protocol.ErrorCodeUnavailable {
				wait = 30 * time.Second // not allowed, or gone: no point asking often
			}
			if !sleep(ctx, wait) {
				return
			}
			continue
		}
		set(&scr, "")
		if live {
			stream, _, err := cl.Watch(ctx, observer.WatchRequest{Pane: pane, IntervalMS: 250})
			if err == nil {
				stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
				lines := scr.Lines
				for {
					u, err := stream.Next()
					if err != nil {
						break
					}
					lines = u.Apply(lines)
					scr = observer.Screen{Pane: pane, Width: u.Width, Height: u.Height, Lines: append([]string(nil), lines...), Cursor: u.Cursor, Title: u.Title, AltScreen: u.AltScreen}
					set(&scr, "")
				}
				stop()
				_ = stream.Close()
				if !sleep(ctx, time.Second) {
					return
				}
				continue
			}
			live = false // no stream to spare: poll instead
		}
		select {
		case <-ctx.Done():
			return
		case <-w.poke:
		case <-time.After(30 * time.Second):
		}
		if !sleep(ctx, pollEvery) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
