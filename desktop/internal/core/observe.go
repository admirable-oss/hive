package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// The observer window (M14 in the app): a window for shared screens that
// can only look. The main window makes a scoped, expiring observer
// credential through the control connection; the window then reads
// everything through the daemon's observer socket with it, where no method
// types, changes a layout or starts anything. The token stays in Go and
// the credential is revoked when the window closes.

// Events an observation sends to its own window.
const (
	// EventObserveView carries an ObservedView.
	EventObserveView = "hive:observe:view"
	// EventObserveScreen carries an ObservedScreen.
	EventObserveScreen = "hive:observe:screen"
)

// ObserveRequest is what an observer window may see.
type ObserveRequest struct {
	// All, or the listed environments of this computer.
	All          bool     `json:"all"`
	Environments []string `json:"environments,omitempty"`
	// Screens lets it show screen contents, not only states.
	Screens bool `json:"screens"`
	// Hours is how long the credential lasts (1 to 168).
	Hours int `json:"hours"`
}

// Observer is a credential made for an observer window.
type Observer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	Screens   bool      `json:"screens"`
	ExpiresAt time.Time `json:"expires_at"`
	token     string
	socket    string
}

// CreateObserver makes an observer credential for a window.
func (h *Hub) CreateObserver(req ObserveRequest) (Observer, error) {
	if !req.All && len(req.Environments) == 0 {
		return Observer{}, errors.New("choose what the window may see")
	}
	if req.All && len(req.Environments) > 0 {
		return Observer{}, errors.New("choose everything, or some environments, not both")
	}
	for _, e := range req.Environments {
		if err := validID(e); err != nil || strings.Contains(e, machineSep) {
			return Observer{}, fmt.Errorf("an observer window shows this computer's environments: %q", e)
		}
	}
	if req.Hours < 1 || req.Hours > 168 {
		return Observer{}, errors.New("an observer window lasts 1 to 168 hours")
	}
	params := map[string]any{
		"name":        "desktop window " + time.Now().Format("2006-01-02 15:04"),
		"scope":       map[string]any{"all": req.All, "environments": req.Environments},
		"screens":     req.Screens,
		"ttl_seconds": req.Hours * 3600,
	}
	var res struct {
		Credential struct {
			ID        string    `json:"id"`
			Name      string    `json:"name"`
			Screens   bool      `json:"screens"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"credential"`
		Token  string `json:"token"`
		Socket string `json:"socket"`
	}
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		return c.Call(ctx, "observer.create", params, &res)
	})
	if err != nil {
		return Observer{}, err
	}
	scope := "everything"
	if !req.All {
		scope = strings.Join(req.Environments, ", ")
	}
	return Observer{
		ID: res.Credential.ID, Name: res.Credential.Name, Scope: scope, Screens: res.Credential.Screens,
		ExpiresAt: res.Credential.ExpiresAt, token: res.Token, socket: res.Socket,
	}, nil
}

// RevokeObserver ends a credential: its connections close at once.
func (h *Hub) RevokeObserver(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		return c.Call(ctx, "observer.revoke", map[string]string{"id": id}, nil)
	})
}

// ObservedView is what an observer window shows: no command lines,
// environment variables or paths beyond the environment's own.
type ObservedView struct {
	Observer  Observer  `json:"observer"`
	State     string    `json:"state"` // connecting, connected, ended
	Message   string    `json:"message,omitempty"`
	Workspace Workspace `json:"workspace"`
}

// ObservedScreen is a watched pane's screen, as text.
type ObservedScreen struct {
	Pane     string        `json:"pane"`
	Keyframe bool          `json:"keyframe"`
	Width    int           `json:"width"`
	Height   int           `json:"height"`
	Lines    []ObservedRow `json:"lines"`
	Cursor   CursorState   `json:"cursor"`
	Ended    bool          `json:"ended,omitempty"`
}

// ObservedRow is a line of text.
type ObservedRow struct {
	Y    int    `json:"y"`
	Text string `json:"text"`
}

// Observation follows the daemon for one observer window.
type Observation struct {
	obs  Observer
	emit func(name string, data any)
	log  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	view    ObservedView
	client  *hiveapi.Observer
	watches map[string]context.CancelFunc
}

