package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// The observer API. Every method only reads; none exists that writes to a
// terminal, changes a layout or starts and stops anything.
const (
	methodAuth     = "observe.auth"
	methodSchema   = "observe.schema"
	methodStatus   = "observe.status"
	methodSnapshot = "observe.snapshot"
	methodPane     = "observe.pane"
	methodScreen   = "observe.screen"
	methodOutput   = "observe.output"
	methodHistory  = "observe.history"
	methodEvents   = "observe.events"
	methodWatch    = "observe.watch"
	methodInsights = "observe.insights"
)

// Screen and output bounds.
const (
	// MaxScrollback bounds observe.screen's history lines.
	MaxScrollback = 1000
	// DefaultOutputLines and MaxOutputLines bound observe.output.
	DefaultOutputLines = 100
	MaxOutputLines     = 1000
	// Watch update intervals: an observer gets at most one screen update
	// per interval, however fast the screen changes.
	DefaultWatchInterval = 250 * time.Millisecond
	MinWatchInterval     = 100 * time.Millisecond
	MaxWatchInterval     = 10 * time.Second
	// DefaultInsightsAsks and MaxInsightsAsks bound observe.insights's
	// asks.
	DefaultInsightsAsks = 20
	MaxInsightsAsks     = 1000
	// rescopeEvery is how often a watch re-checks its pane is in scope
	// (a pane can move to another environment).
	rescopeEvery = time.Second
)

// AuthRequest authenticates a connection (observe.auth).
type AuthRequest struct {
	Token string `json:"token"`
}

// AuthResult is the credential the connection now acts as, and where the
// event stream is.
type AuthResult struct {
	Credential Credential `json:"credential"`
	Epoch      string     `json:"epoch"`
	Seq        uint64     `json:"seq"`
}

// Status is observe.status: the daemon's health and what the observer can
// see of it.
type Status struct {
	Info
	APILevel     int        `json:"api_level"`
	Epoch        string     `json:"epoch"`
	Seq          uint64     `json:"seq"`
	Credential   Credential `json:"credential"`
	Environments int        `json:"environments"`
	Panes        int        `json:"panes"`
	Agents       int        `json:"agents"`
}

// PaneRequest names a pane.
type PaneRequest struct {
	Pane string `json:"pane"`
}

// PaneResult is one pane with its agent and metadata, when it has them.
type PaneResult struct {
	Pane     Pane      `json:"pane"`
	Agent    *Agent    `json:"agent,omitempty"`
	Metadata *Metadata `json:"metadata,omitempty"`
}

// ScreenRequest asks for a pane's screen (observe.screen).
type ScreenRequest struct {
	Pane string `json:"pane"`
	// ANSI keeps SGR styling in the lines.
	ANSI bool `json:"ansi,omitempty"`
	// Scrollback also returns up to this many lines above the screen
	// (at most MaxScrollback).
	Scrollback int `json:"scrollback,omitempty"`
}

// OutputRequest asks for a pane's recent output (observe.output).
type OutputRequest struct {
	Pane string `json:"pane"`
	// Lines is how many of the newest lines (DefaultOutputLines; at most
	// MaxOutputLines).
	Lines int  `json:"lines,omitempty"`
	ANSI  bool `json:"ansi,omitempty"`
}

// OutputResult is a pane's recent output, oldest line first.
type OutputResult struct {
	Pane  string   `json:"pane"`
	Lines []string `json:"lines"`
}

// HistoryRequest asks for events after Since in Epoch (observe.history).
type HistoryRequest struct {
	Epoch string `json:"epoch"`
	Since uint64 `json:"since"`
	// Limit bounds the page (DefaultPage; at most MaxPage).
	Limit int `json:"limit,omitempty"`
}

// EventsRequest opens an event stream (observe.events). With Since set (in
// Epoch), retained events after it are replayed first; a position that
// cannot be caught up from starts the stream with a Resync.
type EventsRequest struct {
	Epoch string  `json:"epoch,omitempty"`
	Since *uint64 `json:"since,omitempty"`
}

// EventsResult says where the stream starts: live events follow Seq.
type EventsResult struct {
	Epoch string `json:"epoch"`
	Seq   uint64 `json:"seq"`
}

