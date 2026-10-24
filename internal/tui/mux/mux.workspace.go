package mux

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// Reconnecting after the event stream ended (the daemon restarted, the
// connection dropped): the first retry is quick, as a restarted daemon is
// back within moments, then the pause doubles up to the maximum.
const (
	resubscribeFirst = 200 * time.Millisecond
	resubscribeMax   = 2 * time.Second
)

// snapshot is the workspace as last read from the daemon.
type snapshot struct {
	envs  []environment.Environment
	procs []process.Process // every agent, oldest first
	tabs  []pane.Tab        // every tab, grouped by environment
	panes []pane.Pane       // every pane, with its process
	// agents is every terminal's agent state, by process ID; nil from a
	// daemon without agent detection.
	agents map[string]agent.Agent
	// meta is the tokens and titles reported on panes and environments.
	meta map[metadata.Target]metadata.Entry
	// observed is who observes the daemon (observer credentials with
	// open connections, and the panes whose screens they watch).
	observed observedState
	// plans is every plan's summary, newest first; nil from a daemon
	// without tasks.
	plans []task.Summary
	// branches is every active branch Hive follows; nil from a daemon
	// without them.
	branches []branch.Branch
}

// observedState is observer.activity.
type observedState struct {
	Observers []struct {
		Name        string `json:"name"`
		Connections int    `json:"connections"`
	} `json:"observers"`
	Watched map[string]int `json:"watched"`
}

// loadObserved reads who observes; nothing from a daemon without observers.
func loadObserved(ctx context.Context, c client.Client) observedState {
	var o observedState
	_ = c.Call(ctx, "observer.activity", nil, &o)
	return o
}

type size struct{ w, h int }

// workspaceState is the snapshot and what the UI tracks about it.
type workspaceState struct {
	snap    *snapshot // nil until the first read
	err     error     // the last read failed (shown when there is no snapshot)
	loading bool
	stale   bool // something changed while loading: read again
	envID   string

	live     bool            // the event stream is open
	retry    time.Duration   // the next pause before subscribing again
	activity map[string]bool // tabs whose agents printed since they were last shown
	finals   map[string]bool // exited agents whose last screen was asked for
	claims   map[string]size // tab sizes this client asked for, by tab ID
	claiming map[string]bool // tab.resize calls in flight
}

func (s *snapshot) env(id string) *environment.Environment {
	for i := range s.envs {
		if s.envs[i].ID == id {
			return &s.envs[i]
		}
	}
	return nil
}

func (s *snapshot) tabsOf(envID string) []*pane.Tab {
	var out []*pane.Tab
	for i := range s.tabs {
		if s.tabs[i].EnvironmentID == envID {
			out = append(out, &s.tabs[i])
		}
	}
	return out
}

func (s *snapshot) tab(id string) *pane.Tab {
	for i := range s.tabs {
		if s.tabs[i].ID == id {
			return &s.tabs[i]
		}
	}
	return nil
}

// activeTab is the environment's active tab: the one every client shows.
func (s *snapshot) activeTab(envID string) *pane.Tab {
	tabs := s.tabsOf(envID)
	for _, t := range tabs {
		if t.Active {
			return t
		}
	}
	if len(tabs) > 0 {
		return tabs[0]
	}
	return nil
}

func (s *snapshot) pane(id string) *pane.Pane {
	for i := range s.panes {
		if s.panes[i].ID == id {
			return &s.panes[i]
		}
	}
	return nil
}

func (s *snapshot) paneOfProcess(procID string) *pane.Pane {
	for i := range s.panes {
		if s.panes[i].ProcessID == procID {
			return &s.panes[i]
		}
	}
	return nil
}

func (s *snapshot) process(id string) *process.Process {
	for i := range s.procs {
		if s.procs[i].ID == id {
			return &s.procs[i]
		}
	}
	return nil
}

// loadedMsg carries a fresh snapshot.
type loadedMsg struct {
	snap *snapshot
	err  error
	gen  int // the machine it was read from (App.gen)
}

// load reads the whole workspace: five calls, whatever its size.
func load(ctx context.Context, c client.Client, api client.Workspace) (*snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	var s snapshot
	var err error
	if s.envs, err = c.EnvironmentList(ctx); err != nil {
		return nil, err
	}
	if s.procs, err = c.ProcessList(ctx, ""); err != nil {
		return nil, err
	}
	if s.tabs, err = api.TabList(ctx, ""); err != nil {
		return nil, err
	}
	if s.panes, err = api.PaneList(ctx, "", ""); err != nil {
		return nil, err
	}
	s.agents = loadAgents(ctx, c)
	s.meta = loadMetadata(ctx, c)
	s.observed = loadObserved(ctx, c)
	s.plans = loadPlans(ctx, c)
	s.branches = loadBranches(ctx, c)
	slices.SortStableFunc(s.procs, func(a, b process.Process) int { return a.StartedAt.Compare(b.StartedAt) })
	return &s, nil
}