// Observe starts following the daemon with obs's token; emit sends to the
// window.
func Observe(obs Observer, emit func(string, any), log *slog.Logger) *Observation {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	o := &Observation{obs: obs, emit: emit, log: log, watches: map[string]context.CancelFunc{}}
	o.view = ObservedView{Observer: obs, State: StateConnecting, Workspace: workspaceOfObserved(hiveapi.ObserverSnapshot{})}
	o.ctx, o.cancel = context.WithCancel(context.Background())
	o.wg.Go(o.loop)
	return o
}

// View is the current view.
func (o *Observation) View() ObservedView {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.view
}

// Close stops following; the caller revokes the credential.
func (o *Observation) Close() {
	o.cancel()
	o.wg.Wait()
}

func (o *Observation) setView(change func(*ObservedView)) {
	o.mu.Lock()
	change(&o.view)
	v := o.view
	o.mu.Unlock()
	o.emit(EventObserveView, v)
}

func (o *Observation) loop() {
	backoff := 500 * time.Millisecond
	for o.ctx.Err() == nil {
		err := o.follow()
		if o.ctx.Err() != nil {
			return
		}
		msg := "the daemon went away; reconnecting"
		if err != nil {
			msg = err.Error()
		}
		if errors.Is(err, errObserverEnded) {
			o.setView(func(v *ObservedView) { v.State, v.Message = "ended", msg })
			return
		}
		o.setView(func(v *ObservedView) { v.State, v.Message = StateConnecting, msg })
		select {
		case <-o.ctx.Done():
			return
		case <-time.After(backoff):
			backoff = min(backoff*2, 15*time.Second)
		}
	}
}

var errObserverEnded = errors.New("this window's access ended (revoked or expired)")

func (o *Observation) follow() error {
	ctx, cancel := context.WithTimeout(o.ctx, 10*time.Second)
	c, err := hiveapi.Observe(ctx, hiveapi.ObserverOptions{Socket: o.obs.socket, Token: o.obs.token, Name: "desktop observer window"})
	cancel()
	if err != nil {
		if hiveapi.IsCode(err, hiveapi.CodePermissionDenied) {
			return errObserverEnded // revoked, expired or unknown
		}
		return err
	}
	defer c.Close()
	o.mu.Lock()
	o.client = c
	panes := make([]string, 0, len(o.watches))
	for p := range o.watches {
		panes = append(panes, p)
	}
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.client = nil
		o.mu.Unlock()
	}()
	events, _, err := c.Events(o.ctx, hiveapi.EventsRequest{})
	if err != nil {
		return err
	}
	defer events.Close()
	if err := o.refresh(c); err != nil {
		return err
	}
	for _, p := range panes {
		o.startWatch(c, p)
	}
	changed := make(chan struct{}, 1)
	readErr := make(chan error, 1)
	go func() {
		for {
			if _, err := events.Next(); err != nil {
				readErr <- err
				return
			}
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	for {
		select {
		case <-o.ctx.Done():
			return nil
		case <-c.Done():
			return errors.New("the observer connection ended")
		case err := <-readErr:
			return err
		case <-changed:
			select {
			case <-time.After(refreshEvery):
			case <-o.ctx.Done():
				return nil
			}
			if err := o.refresh(c); err != nil {
				return err
			}
		}
	}
}

func (o *Observation) refresh(c *hiveapi.Observer) error {
	ctx, cancel := context.WithTimeout(o.ctx, 10*time.Second)
	defer cancel()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return err
	}
	ws := workspaceOfObserved(snap)
	o.setView(func(v *ObservedView) { v.State, v.Message, v.Workspace = StateConnected, "", ws })
	return nil
}

