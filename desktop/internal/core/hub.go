package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Events the hub emits to the frontend.
const (
	// EventConnection carries a Connection.
	EventConnection = "hive:connection"
	// EventWorkspace carries a Workspace.
	EventWorkspace = "hive:workspace"
	// EventScreen carries a ScreenUpdate.
	EventScreen = "hive:screen"
	// EventSettings carries Settings.
	EventSettings = "hive:settings"
)

// Emitter sends events to the frontend.
type Emitter interface {
	Emit(name string, data any)
}

// Notification is a system notification about an agent.
type Notification struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	AgentID string `json:"agent_id"`
	PaneID  string `json:"pane_id"`
}

// Config is how the hub reaches Hive and the system.
type Config struct {
	// Home and Session are hive's ($HIVE_HOME, --session; "" for defaults).
	Home, Session string
	// Hive is the hive binary ("" finds it; see FindHive).
	Hive string
	// LoginShell runs hive through the user's login shell (see
	// hiveCommand); the app sets it, tests do not.
	LoginShell bool
	Emitter    Emitter
	// Notify posts a system notification (nil: none).
	Notify func(Notification)
	// Clipboard puts text a program copied (OSC 52) on the clipboard (nil:
	// ignored).
	Clipboard func(text string)
	// Focused says whether one of the app's windows has focus.
	Focused func() bool
	Logger  *slog.Logger
	// Name is the client name in the daemon's logs.
	Name string
	// Dial reaches the daemon some other way (a saved machine's, through
	// `hive machine proxy`); such a hub neither starts a daemon nor reads
	// the configuration.
	Dial func(ctx context.Context) (net.Conn, error)
}

// Limits on what the frontend may send.
const (
	maxInput    = 1 << 20
	maxPrompt   = 1 << 20
	maxNameLen  = 256
	maxIDLen    = 256
	frameBudget = 16 * time.Millisecond // at most ~60 updates a second per pane
	// refreshEvery bounds workspace refreshes when events pour in.
	refreshEvery = 60 * time.Millisecond
)

// ErrNotConnected is returned by actions while the daemon is unreachable.
var ErrNotConnected = errors.New("not connected to the hive daemon")

// Hub is the app's connection to Hive.
type Hub struct {
	cfg Config
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	retry  chan struct{}

	mu       sync.Mutex
	conn     Connection
	client   *hiveapi.Client
	ws       Workspace
	settings Settings
	views    map[string]*view // by pane ID: what the frontend shows
	// seen is each agent's last known state and sequence; notified the
	// sequence last notified, so a state is announced once.
	seen     map[string]Agent
	notified map[string]uint64
	primed   bool // seen holds a snapshot (the first one notifies nothing)
	// asked is the tasks known to wait for approval, by plan/task, so each
	// request is announced once.
	asked map[string]bool
	// overlaps is the branch overlaps known, so each is announced once.
	overlaps map[string]bool
}

// New returns a hub; Start connects it.
func New(cfg Config) *Hub {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Name == "" {
		cfg.Name = "desktop"
	}
	session := cfg.Session
	if session == "" {
		session = "default"
	}
	return &Hub{
		cfg: cfg, log: cfg.Logger, retry: make(chan struct{}, 1),
		conn: Connection{State: StateConnecting, Session: session}, settings: DefaultSettings(),
		ws:    Workspace{Environments: []Environment{}, Tabs: []Tab{}, Panes: []Pane{}, Agents: []Agent{}, Counts: map[string]int{}},
		views: map[string]*view{}, seen: map[string]Agent{}, notified: map[string]uint64{},
	}
}

// Start connects in the background, and reconnects whenever the
// connection drops.
func (h *Hub) Start() {
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.wg.Go(h.loop)
}

// Close disconnects; agents keep running.
func (h *Hub) Close() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	h.mu.Lock()
	for _, v := range h.views {
		v.stop()
	}
	h.mu.Unlock()
	h.wg.Wait()
	h.mu.Lock()
	if h.client != nil {
		_ = h.client.Close()
		h.client = nil
	}
	h.mu.Unlock()
}

