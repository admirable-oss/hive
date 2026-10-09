package pane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// Processes is what panes need from process supervision.
type Processes interface {
	Start(ctx context.Context, req process.StartRequest) (process.Process, error)
	Get(ctx context.Context, id string) (process.Process, error)
	Stop(ctx context.Context, id string) error
	Move(ctx context.Context, id, envID string) (process.Process, error)
}

// Terminals finds a running process's terminal.
type Terminals interface {
	Get(processID string) (terminal.Session, error)
}

// Environments is what panes need from environments.
type Environments interface {
	Get(ctx context.Context, id string) (environment.Environment, error)
	List(ctx context.Context) ([]environment.Environment, error)
	Create(ctx context.Context, req environment.CreateRequest) (environment.Environment, error)
}

// Events receives workspace events.
type Events interface {
	Publish(typ string, data any)
}

// Event types this package publishes. Data is the Tab or Pane concerned.
const (
	EventTabCreated    = "tab.created"
	EventTabClosed     = "tab.closed"
	EventTabUpdated    = "tab.updated" // renamed, focused, resized
	EventPaneCreated   = "pane.created"
	EventPaneClosed    = "pane.closed"
	EventPaneMoved     = "pane.moved"
	EventPaneFocused   = "pane.focused"
	EventPaneUpdated   = "pane.updated" // renamed
	EventLayoutUpdated = "layout.updated"
)

// Service manages tabs and panes. Every change is saved before it returns.
type Service struct {
	cfg    Config
	store  Store
	procs  Processes
	terms  Terminals
	envs   Environments
	events Events
	log    *slog.Logger

	mu       sync.Mutex
	states   map[string]*State // by environment
	loaded   bool              // every environment's state is in states
	nowFn    func() time.Time
	idSource func(prefix string) string
}

// NewService returns a pane service.
func NewService(cfg Config, store Store, procs Processes, terms Terminals, envs Environments, events Events) *Service {
	if !cfg.Size.Valid() {
		cfg.Size = terminal.DefaultSize
	}
	if events == nil {
		events = nopEvents{}
	}
	return &Service{
		cfg: cfg, store: store, procs: procs, terms: terms, envs: envs, events: events,
		log:    logging.OrDiscard(cfg.Logger),
		states: map[string]*State{},
		nowFn:  time.Now,
		idSource: func(prefix string) string {
			b := make([]byte, 6)
			_, _ = rand.Read(b)
			return prefix + hex.EncodeToString(b)
		},
	}
}

type nopEvents struct{}

func (nopEvents) Publish(string, any) {}

// shell is the command a pane runs when none is given.
func (s *Service) shell() []string {
	if len(s.cfg.Shell) > 0 {
		return s.cfg.Shell
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh, "-l"}
	}
	return []string{"/bin/sh"}
}

// --- state access (callers hold mu) ---

func (s *Service) stateLocked(ctx context.Context, envID string) (*State, environment.Environment, error) {
	env, err := s.envs.Get(ctx, envID)
	if err != nil {
		return nil, env, err
	}
	if st, ok := s.states[envID]; ok {
		return st, env, nil
	}
	st, err := s.store.Load(ctx, envID)
	if err != nil {
		return nil, env, err
	}
	s.states[envID] = &st
	return &st, env, nil
}

// loadAllLocked brings every environment's state into memory, so tabs and
// panes can be found by ID alone.
func (s *Service) loadAllLocked(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	envs, err := s.envs.List(ctx)
	if err != nil {
		return err
	}
	for _, env := range envs {
		if _, _, err := s.stateLocked(ctx, env.ID); err != nil {
			return err
		}
	}
	s.loaded = true
	return nil
}

func (s *Service) saveLocked(ctx context.Context, envID string) error {
	st, ok := s.states[envID]
	if !ok {
		return nil
	}
	return s.store.Save(ctx, envID, *st)
}