// workspaceOfObserved turns what an observer sees into the view model.
func workspaceOfObserved(s hiveapi.ObserverSnapshot) Workspace {
	w := Workspace{Environments: []Environment{}, Tabs: []Tab{}, Panes: []Pane{}, Agents: []Agent{}, Counts: map[string]int{}}
	for _, e := range s.Environments {
		env := Environment{ID: e.ID, Name: cmp(e.Name, e.ID), Path: e.Path, Worktree: e.Worktree != nil}
		if e.Git != nil {
			env.Branch = e.Git.Branch
		}
		w.Environments = append(w.Environments, env)
	}
	for _, t := range s.Tabs {
		w.Tabs = append(w.Tabs, Tab{ID: t.ID, EnvironmentID: t.EnvironmentID, Name: t.Name, Active: t.Active, Focused: t.Focused, Zoomed: t.Zoomed, Width: t.Width, Height: t.Height})
	}
	agentOf := map[string]string{}
	for _, a := range s.Agents {
		if a.Kind == "" {
			continue
		}
		agentOf[a.PaneID] = a.ID
		w.Agents = append(w.Agents, Agent{
			ID: a.ID, Kind: a.Kind, Name: a.Name, EnvironmentID: a.EnvironmentID, TabID: a.TabID, PaneID: a.PaneID,
			State: string(a.State), Since: a.Since, Reason: a.Reason, StateSeq: a.StateSeq,
		})
		w.Counts[string(a.State)]++
		if stateRank[string(a.State)] > stateRank[w.State] {
			w.State = string(a.State)
		}
	}
	for _, p := range s.Panes {
		pn := Pane{
			ID: p.ID, TabID: p.TabID, EnvironmentID: p.EnvironmentID, ProcessID: p.ProcessID, Name: p.Name,
			Focused: p.Focused, Zoomed: p.Zoomed, Popup: p.Popup, Agent: agentOf[p.ID],
		}
		if p.Rect != nil {
			pn.Rect = &Rect{X: p.Rect.X, Y: p.Rect.Y, W: p.Rect.W, H: p.Rect.H}
		}
		if p.Process != nil {
			pn.Running = p.Process.Status == "running" || p.Process.Status == "starting"
			pn.Exit = p.Process.ExitCode
		}
		w.Panes = append(w.Panes, pn)
	}
	return w
}

// Watch streams a pane's screen to the window (when the credential lets it
// read screens).
func (o *Observation) Watch(pane string) error {
	if err := validID(pane); err != nil {
		return err
	}
	if !o.obs.Screens {
		return errors.New("this window may see states, not screens")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.watches[pane]; ok {
		return nil
	}
	o.watches[pane] = func() {}
	if o.client != nil {
		o.startWatchLocked(o.client, pane)
	}
	return nil
}

// Unwatch stops streaming a pane.
func (o *Observation) Unwatch(pane string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if stop, ok := o.watches[pane]; ok {
		stop()
		delete(o.watches, pane)
	}
}

func (o *Observation) startWatch(c *hiveapi.Observer, pane string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.watches[pane]; ok {
		o.startWatchLocked(c, pane)
	}
}

func (o *Observation) startWatchLocked(c *hiveapi.Observer, pane string) {
	ctx, cancel := context.WithCancel(o.ctx)
	o.watches[pane] = cancel
	o.wg.Go(func() {
		defer cancel()
		s, _, err := c.Watch(ctx, hiveapi.WatchRequest{Pane: pane, IntervalMS: 50})
		if err != nil {
			o.log.Debug("observer watch failed", "pane", pane, "err", err)
			o.emit(EventObserveScreen, ObservedScreen{Pane: pane, Ended: true, Lines: []ObservedRow{}})
			return
		}
		stop := context.AfterFunc(ctx, func() { _ = s.Close() })
		defer stop()
		for {
			u, err := s.Next()
			if err != nil {
				if ctx.Err() == nil {
					o.emit(EventObserveScreen, ObservedScreen{Pane: pane, Ended: true, Lines: []ObservedRow{}})
				}
				return
			}
			out := ObservedScreen{
				Pane: pane, Keyframe: u.Keyframe, Width: u.Width, Height: u.Height, Lines: make([]ObservedRow, 0, len(u.Lines)),
				Cursor: CursorState{X: u.Cursor.X, Y: u.Cursor.Y, Visible: u.Cursor.Visible, Shape: "block"},
			}
			for _, l := range u.Lines {
				out.Lines = append(out.Lines, ObservedRow{Y: l.Y, Text: l.Text})
			}
			o.emit(EventObserveScreen, out)
		}
	})
}