// Retry connects again now, instead of after the back-off.
func (h *Hub) Retry() {
	select {
	case h.retry <- struct{}{}:
	default:
	}
}

// Connection is the current connection state.
func (h *Hub) Connection() Connection {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn
}

// Workspace is the current workspace.
func (h *Hub) Workspace() Workspace {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ws
}

// Settings are the configuration the app follows.
func (h *Hub) Settings() Settings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings
}

func (h *Hub) emit(name string, data any) {
	if h.cfg.Emitter != nil {
		h.cfg.Emitter.Emit(name, data)
	}
}

func (h *Hub) setConn(c Connection) {
	h.mu.Lock()
	c.Session = h.conn.Session
	h.conn = c
	h.mu.Unlock()
	h.emit(EventConnection, c)
}

func (h *Hub) loop() {
	backoff := 500 * time.Millisecond
	for h.ctx.Err() == nil {
		h.setConn(Connection{State: StateConnecting})
		c, err := h.connect(h.ctx)
		if err == nil {
			backoff = 500 * time.Millisecond
			err = h.serve(h.ctx, c)
		}
		if h.ctx.Err() != nil {
			return
		}
		if err != nil {
			state := StateOffline
			if errors.Is(err, errOutdated) {
				state = StateOutdated
			}
			h.log.Warn("disconnected from the daemon", "err", err)
			h.setConn(Connection{State: state, Message: err.Error()})
		}
		select {
		case <-h.ctx.Done():
			return
		case <-h.retry:
		case <-time.After(backoff):
			ceiling := 10 * time.Second
			if h.cfg.Dial != nil {
				ceiling = time.Minute // a machine away: ssh tries are slow and noisy
			}
			backoff = min(backoff*2, ceiling)
		}
	}
}

var errOutdated = errors.New("the hive daemon is older than this app")

// connect reaches the daemon, starting it when none answers.
func (h *Hub) connect(ctx context.Context) (*hiveapi.Client, error) {
	opts := hiveapi.Options{Home: h.cfg.Home, Session: h.cfg.Session, Name: h.cfg.Name, Dial: h.cfg.Dial}
	timeout := 5 * time.Second
	if h.cfg.Dial != nil {
		timeout = 45 * time.Second // ssh, and maybe the daemon starting there
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	c, err := hiveapi.ConnectContext(cctx, opts)
	cancel()
	if errors.Is(err, hiveapi.ErrUnavailable) && h.cfg.Dial == nil {
		h.setConn(Connection{State: StateConnecting, Message: "starting the hive daemon"})
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = h.startDaemon(sctx)
		cancel()
		if err != nil {
			return nil, err
		}
		cctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		c, err = hiveapi.ConnectContext(cctx, opts)
		cancel()
	}
	if err != nil {
		return nil, err
	}
	st, err := c.Status(ctx)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if st.APILevel < hiveapi.APILevel {
		_ = c.Close()
		return nil, fmt.Errorf("%w (hive %s; update it with `hive update`)", errOutdated, st.Version)
	}
	if h.cfg.Dial == nil {
		if s, err := h.readSettings(ctx); err == nil {
			h.mu.Lock()
			h.settings = s
			h.mu.Unlock()
			h.emit(EventSettings, s)
		} else {
			h.log.Warn("cannot read hive's configuration; using defaults", "err", err)
		}
	}
	h.mu.Lock()
	h.client = c
	h.mu.Unlock()
	h.setConn(Connection{State: StateConnected, Version: st.Version, APILevel: st.APILevel})
	return c, nil
}

// serve follows the daemon's events, refreshing the workspace, until the
// connection ends.
func (h *Hub) serve(ctx context.Context, c *hiveapi.Client) error {
	defer func() {
		h.mu.Lock()
		if h.client == c {
			h.client = nil
		}
		for _, v := range h.views {
			v.stop() // they are opened again on the next connection
		}
		h.mu.Unlock()
		_ = c.Close()
	}()
	events, err := c.Events(ctx, "agent.", "pane.", "tab.", "environment.", "process.", "task.plan", "task.removed", "branch.", "events.")
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = events.Close() })
	defer stop()
	changed := make(chan struct{}, 1)
	readErr := make(chan error, 1)
	// Plans are read again only after a plan event: the screen's own
	// events (a key typed, output) must not cost a plans round trip.
	var plansStale atomic.Bool
	plansStale.Store(true)
	go func() {
		for {
			ev, err := events.Next()
			if err != nil {
				readErr <- err
				return
			}
			if strings.HasPrefix(ev.Type, "task.") || strings.HasPrefix(ev.Type, "branch.") || ev.Type == "events_lost" {
				plansStale.Store(true)
			}
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	if err := h.refresh(ctx, c, plansStale.Swap(false)); err != nil {
		return err
	}
	h.mu.Lock()
	for _, v := range h.views {
		h.startViewLocked(ctx, c, v.pane, v.cols, v.rows)
	}
	h.mu.Unlock()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return fmt.Errorf("the daemon's events ended: %w", err)
		case <-changed:
			if wait := refreshEvery - time.Since(last); wait > 0 {
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return nil
				}
			}
			last = time.Now()
			if err := h.refresh(ctx, c, plansStale.Swap(false)); err != nil {
				return err
			}
		}
	}
}