func (s *Service) tabLocked(ctx context.Context, tabID string) (*State, *Tab, error) {
	if err := s.loadAllLocked(ctx); err != nil {
		return nil, nil, err
	}
	for _, st := range s.states {
		for i := range st.Tabs {
			if st.Tabs[i].ID == tabID {
				return st, &st.Tabs[i], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("%w: %q", ErrTabNotFound, tabID)
}

func (s *Service) paneLocked(ctx context.Context, paneID string) (*State, *Tab, *Pane, error) {
	if err := s.loadAllLocked(ctx); err != nil {
		return nil, nil, nil, err
	}
	for _, st := range s.states {
		for i := range st.Panes {
			if st.Panes[i].ID == paneID {
				p := &st.Panes[i]
				for j := range st.Tabs {
					if st.Tabs[j].ID == p.TabID {
						return st, &st.Tabs[j], p, nil
					}
				}
				return nil, nil, nil, fmt.Errorf("%w: pane %q has no tab", ErrInvalid, paneID)
			}
		}
	}
	return nil, nil, nil, fmt.Errorf("%w: %q", ErrPaneNotFound, paneID)
}

// ForgetEnvironment drops the cached state of a deleted environment.
func (s *Service) ForgetEnvironment(envID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, envID)
}

// --- geometry ---

func area(t *Tab) layout.Rect { return layout.Rect{W: t.Width, H: t.Height} }

// popupRect centres a popup over the tab.
func popupRect(t *Tab, p Popup) layout.Rect {
	w, h := max(t.Width*p.WidthPct/100, 1), max(t.Height*p.HeightPct/100, 1)
	return layout.Rect{X: (t.Width - w) / 2, Y: (t.Height - h) / 2, W: w, H: h}
}

// rects returns every visible pane's area in t.
func rects(t *Tab) map[string]layout.Rect {
	g := layout.Geometry(t.Layout, area(t), t.Zoomed)
	for _, p := range t.Popups {
		g[p.Pane] = popupRect(t, p)
	}
	return g
}

// resizeTabLocked gives every visible pane's terminal its area's size.
func (s *Service) resizeTabLocked(st *State, t *Tab) {
	g := rects(t)
	for _, p := range st.Panes {
		r, ok := g[p.ID]
		if p.TabID != t.ID || !ok || r.W <= 0 || r.H <= 0 {
			continue
		}
		sess, err := s.terms.Get(p.ProcessID)
		if err != nil {
			continue // not running
		}
		if err := sess.Resize(terminal.Size{Width: uint16(r.W), Height: uint16(r.H)}); err != nil {
			s.log.Debug("resize pane", "pane", p.ID, "err", err)
		}
	}
}

// --- process start ---

// startLocked starts the process of a new pane at the given size.
func (s *Service) startLocked(ctx context.Context, env environment.Environment, tabID, paneID string, spec Spec, r layout.Rect) (process.Process, error) {
	argv := spec.Command
	if len(argv) == 0 {
		argv = s.shell()
	}
	vars := maps.Clone(spec.Env)
	if vars == nil {
		vars = map[string]string{}
	}
	vars["HIVE_PANE_ID"], vars["HIVE_TAB_ID"] = paneID, tabID
	return s.procs.Start(ctx, process.StartRequest{
		EnvironmentID: env.ID, Command: argv[0], Args: argv[1:], Cwd: spec.Cwd, Env: vars,
		Terminal: true, Width: uint16(max(r.W, 1)), Height: uint16(max(r.H, 1)),
	})
}

// --- tabs ---

// CreateTab adds a tab with one pane to an environment and focuses it.
func (s *Service) CreateTab(ctx context.Context, req CreateTabRequest) (Tab, Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createTabLocked(ctx, req.EnvironmentID, req.Name, req.Pane)
}

func (s *Service) createTabLocked(ctx context.Context, envID, name string, spec Spec) (Tab, Pane, error) {
	st, env, err := s.stateLocked(ctx, envID)
	if err != nil {
		return Tab{}, Pane{}, err
	}
	tab := Tab{
		ID: s.idSource("t"), EnvironmentID: envID, Name: name,
		Width: int(s.cfg.Size.Width), Height: int(s.cfg.Size.Height), CreatedAt: s.nowFn(),
	}
	if tab.Name == "" {
		tab.Name = fmt.Sprintf("tab %d", len(st.Tabs)+1)
	}
	pane := Pane{ID: s.idSource("p"), TabID: tab.ID, EnvironmentID: envID, Name: spec.Name, CreatedAt: s.nowFn()}
	proc, err := s.startLocked(ctx, env, tab.ID, pane.ID, spec, area(&tab))
	if err != nil {
		return Tab{}, Pane{}, err
	}
	pane.ProcessID = proc.ID
	tab.Layout, tab.Focused = layout.Leaf(pane.ID), pane.ID
	st.Tabs = append(st.Tabs, tab)
	st.Panes = append(st.Panes, pane)
	st.ActiveTab = tab.ID
	if err := s.saveLocked(ctx, envID); err != nil {
		return Tab{}, Pane{}, err
	}
	s.events.Publish(EventTabCreated, tab)
	s.events.Publish(EventPaneCreated, pane)
	described := s.describeLocked(ctx, &tab, pane)
	return tab, described, nil
}

// Tabs lists the tabs of an environment, or of every environment when envID
// is empty.
func (s *Service) Tabs(ctx context.Context, envID string) ([]Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if envID != "" {
		st, _, err := s.stateLocked(ctx, envID)
		if err != nil {
			return nil, err
		}
		return slices.Clone(st.Tabs), nil
	}
	if err := s.loadAllLocked(ctx); err != nil {
		return nil, err
	}
	var out []Tab
	for _, id := range slices.Sorted(maps.Keys(s.states)) {
		out = append(out, s.states[id].Tabs...)
	}
	return out, nil
}

// Tab returns a tab.
func (s *Service) Tab(ctx context.Context, tabID string) (Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, t, err := s.tabLocked(ctx, tabID)
	if err != nil {
		return Tab{}, err
	}
	return *t, nil
}

// RenameTab renames a tab.
func (s *Service) RenameTab(ctx context.Context, tabID, name string) (Tab, error) {
	if name == "" {
		return Tab{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, t, err := s.tabLocked(ctx, tabID)
	if err != nil {
		return Tab{}, err
	}
	t.Name = name
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Tab{}, err
	}
	s.events.Publish(EventTabUpdated, *t)
	return *t, nil
}

// FocusTab makes a tab its environment's active tab.
func (s *Service) FocusTab(ctx context.Context, tabID string) (Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, err := s.tabLocked(ctx, tabID)
	if err != nil {
		return Tab{}, err
	}
	st.ActiveTab = t.ID
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Tab{}, err
	}
	s.events.Publish(EventTabUpdated, *t)
	return *t, nil
}

// ResizeTab sets a tab's area (a client's size) and resizes its panes.
func (s *Service) ResizeTab(ctx context.Context, tabID string, width, height int) (Tab, error) {
	if width < 2 || height < 2 || width > 2000 || height > 1000 {
		return Tab{}, fmt.Errorf("%w: tab size %dx%d", ErrInvalid, width, height)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, err := s.tabLocked(ctx, tabID)
	if err != nil {
		return Tab{}, err
	}
	t.Width, t.Height = width, height
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Tab{}, err
	}
	s.resizeTabLocked(st, t)
	s.events.Publish(EventLayoutUpdated, *t)
	return *t, nil
}

// CloseTab stops every pane's process and removes the tab.
func (s *Service) CloseTab(ctx context.Context, tabID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, err := s.tabLocked(ctx, tabID)
	if err != nil {
		return err
	}
	tab := *t
	for _, p := range slices.Clone(st.Panes) {
		if p.TabID == tab.ID {
			s.stopLocked(ctx, p)
			st.Panes = slices.DeleteFunc(st.Panes, func(x Pane) bool { return x.ID == p.ID })
			s.events.Publish(EventPaneClosed, p)
		}
	}
	s.removeTabLocked(st, tab.ID)
	if err := s.saveLocked(ctx, tab.EnvironmentID); err != nil {
		return err
	}
	s.events.Publish(EventTabClosed, tab)
	return nil
}

func (s *Service) removeTabLocked(st *State, tabID string) {
	st.Tabs = slices.DeleteFunc(st.Tabs, func(t Tab) bool { return t.ID == tabID })
	if st.ActiveTab == tabID {
		st.ActiveTab = ""
		if len(st.Tabs) > 0 {
			st.ActiveTab = st.Tabs[len(st.Tabs)-1].ID
		}
	}
}

// stopLocked stops a pane's process if it runs.
func (s *Service) stopLocked(ctx context.Context, p Pane) {
	if proc, err := s.procs.Get(ctx, p.ProcessID); err == nil && proc.Active() {
		if err := s.procs.Stop(ctx, p.ProcessID); err != nil {
			s.log.Warn("stop pane process", "pane", p.ID, "err", err)
		}
	}
}

// --- panes ---

// Panes lists panes, filtered by environment and/or tab.
func (s *Service) Panes(ctx context.Context, envID, tabID string) ([]Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadAllLocked(ctx); err != nil {
		return nil, err
	}
	var out []Pane
	for _, id := range slices.Sorted(maps.Keys(s.states)) {
		if envID != "" && id != envID {
			continue
		}
		st := s.states[id]
		for i := range st.Tabs {
			t := &st.Tabs[i]
			if tabID != "" && t.ID != tabID {
				continue
			}
			for _, p := range st.Panes {
				if p.TabID == t.ID {
					out = append(out, s.describeLocked(ctx, t, p))
				}
			}
		}
	}
	return out, nil
}

// Pane returns a pane with its process, area and state.
func (s *Service) Pane(ctx context.Context, paneID string) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, t, p, err := s.paneLocked(ctx, paneID)
	if err != nil {
		return Pane{}, err
	}
	return s.describeLocked(ctx, t, *p), nil
}

// describeLocked fills in a pane's derived fields.
func (s *Service) describeLocked(ctx context.Context, t *Tab, p Pane) Pane {
	if proc, err := s.procs.Get(ctx, p.ProcessID); err == nil {
		p.Process = &proc
	}
	if r, ok := rects(t)[p.ID]; ok {
		p.Rect = &r
	}
	p.Focused = t.Focused == p.ID
	p.Zoomed = t.Zoomed == p.ID
	p.Popup = slices.ContainsFunc(t.Popups, func(x Popup) bool { return x.Pane == p.ID })
	return p
}

// Split starts a new pane next to an existing one.
func (s *Service) Split(ctx context.Context, req SplitRequest) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		st  *State
		t   *Tab
		err error
	)
	target := req.Pane
	switch {
	case target != "":
		st, t, _, err = s.paneLocked(ctx, target)
	case req.TabID != "":
		st, t, err = s.tabLocked(ctx, req.TabID)
		if err == nil {
			target = t.Focused
			if target == "" || !t.Layout.Contains(target) {
				if panes := t.Layout.Panes(); len(panes) > 0 {
					target = panes[0]
				}
			}
		}
	default:
		return Pane{}, fmt.Errorf("%w: a pane or tab to split is required", ErrInvalid)
	}
	if err != nil {
		return Pane{}, err
	}
	if !t.Layout.Contains(target) {
		return Pane{}, fmt.Errorf("%w: popups cannot be split", ErrInvalid)
	}
	d := req.Direction
	if d == "" {
		d = layout.Right
	}
	if _, err := layout.ParseDirection(string(d)); err != nil {
		return Pane{}, err
	}
	env, err := s.envs.Get(ctx, t.EnvironmentID)
	if err != nil {
		return Pane{}, err
	}

	pane := Pane{ID: s.idSource("p"), TabID: t.ID, EnvironmentID: t.EnvironmentID, Name: req.Spec.Name, CreatedAt: s.nowFn()}
	next, err := layout.Split(t.Layout.Clone(), target, d, req.Ratio, pane.ID)
	if err != nil {
		return Pane{}, err
	}
	r := layout.Geometry(next, area(t), "")[pane.ID]
	proc, err := s.startLocked(ctx, env, t.ID, pane.ID, req.Spec, r)
	if err != nil {
		return Pane{}, err
	}
	pane.ProcessID = proc.ID
	t.Layout, t.Zoomed = next, ""
	if req.Focus == nil || *req.Focus {
		t.Focused = pane.ID
	}
	st.Panes = append(st.Panes, pane)
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Pane{}, err
	}
	s.resizeTabLocked(st, t)
	s.events.Publish(EventPaneCreated, pane)
	s.events.Publish(EventLayoutUpdated, *t)
	return s.describeLocked(ctx, t, pane), nil
}