// WatchRequest opens a pane's screen stream (observe.watch): a keyframe,
// then the lines that changed, at most one update per interval.
type WatchRequest struct {
	Pane       string `json:"pane"`
	ANSI       bool   `json:"ansi,omitempty"`
	IntervalMS int    `json:"interval_ms,omitempty"`
}

// WatchResult names what is watched.
type WatchResult struct {
	Pane      string `json:"pane"`
	ProcessID string `json:"process_id"`
}

// InsightsRequest asks for time and usage over a range (observe.insights):
// today by default, by kind.
type InsightsRequest struct {
	From time.Time `json:"from,omitzero" desc:"from this time (default: the start of today, the daemon's local time)"`
	To   time.Time `json:"to,omitzero" desc:"until this time (default: now)"`
	By   string    `json:"by,omitempty" desc:"group rows by kind (default), agent, environment, task, day or model"`
	// Asks is how many of the latest asks (DefaultInsightsAsks; at most
	// MaxInsightsAsks).
	Asks int `json:"asks,omitempty" desc:"how many of the latest asks (default 20, at most 1000)"`
	// Within narrows what is counted to a part of the credential's scope:
	// what a page showing part of it may show.
	Within *Scope `json:"within,omitempty" desc:"count only what is also in this scope"`
}

// Insights is the time and usage of what the credential sees: a summary,
// its budgets, and the latest times agents waited on a person (what they
// asked only with the screens permission).
type Insights struct {
	Summary insights.Summary        `json:"summary"`
	Budgets []insights.BudgetStatus `json:"budgets"`
	Asks    []insights.Ask          `json:"asks"`
}

func (s *Server) register(r *protocol.Router) {
	r.MustRegister(methodAuth, protocol.Method(func(ctx context.Context, p AuthRequest) (AuthResult, error) {
		client := ""
		if peer := protocol.PeerOf(ctx); peer != nil {
			client = peer.Client
		}
		cred, err := sessionOf(ctx).authenticate(p.Token, client)
		if err != nil {
			return AuthResult{}, err
		}
		return AuthResult{Credential: cred, Epoch: s.cfg.History.Epoch(), Seq: s.cfg.History.Last()}, nil
	}))
	r.MustRegister(methodSchema, protocol.Method(func(context.Context, protocol.Empty) (protocol.Schema, error) {
		return s.router.Schema(), nil
	}))
	r.MustRegister(methodStatus, protocol.Method(s.status))
	r.MustRegister(methodSnapshot, protocol.Method(func(ctx context.Context, _ protocol.Empty) (Snapshot, error) {
		cred, err := sessionOf(ctx).credential()
		if err != nil {
			return Snapshot{}, err
		}
		return s.snapshot(ctx, cred.Scope)
	}))
	r.MustRegister(methodPane, protocol.Method(s.pane))
	r.MustRegister(methodScreen, protocol.Method(s.screen))
	r.MustRegister(methodOutput, protocol.Method(s.output))
	r.MustRegister(methodHistory, protocol.Method(func(ctx context.Context, p HistoryRequest) (Page, error) {
		cred, err := sessionOf(ctx).credential()
		if err != nil {
			return Page{}, err
		}
		return s.cfg.History.Page(cred.Scope, p.Epoch, p.Since, p.Limit), nil
	}))
	r.MustRegister(methodEvents, protocol.PipeMethod(s.events))
	r.MustRegister(methodWatch, protocol.PipeMethod(s.watch))
	r.MustRegister(methodInsights, protocol.Method(s.insights))
}