// refresh reads the workspace (and with plans, the plans; otherwise the
// last ones read stand), tells the frontend, notifies, and keeps pane
// views on the right processes.
func (h *Hub) refresh(ctx context.Context, c *hiveapi.Client, plans bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return err
	}
	ws := workspaceOf(snap)
	if plans {
		if list, err := c.Plans(ctx); err == nil {
			ws.Plans = plansOf(list)
		} else if ctx.Err() == nil {
			h.log.Debug("no plans (a daemon older than tasks?)", "err", err)
		}
		if list, err := c.Branches(ctx, "", false); err == nil {
			ws.Branches = branchesOf(list)
		} else if ctx.Err() == nil {
			h.log.Debug("no branches (a daemon older than M23?)", "err", err)
		}
	} else {
		h.mu.Lock()
		ws.Plans = h.ws.Plans // replaced whole on every read, never changed in place
		ws.Branches = h.ws.Branches
		h.mu.Unlock()
	}
	h.mu.Lock()
	h.ws = ws
	notes := h.notificationsLocked(ws)
	notes = append(notes, h.approvalsLocked(ws)...)
	notes = append(notes, h.overlapsLocked(ws)...)
	for id, v := range h.views {
		p, ok := paneByID(ws, id)
		switch {
		case !ok:
			v.stop()
			delete(h.views, id)
		case p.ProcessID != v.proc && p.ProcessID != "":
			h.startViewLocked(ctx, c, id, v.cols, v.rows)
		}
	}
	h.mu.Unlock()
	h.emit(EventWorkspace, ws)
	if h.cfg.Notify != nil {
		for _, n := range notes {
			h.cfg.Notify(n)
		}
	}
	return nil
}

func paneByID(ws Workspace, id string) (Pane, bool) {
	for _, p := range ws.Panes {
		if p.ID == id {
			return p, true
		}
	}
	return Pane{}, false
}

// approvalsLocked announces tasks that started waiting for approval since
// the last refresh, as the agents' notifications are: once, and not on the
// first snapshot.
func (h *Hub) approvalsLocked(ws Workspace) []Notification {
	next := map[string]bool{}
	var out []Notification
	for _, p := range ws.Plans {
		for _, t := range p.Waiting {
			if t.State != string(hiveapi.TaskApproval) {
				continue
			}
			key := p.ID + "/" + t.ID
			next[key] = true
			if h.asked == nil || h.asked[key] || !h.settings.notifies("blocked") || h.settings.SystemNotifications {
				continue
			}
			title := "Plan " + cmp(p.Name, p.ID)
			out = append(out, Notification{ID: "plan:" + key, Title: title, Body: t.ID + " waits for your approval"})
		}
	}
	h.asked = next
	return out
}