// Popup starts a pane floating over a tab.
func (s *Service) Popup(ctx context.Context, req PopupRequest) (Pane, error) {
	if req.WidthPct == 0 {
		req.WidthPct = 80
	}
	if req.HeightPct == 0 {
		req.HeightPct = 80
	}
	if req.WidthPct < 10 || req.WidthPct > 100 || req.HeightPct < 10 || req.HeightPct > 100 {
		return Pane{}, fmt.Errorf("%w: popup size must be 10..100%%", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, err := s.tabLocked(ctx, req.TabID)
	if err != nil {
		return Pane{}, err
	}
	env, err := s.envs.Get(ctx, t.EnvironmentID)
	if err != nil {
		return Pane{}, err
	}
	pane := Pane{ID: s.idSource("p"), TabID: t.ID, EnvironmentID: t.EnvironmentID, Name: req.Spec.Name, CreatedAt: s.nowFn()}
	popup := Popup{Pane: pane.ID, WidthPct: req.WidthPct, HeightPct: req.HeightPct}
	proc, err := s.startLocked(ctx, env, t.ID, pane.ID, req.Spec, popupRect(t, popup))
	if err != nil {
		return Pane{}, err
	}
	pane.ProcessID = proc.ID
	t.Popups = append(t.Popups, popup)
	t.Focused = pane.ID
	st.Panes = append(st.Panes, pane)
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Pane{}, err
	}
	s.events.Publish(EventPaneCreated, pane)
	return s.describeLocked(ctx, t, pane), nil
}

// Focus makes a pane its tab's focused pane, and its tab the active one.
func (s *Service) Focus(ctx context.Context, paneID string) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focusLocked(ctx, paneID)
}