// insights answers observe.insights: only rows of what the credential sees
// (and Within) are counted, budgets show where their keys are seen, and
// asks' reasons, read from agents' screens, need screens.
func (s *Server) insights(ctx context.Context, p InsightsRequest) (Insights, error) {
	c := sessionOf(ctx)
	cred, err := c.credential()
	if err != nil {
		return Insights{}, err
	}
	src := s.cfg.Sources.Insights
	if src == nil {
		return Insights{}, protocol.NewError(protocol.ErrorCodeUnavailable, errors.New("observer: this daemon keeps no insights"))
	}
	if p.Within != nil {
		if err := p.Within.Validate(); err != nil {
			return Insights{}, protocol.NewError(protocol.ErrorCodeInvalidParams, err)
		}
	}
	sees := func(t Target) bool { return cred.Scope.Sees(t) && (p.Within == nil || p.Within.Sees(t)) }
	q := insights.Query{
		From: p.From, To: p.To, By: p.By,
		Sees: func(env, pane string) bool { return sees(Target{Environment: env, Pane: pane}) },
	}
	sum, err := src.Summary(ctx, q)
	if err != nil {
		return Insights{}, insightsError(err)
	}
	n := p.Asks
	if n <= 0 {
		n = DefaultInsightsAsks
	}
	asks, err := src.Asks(ctx, q, min(n, MaxInsightsAsks))
	if err != nil {
		return Insights{}, insightsError(err)
	}
	if !cred.Screens {
		for i := range asks {
			asks[i].Reason = ""
		}
	}
	out := Insights{Summary: sum, Budgets: []insights.BudgetStatus{}, Asks: asks}
	if out.Asks == nil {
		out.Asks = []insights.Ask{}
	}
	for _, b := range src.Budgets(ctx) {
		if sees(Target{Environment: b.Target()}) {
			out.Budgets = append(out.Budgets, b)
		}
	}
	return out, nil
}

// insightsError gives package insights' errors their protocol codes.
func insightsError(err error) error {
	if errors.Is(err, insights.ErrInvalid) {
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}

func (s *Server) status(ctx context.Context, _ protocol.Empty) (Status, error) {
	cred, err := sessionOf(ctx).credential()
	if err != nil {
		return Status{}, err
	}
	snap, err := s.snapshot(ctx, cred.Scope)
	if err != nil {
		return Status{}, err
	}
	st := Status{
		APILevel: protocol.APILevel, Epoch: snap.Epoch, Seq: snap.Seq, Credential: cred,
		Environments: len(snap.Environments), Panes: len(snap.Panes), Agents: len(snap.Agents),
	}
	if s.cfg.Sources.Info != nil {
		st.Info = s.cfg.Sources.Info()
	}
	return st, nil
}

// snapshot reads everything in scope. Its position is read first, so every
// event after it is either in the snapshot already or still to come.
func (s *Server) snapshot(ctx context.Context, scope Scope) (Snapshot, error) {
	src := s.cfg.Sources
	snap := Snapshot{
		Epoch: s.cfg.History.Epoch(), Seq: s.cfg.History.Last(),
		Environments: []Environment{}, Tabs: []Tab{}, Panes: []Pane{}, Agents: []Agent{}, Metadata: []Metadata{},
	}
	envs, err := src.Environments.List(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	for _, e := range envs {
		if scope.Environment(e.ID) {
			snap.Environments = append(snap.Environments, EnvironmentView(e))
		}
	}
	tabs, err := src.Panes.Tabs(ctx, "")
	if err != nil {
		return Snapshot{}, err
	}
	for _, t := range tabs {
		if scope.Environment(t.EnvironmentID) {
			snap.Tabs = append(snap.Tabs, TabView(t))
		}
	}
	panes, err := src.Panes.Panes(ctx, "", "")
	if err != nil {
		return Snapshot{}, err
	}
	paneEnv := map[string]string{}
	for _, p := range panes {
		paneEnv[p.ID] = p.EnvironmentID
		if scope.Pane(p.EnvironmentID, p.ID) {
			snap.Panes = append(snap.Panes, PaneView(p))
		}
	}
	agents, err := src.Agents.List(ctx, false)
	if err != nil {
		return Snapshot{}, err
	}
	for _, a := range agents {
		if scope.Sees(Target{Environment: a.EnvironmentID, Pane: a.PaneID}) {
			snap.Agents = append(snap.Agents, AgentView(a))
		}
	}
	if src.Metadata != nil {
		for _, e := range src.Metadata() {
			t := Target{Environment: e.Environment, Pane: e.Pane}
			if t.Environment == "" {
				t.Environment = paneEnv[e.Pane]
			}
			if scope.Sees(t) {
				snap.Metadata = append(snap.Metadata, MetadataView(e))
			}
		}
	}
	if src.Plans != nil {
		for _, p := range src.Plans.All(ctx) {
			if v, ok := PlanView(p, scope); ok {
				snap.Plans = append(snap.Plans, v)
			}
		}
	}
	return snap, nil
}

// paneInScope returns pane id when the credential may see it. A pane out
// of scope is reported exactly like one that does not exist.
func (s *Server) paneInScope(ctx context.Context, c *session, cred Credential, id string) (pane.Pane, error) {
	notFound := protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf("observer: no pane %q in this observer's scope", id))
	if id == "" {
		return pane.Pane{}, protocol.NewError(protocol.ErrorCodeInvalidParams, errors.New("observer: pane is required"))
	}
	p, err := s.cfg.Sources.Panes.Pane(ctx, id)
	if err != nil {
		return pane.Pane{}, notFound
	}
	if !cred.Scope.Pane(p.EnvironmentID, p.ID) {
		c.denied(cred, "pane "+id)
		return pane.Pane{}, notFound
	}
	return p, nil
}

// screens checks the credential allows screen contents.
func screens(c *session, cred Credential, what string) error {
	if cred.Screens {
		return nil
	}
	c.denied(cred, what+" (no screens permission)")
	return protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New("observer: this credential does not allow screen contents (create it with --screens)"))
}