// notificationsLocked compares agents with what was seen before: an agent
// that entered a state worth notifying is announced once per change,
// unless someone is looking at it (its pane focused in the app's focused
// window), or the daemon notifies the system itself.
func (h *Hub) notificationsLocked(ws Workspace) []Notification {
	var out []Notification
	focusedPane := map[string]bool{}
	for _, t := range ws.Tabs {
		if t.Active && t.Focused != "" {
			focusedPane[t.Focused] = true
		}
	}
	looking := h.cfg.Focused != nil && h.cfg.Focused()
	next := make(map[string]Agent, len(ws.Agents))
	for _, a := range ws.Agents {
		next[a.ID] = a
		prev, known := h.seen[a.ID]
		if !h.primed || (known && prev.StateSeq == a.StateSeq && prev.State == a.State) {
			continue
		}
		if !h.settings.notifies(a.State) || h.settings.SystemNotifications {
			continue
		}
		if h.notified[a.ID] >= a.StateSeq && known {
			continue
		}
		if looking && focusedPane[a.PaneID] {
			continue
		}
		h.notified[a.ID] = a.StateSeq
		verb := map[string]string{"blocked": "needs you", "done": "is done", "idle": "is idle", "working": "is working"}[a.State]
		if verb == "" {
			verb = "is " + a.State
		}
		body := a.Reason
		if body == "" {
			body = a.Kind + " in " + a.EnvironmentID
		}
		out = append(out, Notification{
			ID: fmt.Sprintf("hive-%s-%d", a.ID, a.StateSeq), Title: a.Name + " " + verb, Body: body, AgentID: a.ID, PaneID: a.PaneID,
		})
	}
	h.seen, h.primed = next, true
	for id := range h.notified {
		if _, ok := next[id]; !ok {
			delete(h.notified, id)
		}
	}
	return out
}

// --- pane views ---

// view streams one pane's screen to the frontend.
type view struct {
	pane, proc string
	cols, rows int
	// viewers are the windows showing it; it stops when none is left.
	viewers map[string]bool

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	ops    chan viewOp

	mu     sync.Mutex
	screen *hiveapi.TerminalScreen
	dirty  map[int]bool
	full   bool
	seq    uint64
	ended  bool
}

type viewOp struct {
	data   []byte
	resize *[2]int
}

func (v *view) stop() {
	if v.cancel != nil {
		v.cancel()
	}
}

func validSize(cols, rows int) error {
	if cols < 2 || rows < 1 || cols > 2000 || rows > 1000 {
		return fmt.Errorf("invalid terminal size %d×%d", cols, rows)
	}
	return nil
}

func validID(id string) error {
	if id == "" || len(id) > maxIDLen || strings.ContainsAny(id, "\x00\n\r") {
		return fmt.Errorf("invalid ID %q", id)
	}
	return nil
}

// OpenView starts streaming a pane's screen at cols × rows for viewer (a
// window), or resizes the stream when it is open: one stream serves every
// viewer, at the size the last one asked for. The first update is the
// whole screen.
func (h *Hub) OpenView(viewer, paneID string, cols, rows int) error {
	if err := validID(viewer); err != nil {
		return err
	}
	if err := validID(paneID); err != nil {
		return err
	}
	if err := validSize(cols, rows); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := h.views[paneID]; ok {
		v.viewers[viewer] = true
	}
	if v, ok := h.views[paneID]; ok && v.ctx != nil && v.ctx.Err() == nil {
		if v.cols != cols || v.rows != rows {
			v.cols, v.rows = cols, rows
			select {
			case v.ops <- viewOp{resize: &[2]int{cols, rows}}:
			default:
			}
		}
		v.mu.Lock()
		v.full = true
		v.mu.Unlock()
		v.wake()
		return nil
	}
	viewers := map[string]bool{viewer: true}
	if v, ok := h.views[paneID]; ok {
		viewers = v.viewers
	}
	if h.client == nil {
		h.views[paneID] = &view{pane: paneID, cols: cols, rows: rows, viewers: viewers} // opened once connected
		return nil
	}
	h.startViewLocked(h.ctx, h.client, paneID, cols, rows)
	h.views[paneID].viewers = viewers
	return nil
}