// refresh reads the workspace again. Requests while a read is running are
// coalesced into one more read.
func (a *App) refresh() {
	if a.ws.loading {
		a.ws.stale = true
		return
	}
	a.ws.loading = true
	c, api, gen := a.c, a.api, a.gen
	a.spawn(func(ctx context.Context) Msg {
		s, err := load(ctx, c, api)
		return loadedMsg{snap: s, err: err, gen: gen}
	})
}

func (a *App) loaded(m loadedMsg) {
	a.ws.loading = false
	if m.err != nil {
		if a.ws.err == nil && !errors.Is(m.err, context.Canceled) {
			a.toast(compositor.ToastError, "read the workspace: "+errText(m.err))
		}
		a.ws.err = m.err
	} else {
		a.ws.err = nil
		a.ws.snap = m.snap
		for id := range a.ws.activity {
			if m.snap.tab(id) == nil {
				delete(a.ws.activity, id) // the tab closed
			}
		}
		a.pickEnv()
		a.afterChange()
		a.focusAfterSwitch()
	}
	if a.ws.stale {
		a.ws.stale = false
		a.refresh()
	}
}

// afterChange brings everything that depends on the workspace up to date.
func (a *App) afterChange() {
	if c := a.ui.copy; c != nil {
		if _, shown := a.geometry()[c.paneID]; !shown {
			a.exitCopy() // its pane closed, moved, or went off screen
		}
	}
	if a.ui.overview != nil {
		a.ui.overview.sync(a)
	}
	if o, ok := a.ui.overlay.(*picker); ok {
		o.reload(a)
	}
	a.syncViews()
	a.loadFinals()
	a.claimSize(false)
	a.seen()
	a.dirty = true
}

// pickEnv keeps the current environment, or chooses one: the configured
// one, the one rooted at the working directory, else the first.
func (a *App) pickEnv() {
	s := a.ws.snap
	if s.env(a.ws.envID) != nil {
		return
	}
	a.ws.envID = ""
	if a.opts.Env != "" && s.env(a.opts.Env) != nil {
		a.ws.envID = a.opts.Env
		return
	}
	if id := envForDir(s.envs, a.opts.Cwd); id != "" {
		a.ws.envID = id
		return
	}
	for _, e := range s.envs {
		if len(s.tabsOf(e.ID)) > 0 {
			a.ws.envID = e.ID
			return
		}
	}
	if len(s.envs) > 0 {
		a.ws.envID = s.envs[0].ID
	}
}

// envForDir returns the environment whose root holds dir, the deepest one
// when several do.
func envForDir(envs []environment.Environment, dir string) string {
	if dir == "" {
		return ""
	}
	best, bestLen := "", -1
	for _, e := range envs {
		if e.Path == "" {
			continue
		}
		rel, err := filepath.Rel(e.Path, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if len(e.Path) > bestLen {
			best, bestLen = e.ID, len(e.Path)
		}
	}
	return best
}

// --- what is on screen ---

func (a *App) env() *environment.Environment {
	if a.ws.snap == nil {
		return nil
	}
	return a.ws.snap.env(a.ws.envID)
}

// tab is the tab on screen.
func (a *App) tab() *pane.Tab {
	if a.ws.snap == nil {
		return nil
	}
	return a.ws.snap.activeTab(a.ws.envID)
}

// focusedPane is the tab's focused pane.
func (a *App) focusedPane() *pane.Pane {
	t := a.tab()
	if t == nil || t.Focused == "" {
		return nil
	}
	return a.ws.snap.pane(t.Focused)
}

// paneArea is the size of the tab area on this screen.
func (a *App) paneArea() size {
	r := a.regions().Panes
	return size{r.Dx(), r.Dy()}
}

// geometry is every visible pane's area in the tab on screen.
func (a *App) geometry() map[string]layout.Rect {
	t := a.tab()
	if t == nil {
		return nil
	}
	ar := a.paneArea()
	return t.Geometry(ar.w, ar.h)
}

// paneOrder lists the tab's tiled panes in layout order.
func (a *App) paneOrder() []string {
	t := a.tab()
	if t == nil || t.Layout == nil {
		return nil
	}
	return t.Layout.Panes()
}

// noteOutput marks the tab of an agent that printed, unless it is on
// screen.
func (a *App) noteOutput(ev event.Event) {
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(ev.Data, &out) != nil {
		return
	}
	p := a.ws.snap.paneOfProcess(out.ID)
	if p == nil {
		return
	}
	if t := a.tab(); t != nil && t.ID == p.TabID && a.ui.overview == nil {
		return // on screen already
	}
	if !a.ws.activity[p.TabID] {
		a.ws.activity[p.TabID] = true
		a.dirty = true
	}
}

// seen clears the activity mark of the tab on screen.
func (a *App) seen() {
	if t := a.tab(); t != nil && a.ui.overview == nil && a.ws.activity[t.ID] {
		delete(a.ws.activity, t.ID)
	}
}

// envActivity reports whether a tab of envID has unseen output.
func (a *App) envActivity(envID string) bool {
	for _, t := range a.ws.snap.tabsOf(envID) {
		if a.ws.activity[t.ID] {
			return true
		}
	}
	return false
}

// --- sizes ---

// claimedMsg reports a finished tab.resize.
type claimedMsg struct {
	tab string
	err error
}

// claimSize makes the tab on screen take this client's size, as the last
// client to show or type into a tab does. With force it asks again even
// when it asked before (the user typed: they are using this screen now).
func (a *App) claimSize(force bool) {
	t := a.tab()
	if t == nil {
		return
	}
	want := a.paneArea()
	if want.w < 2 || want.h < 2 || (t.Width == want.w && t.Height == want.h) || a.ws.claiming[t.ID] {
		return
	}
	if !force && a.ws.claims[t.ID] == want {
		return // asked already; another client has it now
	}
	a.ws.claims[t.ID] = want
	a.ws.claiming[t.ID] = true
	id, api := t.ID, a.api
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		_, err := api.TabResize(ctx, id, want.w, want.h)
		return claimedMsg{tab: id, err: err}
	})
}