func (s *Service) focusLocked(ctx context.Context, paneID string) (Pane, error) {
	st, t, p, err := s.paneLocked(ctx, paneID)
	if err != nil {
		return Pane{}, err
	}
	t.Focused, st.ActiveTab = p.ID, t.ID
	if t.Zoomed != "" && t.Zoomed != p.ID {
		t.Zoomed = "" // focusing another pane leaves zoom, as in tmux
	}
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Pane{}, err
	}
	s.events.Publish(EventPaneFocused, *p)
	return s.describeLocked(ctx, t, *p), nil
}

// FocusDirection focuses the pane next to paneID in direction d.
func (s *Service) FocusDirection(ctx context.Context, paneID string, d layout.Direction) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, t, _, err := s.paneLocked(ctx, paneID)
	if err != nil {
		return Pane{}, err
	}
	next, ok := layout.Neighbor(t.Layout, area(t), paneID, d)
	if !ok {
		return Pane{}, fmt.Errorf("%w: no pane %s of %s", ErrPaneNotFound, d, paneID)
	}
	return s.focusLocked(ctx, next)
}

// Resize grows a pane by cells towards d (negative cells shrink it).
func (s *Service) Resize(ctx context.Context, paneID string, d layout.Direction, cells int) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, p, err := s.paneLocked(ctx, paneID)
	if err != nil {
		return Pane{}, err
	}
	if err := layout.Resize(t.Layout, area(t), paneID, d, cells); err != nil {
		return Pane{}, err
	}
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Pane{}, err
	}
	s.resizeTabLocked(st, t)
	s.events.Publish(EventLayoutUpdated, *t)
	return s.describeLocked(ctx, t, *p), nil
}