// CloseView says viewer no longer shows a pane; the stream stops when no
// viewer does.
func (h *Hub) CloseView(viewer, paneID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := h.views[paneID]; ok {
		delete(v.viewers, viewer)
		if len(v.viewers) == 0 {
			v.stop()
			delete(h.views, paneID)
		}
	}
}

// startViewLocked (re)starts the view of a pane on its current process,
// for as long as ctx (the connection) lasts.
func (h *Hub) startViewLocked(ctx context.Context, c *hiveapi.Client, paneID string, cols, rows int) {
	viewers := map[string]bool{}
	if old, ok := h.views[paneID]; ok {
		old.stop()
		viewers = old.viewers
	}
	p, ok := paneByID(h.ws, paneID)
	v := &view{pane: paneID, proc: p.ProcessID, cols: cols, rows: rows, viewers: viewers, ops: make(chan viewOp, 256), done: make(chan struct{}), dirty: map[int]bool{}, full: true}
	h.views[paneID] = v
	if !ok || p.ProcessID == "" {
		return
	}
	v.ctx, v.cancel = context.WithCancel(ctx)
	h.wg.Go(func() { h.runView(v.ctx, c, v, cols, rows) })
}

// wake is set by runView: it schedules an update.
func (v *view) wake() {
	select {
	case v.ops <- viewOp{}:
	default:
	}
}

func (h *Hub) runView(ctx context.Context, c *hiveapi.Client, v *view, cols, rows int) {
	defer close(v.done)
	fs, err := c.Frames(ctx, v.proc, cols, rows)
	if err != nil {
		if ctx.Err() == nil {
			h.log.Warn("cannot show a pane", "pane", v.pane, "err", err)
			h.emit(EventScreen, ScreenUpdate{Pane: v.pane, Ended: true, Lines: []Line{}})
		}
		return
	}
	stop := context.AfterFunc(ctx, func() { _ = fs.Close() })
	defer stop()
	h.followImages(ctx, c, v, cols, rows)
	frames := make(chan struct{}, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			f, err := fs.Next()
			v.mu.Lock()
			if err != nil {
				v.ended = true
				v.mu.Unlock()
				select {
				case frames <- struct{}{}:
				default:
				}
				return
			}
			if v.screen == nil {
				v.screen = &hiveapi.TerminalScreen{}
			}
			resized := f.Cols != v.screen.Cols || f.Rows != v.screen.Rows
			// Most of the screen changing at once is a scroll: images
			// anchored to cells no longer belong there (as in hive ui).
			scrolled := !f.Keyframe && f.Rows > 2 && len(f.Lines) >= max(3, f.Rows/2)
			v.screen.Apply(f)
			if f.Keyframe || resized {
				v.full = true
			}
			for _, l := range f.Lines {
				v.dirty[l.Y] = true
			}
			v.mu.Unlock()
			if scrolled {
				h.emit(EventImage, ImageUpdate{Pane: v.pane, Op: hiveapi.ImageClear})
			}
			if cb := f.Clipboard; cb != nil && h.cfg.Clipboard != nil {
				if text, err := base64.StdEncoding.DecodeString(cb.Data); err == nil {
					h.cfg.Clipboard(string(text))
				}
			}
			select {
			case frames <- struct{}{}:
			default:
			}
		}
	}()
	var last time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	pending := false
	send := func() {
		pending = false
		last = time.Now()
		if u, ok := v.take(); ok {
			h.emit(EventScreen, u)
		}
	}
	schedule := func() {
		if pending {
			return
		}
		if wait := frameBudget - time.Since(last); wait > 0 {
			pending = true
			timer.Reset(wait)
			return
		}
		send()
	}
	for {
		select {
		case <-ctx.Done():
			_ = fs.Close()
			<-readerDone
			return
		case <-frames:
			schedule()
			v.mu.Lock()
			ended := v.ended
			v.mu.Unlock()
			if ended {
				send()
				return
			}
		case <-timer.C:
			send()
		case op := <-v.ops:
			switch {
			case op.resize != nil:
				rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_ = fs.Resize(rctx, uint16(op.resize[0]), uint16(op.resize[1]))
				cancel()
			case len(op.data) > 0:
				if _, err := fs.Write(op.data); err != nil {
					h.log.Debug("typing failed", "pane", v.pane, "err", err)
				}
			default:
				schedule() // the frontend asked for the whole screen
			}
		}
	}
}