func (s *Server) pane(ctx context.Context, p PaneRequest) (PaneResult, error) {
	c := sessionOf(ctx)
	cred, err := c.credential()
	if err != nil {
		return PaneResult{}, err
	}
	pn, err := s.paneInScope(ctx, c, cred, p.Pane)
	if err != nil {
		return PaneResult{}, err
	}
	res := PaneResult{Pane: PaneView(pn)}
	if agents, err := s.cfg.Sources.Agents.List(ctx, false); err == nil {
		for _, a := range agents {
			if a.PaneID == pn.ID {
				v := AgentView(a)
				res.Agent = &v
			}
		}
	}
	if s.cfg.Sources.Metadata != nil {
		for _, e := range s.cfg.Sources.Metadata() {
			if e.Pane == pn.ID {
				v := MetadataView(e)
				res.Metadata = &v
			}
		}
	}
	return res, nil
}

// terminalOf returns the terminal of an in-scope pane, for a credential
// with screen access.
func (s *Server) terminalOf(ctx context.Context, paneID, what string) (pane.Pane, terminal.Session, error) {
	c := sessionOf(ctx)
	cred, err := c.credential()
	if err != nil {
		return pane.Pane{}, nil, err
	}
	pn, err := s.paneInScope(ctx, c, cred, paneID)
	if err != nil {
		return pane.Pane{}, nil, err
	}
	if err := screens(c, cred, what+" "+paneID); err != nil {
		return pane.Pane{}, nil, err
	}
	sess, err := s.cfg.Sources.Terminals.Get(pn.ProcessID)
	if err != nil {
		return pane.Pane{}, nil, protocol.NewError(protocol.ErrorCodeUnavailable, fmt.Errorf("observer: pane %s has no terminal", paneID))
	}
	return pn, sess, nil
}

func (s *Server) screen(ctx context.Context, p ScreenRequest) (Screen, error) {
	pn, sess, err := s.terminalOf(ctx, p.Pane, "screen")
	if err != nil {
		return Screen{}, err
	}
	scr, err := sess.Snapshot(ctx)
	if err != nil {
		return Screen{}, protocol.NewError(protocol.ErrorCodeUnavailable, err)
	}
	res := Screen{
		Pane: pn.ID, Width: scr.Cols, Height: scr.Rows, Cursor: CursorView(scr.Cursor), Title: scr.Title,
		AltScreen: scr.Modes&vt.ModeAltScreen != 0, Lines: make([]string, 0, scr.Rows),
	}
	for y := range scr.Rows {
		if p.ANSI {
			res.Lines = append(res.Lines, vt.LineANSI(scr.Lines[y]))
		} else {
			res.Lines = append(res.Lines, scr.LineText(y))
		}
	}
	if p.Scrollback > 0 {
		res.Scrollback, err = sess.Read(ctx, terminal.ReadRequest{Source: terminal.SourceHistory, Lines: min(p.Scrollback, MaxScrollback), ANSI: p.ANSI})
		if err != nil {
			return Screen{}, protocol.NewError(protocol.ErrorCodeUnavailable, err)
		}
	}
	return res, nil
}