// Zoom makes a pane fill its tab (on true), restores the layout (on false)
// or toggles (nil).
func (s *Service) Zoom(ctx context.Context, paneID string, on *bool) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, p, err := s.paneLocked(ctx, paneID)
	if err != nil {
		return Pane{}, err
	}
	if !t.Layout.Contains(paneID) {
		return Pane{}, fmt.Errorf("%w: popups cannot be zoomed", ErrInvalid)
	}
	zoom := t.Zoomed != paneID
	if on != nil {
		zoom = *on
	}
	t.Zoomed = ""
	if zoom {
		t.Zoomed, t.Focused = paneID, paneID
	}
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Pane{}, err
	}
	s.resizeTabLocked(st, t)
	s.events.Publish(EventLayoutUpdated, *t)
	return s.describeLocked(ctx, t, *p), nil
}

// Swap exchanges two panes of the same tab.
func (s *Service) Swap(ctx context.Context, a, b string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, _, err := s.paneLocked(ctx, a)
	if err != nil {
		return err
	}
	_, tb, _, err := s.paneLocked(ctx, b)
	if err != nil {
		return err
	}
	if t.ID != tb.ID {
		return fmt.Errorf("%w: swap needs two panes of one tab (use move between tabs)", ErrInvalid)
	}
	if err := layout.Swap(t.Layout, a, b); err != nil {
		return err
	}
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return err
	}
	s.resizeTabLocked(st, t)
	s.events.Publish(EventLayoutUpdated, *t)
	return nil
}