// followImages turns a pane's Kitty graphics into image updates, for as
// long as the view lasts. A daemon without a graphics stream shows none.
func (h *Hub) followImages(ctx context.Context, c *hiveapi.Client, v *view, cols, rows int) {
	gs, err := c.Graphics(ctx, v.proc, cols, rows)
	if err != nil {
		return
	}
	h.wg.Go(func() {
		stop := context.AfterFunc(ctx, func() { _ = gs.Close() })
		defer stop()
		var images hiveapi.KittyImages
		seq := 0
		for {
			ev, err := gs.Next()
			if err != nil {
				return
			}
			for _, op := range images.Push(ev) {
				switch op.Kind {
				case hiveapi.ImageReply:
					select {
					case v.ops <- viewOp{data: op.Reply}:
					default:
					}
				case hiveapi.ImagePlace:
					p := op.Placement
					if p == 0 {
						seq++
						p = 1<<31 | uint32(seq)
					}
					h.emit(EventImage, ImageUpdate{
						Pane: v.pane, Op: op.Kind, Key: fmt.Sprintf("%d:%d", op.Image, p), Image: op.Image,
						X: op.X, Y: op.Y, Cols: op.Cols, Rows: op.Rows,
						Src: "data:image/png;base64," + base64.StdEncoding.EncodeToString(op.PNG),
					})
				default:
					h.emit(EventImage, ImageUpdate{Pane: v.pane, Op: op.Kind, Image: op.Image})
				}
			}
		}
	})
}

// take builds the update owed to the frontend, if any.
func (v *view) take() (ScreenUpdate, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.screen == nil {
		if v.ended {
			return ScreenUpdate{Pane: v.pane, Ended: true, Lines: []Line{}}, true
		}
		return ScreenUpdate{}, false
	}
	if !v.full && len(v.dirty) == 0 && !v.ended {
		return ScreenUpdate{}, false
	}
	ys := make([]int, 0, len(v.dirty))
	for y := range v.dirty {
		ys = append(ys, y)
	}
	v.seq++
	u := update(v.pane, v.seq, v.screen, v.full, ys)
	u.Ended = v.ended
	v.full, v.dirty = false, map[int]bool{}
	return u, true
}

// Refresh sends a pane's whole screen again (the frontend lost track).
func (h *Hub) Refresh(paneID string) {
	h.mu.Lock()
	v, ok := h.views[paneID]
	h.mu.Unlock()
	if !ok {
		return
	}
	v.mu.Lock()
	v.full = true
	v.mu.Unlock()
	if v.ops != nil {
		v.wake()
	}
}

func (h *Hub) liveView(paneID string) (*view, error) {
	if err := validID(paneID); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.views[paneID]
	if !ok || v.ctx == nil || v.ctx.Err() != nil {
		return nil, fmt.Errorf("pane %s is not shown", paneID)
	}
	return v, nil
}

// Input types bytes into a pane, as its view: the pane then follows this
// view's size.
func (h *Hub) Input(paneID, data string) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > maxInput {
		return errors.New("input too large")
	}
	v, err := h.liveView(paneID)
	if err != nil {
		return err
	}
	select {
	case v.ops <- viewOp{data: []byte(data)}:
		return nil
	default:
		return errors.New("the pane is not reading its input")
	}
}

// Paste types text into a pane as a paste: bracketed when the program asked
// for that (with any end marker inside the text removed, so a paste cannot
// break out of it), else with newlines as Enter.
func (h *Hub) Paste(paneID, text string) error {
	v, err := h.liveView(paneID)
	if err != nil {
		return err
	}
	v.mu.Lock()
	bracketed := v.screen != nil && uint32(v.screen.Modes)&ModeBracketedPaste != 0
	v.mu.Unlock()
	return h.Input(paneID, pasteBytes(text, bracketed))
}