// --- events ---

// eventsMsg is news from the event stream: an event, the stream opening
// (neither set), or its end.
type eventsMsg struct {
	ev  *event.Event
	err error
	gen int
}

// eventTypes are the events the UI follows.
var eventTypes = []string{"tab.", "pane.", "layout.", "process.", "environment.", "agent.", "metadata.", "plugin.", "task.", "branch.", event.Lost}

// subscribe opens the event stream and reads it until it ends.
func (a *App) subscribe() {
	c, gen, conn := a.c, a.gen, a.connCtx
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		ctx, cancel := context.WithTimeout(conn, callTimeout)
		es, err := c.Events(ctx, eventTypes...)
		cancel()
		if err != nil {
			a.post(eventsMsg{err: err, gen: gen})
			return
		}
		defer es.Close()
		stop := context.AfterFunc(conn, func() { _ = es.Close() })
		defer stop()
		a.post(eventsMsg{gen: gen})
		for {
			ev, err := es.Next()
			if err != nil {
				a.post(eventsMsg{err: err, gen: gen})
				return
			}
			a.post(eventsMsg{ev: &ev, gen: gen})
		}
	}()
}

func (a *App) handleEvents(m eventsMsg) {
	switch {
	case m.err != nil:
		a.ws.live, a.dirty = false, true
		// Read the workspace now (whatever changed meanwhile) and try the
		// stream again shortly.
		a.refresh()
		a.ws.retry = min(max(a.ws.retry*2, resubscribeFirst), resubscribeMax)
		gen := a.gen
		a.after(a.ws.retry, func() {
			if gen == a.gen {
				a.subscribe()
			}
		})
	case m.ev == nil:
		a.ws.live, a.ws.retry, a.dirty = true, 0, true
		a.refresh() // changes made before the stream opened
		a.loadContributions()
	default:
		a.apply(*m.ev)
	}
}

// apply updates the snapshot from one event. Focus and layout changes,
// the common case while working, apply at once; anything else reads the
// workspace again.
func (a *App) apply(ev event.Event) {
	s := a.ws.snap
	if ev.Type == event.ProcessOutput {
		if s != nil {
			a.noteOutput(ev)
		}
		return // news, not a change to the workspace
	}
	if strings.HasPrefix(ev.Type, "plugin.") {
		a.applyPluginEvent(ev)
		return // plugins, not the workspace
	}
	if strings.HasPrefix(ev.Type, "task.") {
		if s != nil {
			a.applyTaskEvent(ev)
		}
		return // plans carry their own state: no read needed
	}
	if strings.HasPrefix(ev.Type, "branch.") {
		if s != nil {
			a.applyBranchEvent(ev)
		}
		return // so do branches
	}
	if s == nil {
		a.refresh()
		return
	}
	if a.ws.loading {
		// A read in flight began before this event and would undo it.
		a.ws.stale = true
	}
	switch ev.Type {
	case agent.EventState:
		a.applyAgentState(ev)
		return
	case metadata.EventChange:
		a.applyMetadata(ev)
		return
	case "observer.activity":
		var o observedState
		if json.Unmarshal(ev.Data, &o) == nil {
			s.observed, a.dirty = o, true
		}
		return
	case "pane.focused":
		var p pane.Pane
		if json.Unmarshal(ev.Data, &p) != nil {
			break
		}
		if t := s.tab(p.TabID); t != nil {
			t.Focused = p.ID
			if t.Zoomed != "" && t.Zoomed != p.ID {
				t.Zoomed = ""
			}
			for _, o := range s.tabsOf(t.EnvironmentID) {
				o.Active = o.ID == t.ID
			}
			a.afterChange()
			return
		}
	case "layout.updated":
		var nt pane.Tab
		if json.Unmarshal(ev.Data, &nt) != nil {
			break
		}
		if t := s.tab(nt.ID); t != nil {
			nt.Active = t.Active
			*t = nt
			a.afterChange()
			return
		}
	case event.EnvironmentGit:
		var g struct {
			ID  string      `json:"id"`
			Git *git.Status `json:"git"`
		}
		if json.Unmarshal(ev.Data, &g) == nil {
			if e := s.env(g.ID); e != nil {
				e.Git = g.Git
				a.dirty = true
				return
			}
		}
	}
	a.refresh()
}