// Rename names a pane.
func (s *Service) Rename(ctx context.Context, paneID, name string) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, t, p, err := s.paneLocked(ctx, paneID)
	if err != nil {
		return Pane{}, err
	}
	p.Name = name
	if err := s.saveLocked(ctx, t.EnvironmentID); err != nil {
		return Pane{}, err
	}
	s.events.Publish(EventPaneUpdated, *p)
	return s.describeLocked(ctx, t, *p), nil
}

// Close stops a pane's process and removes the pane; a tab left without
// panes is closed too.
func (s *Service) Close(ctx context.Context, paneID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked(ctx, paneID, true)
}

func (s *Service) closeLocked(ctx context.Context, paneID string, stop bool) error {
	st, t, p, err := s.paneLocked(ctx, paneID)
	if err != nil {
		return err
	}
	pane := *p
	if stop {
		s.stopLocked(ctx, pane)
	}
	tabClosed := s.detachLocked(st, t, pane.ID)
	st.Panes = slices.DeleteFunc(st.Panes, func(x Pane) bool { return x.ID == pane.ID })
	if err := s.saveLocked(ctx, pane.EnvironmentID); err != nil {
		return err
	}
	s.events.Publish(EventPaneClosed, pane)
	if tabClosed != nil {
		s.events.Publish(EventTabClosed, *tabClosed)
	} else {
		s.resizeTabLocked(st, t)
		s.events.Publish(EventLayoutUpdated, *t)
	}
	return nil
}

// detachLocked takes a pane out of its tab's layout or popups, moving the
// focus elsewhere. It removes the tab when nothing is left and returns it.
func (s *Service) detachLocked(st *State, t *Tab, paneID string) *Tab {
	if t.Layout.Contains(paneID) {
		t.Layout, _ = layout.Remove(t.Layout, paneID)
	}
	t.Popups = slices.DeleteFunc(t.Popups, func(p Popup) bool { return p.Pane == paneID })
	if t.Zoomed == paneID {
		t.Zoomed = ""
	}
	if t.Focused == paneID {
		t.Focused = ""
		if len(t.Popups) > 0 {
			t.Focused = t.Popups[len(t.Popups)-1].Pane
		} else if panes := t.Layout.Panes(); len(panes) > 0 {
			t.Focused = panes[0]
		}
	}
	if t.Layout == nil && len(t.Popups) == 0 {
		closed := *t
		s.removeTabLocked(st, t.ID)
		return &closed
	}
	return nil
}