func pasteBytes(text string, bracketed bool) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r")
	if !bracketed {
		return text
	}
	for strings.Contains(text, "\x1b[201~") {
		text = strings.ReplaceAll(text, "\x1b[201~", "")
	}
	return "\x1b[200~" + text + "\x1b[201~"
}

// --- actions ---

func (h *Hub) api() (*hiveapi.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client == nil {
		return nil, ErrNotConnected
	}
	return h.client, nil
}

func (h *Hub) call(fn func(context.Context, *hiveapi.Client) error) error {
	c, err := h.api()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	return fn(ctx, c)
}

// Manifests lists the agents Hive can start.
func (h *Hub) Manifests() ([]Manifest, error) {
	out := []Manifest{}
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		ms, err := c.Manifests(ctx)
		for _, m := range ms {
			out = append(out, Manifest{ID: m.ID, Name: m.Name})
		}
		return err
	})
	return out, err
}

// StartAgentRequest starts an agent.
type StartAgentRequest struct {
	Kind          string `json:"kind"`
	EnvironmentID string `json:"environment_id"`
	// Worktree is a branch to start it on, in a new worktree.
	Worktree string `json:"worktree,omitempty"`
	// Isolate starts it in a new worktree on a branch Hive names.
	Isolate bool   `json:"isolate,omitempty"`
	Name    string `json:"name,omitempty"`
}

// StartAgent starts an agent in a new tab, and returns its pane.
func (h *Hub) StartAgent(req StartAgentRequest) (string, error) {
	if err := validID(req.Kind); err != nil {
		return "", err
	}
	if err := validID(req.EnvironmentID); err != nil {
		return "", err
	}
	if len(req.Name) > maxNameLen || len(req.Worktree) > maxNameLen {
		return "", errors.New("name too long")
	}
	var pane string
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		res, err := c.StartAgent(ctx, hiveapi.StartAgent{Kind: req.Kind, EnvironmentID: req.EnvironmentID, Worktree: req.Worktree, Isolate: req.Isolate, Name: req.Name})
		pane = res.Pane.ID
		return err
	})
	return pane, err
}

// Prompt sends a prompt to an agent; it returns once the agent reacts.
func (h *Hub) Prompt(agentID, text string) (string, error) {
	if err := validID(agentID); err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" || len(text) > maxPrompt {
		return "", errors.New("a prompt needs text (up to 1 MiB)")
	}
	var outcome string
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		res, err := c.Prompt(ctx, agentID, text, hiveapi.PromptOptions{})
		outcome = string(res.Outcome)
		return err
	})
	return outcome, err
}

// StopAgent stops an agent's process; its pane keeps its last screen.
// ApproveTask lets a task waiting for approval start; deny cancels it.
func (h *Hub) ApproveTask(planID, taskID string, deny bool) error {
	if err := validID(planID); err != nil {
		return err
	}
	if err := validID(taskID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.ApproveTask(ctx, planID, taskID, hiveapi.ApproveOptions{Deny: deny, By: "desktop"})
		return err
	})
}

// CancelPlan cancels a plan: its running agents are stopped.
func (h *Hub) CancelPlan(planID string) error {
	if err := validID(planID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.CancelPlan(ctx, planID, "")
		return err
	})
}

func (h *Hub) StopAgent(agentID string) error {
	if err := validID(agentID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error { _, err := c.StopAgent(ctx, agentID); return err })
}

// RenamePane renames a pane (and so its agent).
func (h *Hub) RenamePane(paneID, name string) error {
	if err := validID(paneID); err != nil {
		return err
	}
	if len(name) > maxNameLen {
		return errors.New("name too long")
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.RenamePane(ctx, paneID, name)
		return err
	})
}

// FocusPane makes a pane the focused one, and its tab active.
func (h *Hub) FocusPane(paneID string) error {
	if err := validID(paneID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error { _, err := c.FocusPane(ctx, paneID, ""); return err })
}

// FocusNeighbour focuses the pane beside paneID that way (the focus-left,
// …-right, …-up and …-down key bindings).
func (h *Hub) FocusNeighbour(paneID, dir string) error {
	if err := validID(paneID); err != nil {
		return err
	}
	d, err := direction(dir)
	if err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error { _, err := c.FocusPane(ctx, paneID, d); return err })
}