func (s *Server) output(ctx context.Context, p OutputRequest) (OutputResult, error) {
	pn, sess, err := s.terminalOf(ctx, p.Pane, "output")
	if err != nil {
		return OutputResult{}, err
	}
	n := p.Lines
	if n <= 0 {
		n = DefaultOutputLines
	}
	lines, err := sess.Read(ctx, terminal.ReadRequest{Source: terminal.SourceRecent, Lines: min(n, MaxOutputLines), ANSI: p.ANSI})
	if err != nil {
		return OutputResult{}, protocol.NewError(protocol.ErrorCodeUnavailable, err)
	}
	if lines == nil {
		lines = []string{}
	}
	return OutputResult{Pane: pn.ID, Lines: lines}, nil
}

func (s *Server) events(ctx context.Context, p EventsRequest) (EventsResult, protocol.PipeFunc, error) {
	c := sessionOf(ctx)
	cred, err := c.credential()
	if err != nil {
		return EventsResult{}, nil, err
	}
	if err := c.openStream(); err != nil {
		return EventsResult{}, nil, err
	}
	feed, at := s.cfg.History.Subscribe(cred.Scope, p.Epoch, p.Since)
	return EventsResult{Epoch: s.cfg.History.Epoch(), Seq: at}, func(ctx context.Context, pipe protocol.Pipe) error {
		defer c.closeStream()
		defer feed.Close()
		ctx, stop := protocol.UntilClosed(ctx, pipe)
		defer stop()
		for {
			ev, err := feed.Next(ctx)
			if err != nil {
				return nil //nolint:nilerr // the observer closed the stream or the connection ended
			}
			line, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := pipe.Write(append(line, '\n')); err != nil {
				return nil //nolint:nilerr // the observer went away
			}
		}
	}, nil
}

// errOutOfScope ends a watch whose pane left the observer's scope.
var errOutOfScope = errors.New("observer: the pane left this observer's scope")

func (s *Server) watch(ctx context.Context, p WatchRequest) (WatchResult, protocol.PipeFunc, error) {
	c := sessionOf(ctx)
	pn, sess, err := s.terminalOf(ctx, p.Pane, "watch")
	if err != nil {
		return WatchResult{}, nil, err
	}
	if err := c.openStream(); err != nil {
		return WatchResult{}, nil, err
	}
	interval := DefaultWatchInterval
	if p.IntervalMS > 0 {
		interval = min(max(time.Duration(p.IntervalMS)*time.Millisecond, MinWatchInterval), MaxWatchInterval)
	}
	return WatchResult{Pane: pn.ID, ProcessID: pn.ProcessID}, func(ctx context.Context, pipe protocol.Pipe) error {
		defer c.closeStream()
		s.watching(pn.ID, 1)
		defer s.watching(pn.ID, -1)
		ctx, stop := protocol.UntilClosed(ctx, pipe)
		defer stop()
		checked := time.Now()
		err := sess.Frames(ctx, func(f *vt.Frame) error {
			if time.Since(checked) >= rescopeEvery {
				checked = time.Now()
				cred, err := c.credential()
				if err != nil {
					return err
				}
				if _, err := s.paneInScope(ctx, c, cred, pn.ID); err != nil {
					return errOutOfScope
				}
			}
			line, err := json.Marshal(UpdateFromFrame(f, p.ANSI))
			if err != nil {
				return err
			}
			if _, err := pipe.Write(append(line, '\n')); err != nil {
				return err
			}
			// Changes made meanwhile are coalesced into the next frame.
			t := time.NewTimer(interval)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		})
		if errors.Is(err, errOutOfScope) {
			return protocol.NewError(protocol.ErrorCodePermissionDenied, err)
		}
		return nil
	}, nil
}