// Move puts a pane in another tab (possibly in another environment) without
// restarting its process.
func (s *Service) Move(ctx context.Context, req MoveRequest) (Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srcState, src, p, err := s.paneLocked(ctx, req.Pane)
	if err != nil {
		return Pane{}, err
	}
	if src.ID == req.TabID {
		return Pane{}, fmt.Errorf("%w: the pane is already in that tab (use swap)", ErrInvalid)
	}
	d := req.Direction
	if d == "" {
		d = layout.Right
	}
	if _, err := layout.ParseDirection(string(d)); err != nil {
		return Pane{}, err
	}

	// Resolve the destination before changing anything.
	var (
		dstState *State
		dst      *Tab
	)
	switch {
	case req.TabID != "":
		dstState, dst, err = s.tabLocked(ctx, req.TabID)
	case req.EnvironmentID != "":
		dstState, _, err = s.stateLocked(ctx, req.EnvironmentID)
	default:
		err = fmt.Errorf("%w: a destination tab or environment is required", ErrInvalid)
	}
	if err != nil {
		return Pane{}, err
	}
	target := req.Target
	if dst != nil {
		if target == "" {
			target = dst.Focused
		}
		if !dst.Layout.Contains(target) {
			if panes := dst.Layout.Panes(); len(panes) > 0 {
				target = panes[0]
			} else {
				target = ""
			}
		}
	}

	pane := *p
	destEnv := req.EnvironmentID
	if dst != nil {
		destEnv = dst.EnvironmentID
	}
	if destEnv != pane.EnvironmentID {
		if _, err := s.procs.Move(ctx, pane.ProcessID, destEnv); err != nil {
			return Pane{}, err
		}
	}

	// Take it out of the source tab. Tabs are addressed by ID from here on:
	// removing or adding a tab moves the others within their slice.
	srcID, dstID := src.ID, ""
	if dst != nil {
		dstID = dst.ID
	}
	srcClosed := s.detachLocked(srcState, src, pane.ID)
	srcState.Panes = slices.DeleteFunc(srcState.Panes, func(x Pane) bool { return x.ID == pane.ID })

	// Put it in the destination (making a tab when asked for a new one).
	pane.EnvironmentID = destEnv
	dst = findTab(dstState, dstID)
	if dst == nil {
		tab := Tab{
			ID: s.idSource("t"), EnvironmentID: destEnv, Name: pane.Name,
			Width: int(s.cfg.Size.Width), Height: int(s.cfg.Size.Height), CreatedAt: s.nowFn(),
		}
		if tab.Name == "" {
			tab.Name = fmt.Sprintf("tab %d", len(dstState.Tabs)+1)
		}
		dstState.Tabs = append(dstState.Tabs, tab)
		dst = &dstState.Tabs[len(dstState.Tabs)-1]
		s.events.Publish(EventTabCreated, tab)
	}
	if target == "" {
		dst.Layout = layout.Leaf(pane.ID)
	} else if dst.Layout, err = layout.Split(dst.Layout, target, d, 0, pane.ID); err != nil {
		return Pane{}, err
	}
	pane.TabID = dst.ID
	dst.Focused, dst.Zoomed = pane.ID, ""
	dstState.ActiveTab = dst.ID
	dstState.Panes = append(dstState.Panes, pane)

	for _, envID := range uniq(pane.EnvironmentID, src.EnvironmentID) {
		if err := s.saveLocked(ctx, envID); err != nil {
			return Pane{}, err
		}
	}
	if srcClosed != nil {
		s.events.Publish(EventTabClosed, *srcClosed)
	} else if t := findTab(srcState, srcID); t != nil {
		s.resizeTabLocked(srcState, t)
	}
	s.resizeTabLocked(dstState, dst)
	s.events.Publish(EventPaneMoved, pane)
	return s.describeLocked(ctx, dst, pane), nil
}