// SwapPane exchanges two panes of one tab, through the layout API
// (pane.swap): what dragging a pane onto another does.
func (h *Hub) SwapPane(paneID, withID string) error {
	if err := validID(paneID); err != nil {
		return err
	}
	if err := validID(withID); err != nil {
		return err
	}
	if paneID == withID {
		return nil
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		return c.Call(ctx, "pane.swap", map[string]string{"id": paneID, "with": withID}, nil)
	})
}

func direction(d string) (hiveapi.Direction, error) {
	switch hiveapi.Direction(d) {
	case hiveapi.Left, hiveapi.Right, hiveapi.Up, hiveapi.Down:
		return hiveapi.Direction(d), nil
	}
	return "", fmt.Errorf("invalid direction %q", d)
}

// SplitPane splits a pane that way, starting a shell in the new half.
func (h *Hub) SplitPane(paneID, dir string) (string, error) {
	if err := validID(paneID); err != nil {
		return "", err
	}
	d, err := direction(dir)
	if err != nil {
		return "", err
	}
	var id string
	err = h.call(func(ctx context.Context, c *hiveapi.Client) error {
		p, err := c.SplitPane(ctx, hiveapi.SplitRequest{Pane: paneID, Direction: d})
		id = p.ID
		return err
	})
	return id, err
}

// ResizePane moves a pane's border that way by cells.
func (h *Hub) ResizePane(paneID, dir string, cells int) error {
	if err := validID(paneID); err != nil {
		return err
	}
	d, err := direction(dir)
	if err != nil {
		return err
	}
	if cells < 1 || cells > 500 {
		return errors.New("invalid resize")
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.ResizePane(ctx, paneID, d, cells)
		return err
	})
}

// ZoomPane toggles a pane filling its tab.
func (h *Hub) ZoomPane(paneID string) error {
	if err := validID(paneID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error { _, err := c.ZoomPane(ctx, paneID, nil); return err })
}

// ClosePane closes a pane and stops its process.
func (h *Hub) ClosePane(paneID string) error {
	if err := validID(paneID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error { return c.ClosePane(ctx, paneID) })
}

// NewTab opens a tab with a shell in an environment, and returns its pane.
func (h *Hub) NewTab(envID string) (string, error) {
	if err := validID(envID); err != nil {
		return "", err
	}
	var pane string
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		res, err := c.CreateTab(ctx, hiveapi.CreateTabRequest{EnvironmentID: envID})
		pane = res.Pane.ID
		return err
	})
	return pane, err
}

// FocusTab makes a tab its environment's active one.
func (h *Hub) FocusTab(tabID string) error {
	if err := validID(tabID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error { _, err := c.FocusTab(ctx, tabID); return err })
}

// ResizeTab sets a tab's area to what the app has room for.
func (h *Hub) ResizeTab(tabID string, cols, rows int) error {
	if err := validID(tabID); err != nil {
		return err
	}
	if err := validSize(cols, rows); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.ResizeTab(ctx, tabID, cols, rows)
		return err
	})
}

// CloseTab closes a tab and stops its panes' processes.
func (h *Hub) CloseTab(tabID string) error {
	if err := validID(tabID); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error { return c.CloseTab(ctx, tabID) })
}

// maxHistory bounds the lines History returns.
const maxHistory = 20000

// History returns a pane's recent lines: its scrollback and screen, as
// text, the last lines of it.
func (h *Hub) History(paneID string, lines int) ([]string, error) {
	if err := validID(paneID); err != nil {
		return nil, err
	}
	if lines < 1 || lines > maxHistory {
		lines = maxHistory
	}
	out := []string{}
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		got, err := c.ReadPane(ctx, paneID, hiveapi.ReadOptions{Source: "recent", Lines: lines})
		out = append(out, got...)
		return err
	})
	return out, err
}

// CreateEnvironment makes an environment for a directory ("" for a
// managed one).
func (h *Hub) CreateEnvironment(id, dir string) error {
	if err := validID(id); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.CreateEnvironment(ctx, id, dir, nil)
		return err
	})
}