func findTab(st *State, id string) *Tab {
	for i := range st.Tabs {
		if st.Tabs[i].ID == id {
			return &st.Tabs[i]
		}
	}
	return nil
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

// --- terminal I/O ---

// session returns a pane's running terminal.
func (s *Service) session(ctx context.Context, paneID string) (terminal.Session, error) {
	s.mu.Lock()
	_, _, p, err := s.paneLocked(ctx, paneID)
	var processID string
	if p != nil {
		processID = p.ProcessID
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	sess, err := s.terms.Get(processID)
	if err != nil {
		return nil, fmt.Errorf("%w (see `hive ps logs %s` for its output)", ErrNotRunning, processID)
	}
	return sess, nil
}

// Input writes raw bytes to a pane.
func (s *Service) Input(ctx context.Context, paneID string, data []byte) error {
	sess, err := s.session(ctx, paneID)
	if err != nil {
		return err
	}
	_, err = sess.Write(data)
	return err
}

// SendText types text. With paste set and the program in bracketed-paste
// mode, it arrives as one paste (so newlines do not submit line by line).
func (s *Service) SendText(ctx context.Context, paneID, text string, paste bool) error {
	sess, err := s.session(ctx, paneID)
	if err != nil {
		return err
	}
	if paste {
		scr, err := sess.Snapshot(ctx)
		if err == nil && scr.Modes&vt.ModeBracketedPaste != 0 {
			text = "\x1b[200~" + text + "\x1b[201~"
		}
	}
	_, err = sess.Write([]byte(text))
	return err
}

// SendKeys types named keys (see EncodeKeys).
func (s *Service) SendKeys(ctx context.Context, paneID string, keys []string) error {
	sess, err := s.session(ctx, paneID)
	if err != nil {
		return err
	}
	scr, err := sess.Snapshot(ctx)
	if err != nil {
		return err
	}
	data, err := EncodeKeys(keys, scr.Modes)
	if err != nil {
		return err
	}
	_, err = sess.Write(data)
	return err
}

// Run types a command line and presses Enter.
func (s *Service) Run(ctx context.Context, paneID, command string) error {
	if command == "" {
		return fmt.Errorf("%w: command is required", ErrInvalid)
	}
	sess, err := s.session(ctx, paneID)
	if err != nil {
		return err
	}
	_, err = sess.Write([]byte(command + "\r"))
	return err
}

// Read returns a pane's text.
func (s *Service) Read(ctx context.Context, paneID string, req terminal.ReadRequest) ([]string, error) {
	sess, err := s.session(ctx, paneID)
	if err != nil {
		return nil, err
	}
	return sess.Read(ctx, req)
}

// WaitOutput waits up to timeout (0: until ctx ends) for a pane to print a
// line matching req.Pattern.
func (s *Service) WaitOutput(ctx context.Context, paneID string, req terminal.WaitRequest, timeout time.Duration) (string, error) {
	sess, err := s.session(ctx, paneID)
	if err != nil {
		return "", err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return sess.WaitOutput(ctx, req)
}

// ReapPopups closes every popup whose process is no longer running: the
// ones that ended while no daemon was watching, or whose exit event was
// missed.
func (s *Service) ReapPopups(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadAllLocked(ctx); err != nil {
		return
	}
	popups := map[string]bool{}
	for _, st := range s.states {
		for _, t := range st.Tabs {
			for _, pp := range t.Popups {
				popups[pp.Pane] = true
			}
		}
	}
	var done []string
	for _, st := range s.states {
		for _, p := range st.Panes {
			if !popups[p.ID] {
				continue
			}
			if proc, err := s.procs.Get(ctx, p.ProcessID); err != nil || !proc.Active() {
				done = append(done, p.ID)
			}
		}
	}
	for _, id := range done {
		if err := s.closeLocked(ctx, id, false); err != nil && !errors.Is(err, ErrPaneNotFound) {
			s.log.Warn("close finished popup", "pane", id, "err", err)
		}
	}
}

// ProcessExited closes popup panes whose process ended (a popup lives as
// long as its command). Other panes stay, showing that their process exited.
func (s *Service) ProcessExited(ctx context.Context, processID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadAllLocked(ctx); err != nil {
		return
	}
	for _, st := range s.states {
		for _, p := range st.Panes {
			if p.ProcessID != processID {
				continue
			}
			for _, t := range st.Tabs {
				if t.ID == p.TabID && slices.ContainsFunc(t.Popups, func(x Popup) bool { return x.Pane == p.ID }) {
					if err := s.closeLocked(ctx, p.ID, false); err != nil && !errors.Is(err, ErrPaneNotFound) {
						s.log.Warn("close finished popup", "pane", p.ID, "err", err)
					}
					return
				}
			}
			return
		}
	}
}
