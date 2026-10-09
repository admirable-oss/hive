package mux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
)

// Resize steps: columns are narrower than rows are tall.
const (
	resizeCols = 2
	resizeRows = 1
)

// do performs a workspace action (any mode but copy, whose actions go to
// copy mode).
func (a *App) do(act keymap.Action) {
	if n, ok := act.TabNumber(); ok {
		a.selectTab(n - 1)
		return
	}
	if act.IsCopy() {
		return
	}
	switch act {
	case keymap.ExitMode:
		a.ui.mode = keymap.ModeTerminal
	case keymap.EnterNavigate:
		a.ui.mode = keymap.ModeNavigate
	case keymap.EnterResize:
		a.ui.mode = keymap.ModeResize
	case keymap.EnterCopy:
		if p := a.focusedPane(); p != nil {
			a.enterCopy(p.ID, true, 0)
		}
	case keymap.SendPrefix:
		a.typeKey(a.ui.prefixKey)
	case keymap.Help:
		a.openHelp()
	case keymap.Goto:
		a.openPicker()
	case keymap.Overview:
		a.toggleOverview()
	case keymap.ToggleSidebar:
		a.ui.sidebar = !a.ui.sidebar
		a.ui.sidebarAt = 0
		a.afterResize()
	case keymap.Detach:
		a.quit = true
	case keymap.Paste:
		if a.ui.clip == "" {
			a.toast(compositor.ToastInfo, "nothing copied yet")
		} else if v := a.focusedView(); v != nil {
			a.send(v, pasteBytes(a.ui.clip, v.modes()))
		}
	case keymap.EditScroll:
		a.editScrollback()
	case keymap.NewTab:
		a.newTab()
	case keymap.NextTab:
		a.cycleTab(1)
	case keymap.PrevTab:
		a.cycleTab(-1)
	case keymap.NextEnv:
		a.cycleEnv(1)
	case keymap.PrevEnv:
		a.cycleEnv(-1)
	case keymap.CloseTab:
		a.confirmCloseTab()
	case keymap.RenameTab:
		a.renameTab()
	default:
		a.paneAction(act)
	}
}

// paneAction performs an action on the focused pane.
func (a *App) paneAction(act keymap.Action) {
	p := a.focusedPane()
	if p == nil {
		return
	}
	id, api := p.ID, a.api
	switch act {
	case keymap.SplitRight, keymap.SplitDown:
		d := layout.Right
		if act == keymap.SplitDown {
			d = layout.Down
		}
		a.call("split", func(ctx context.Context) error {
			_, err := api.PaneSplit(ctx, pane.SplitRequest{Pane: id, Direction: d})
			return err
		}, nil)
	case keymap.ClosePane:
		a.confirmClosePane(p)
	case keymap.Zoom:
		a.call("zoom", func(ctx context.Context) error {
			_, err := api.PaneZoom(ctx, id, nil)
			return err
		}, nil)
	case keymap.FocusLeft, keymap.FocusRight, keymap.FocusUp, keymap.FocusDown:
		d := map[keymap.Action]layout.Direction{
			keymap.FocusLeft: layout.Left, keymap.FocusRight: layout.Right,
			keymap.FocusUp: layout.Up, keymap.FocusDown: layout.Down,
		}[act]
		a.call("focus", func(ctx context.Context) error {
			_, err := api.PaneFocus(ctx, id, d)
			if isCode(err, protocol.ErrorCodeNotFound) {
				return nil // nothing that way: stay
			}
			return err
		}, nil)
	case keymap.FocusNext, keymap.FocusPrev:
		if next := a.neighbourInOrder(id, act == keymap.FocusNext); next != "" {
			a.focusPane(next)
		}
	case keymap.SwapNext, keymap.SwapPrev:
		if next := a.neighbourInOrder(id, act == keymap.SwapNext); next != "" && next != id {
			a.call("swap", func(ctx context.Context) error { return api.PaneSwap(ctx, id, next) }, nil)
		}
	case keymap.ResizeLeft, keymap.ResizeRight, keymap.ResizeUp, keymap.ResizeDown:
		a.resizePane(id, act)
	case keymap.RenamePane:
		a.renamePane(p)
	case keymap.Popup:
		tabID := p.TabID
		a.call("popup", func(ctx context.Context) error {
			_, err := api.PanePopup(ctx, pane.PopupRequest{TabID: tabID})
			return err
		}, nil)
	}
}

func isCode(err error, code string) bool {
	var pe *protocol.Error
	return errors.As(err, &pe) && pe.Code == code
}

// resizePane moves the border on the action's side, like tmux: right
// grows the pane rightwards when it has a border there, else moves its
// left border right (shrinking it).
func (a *App) resizePane(id string, act keymap.Action) {
	type step struct {
		d     layout.Direction
		cells int
	}
	s := map[keymap.Action]step{
		keymap.ResizeLeft: {layout.Left, resizeCols}, keymap.ResizeRight: {layout.Right, resizeCols},
		keymap.ResizeUp: {layout.Up, resizeRows}, keymap.ResizeDown: {layout.Down, resizeRows},
	}[act]
	opposite := map[layout.Direction]layout.Direction{
		layout.Left: layout.Right, layout.Right: layout.Left, layout.Up: layout.Down, layout.Down: layout.Up,
	}
	api := a.api
	a.call("resize", func(ctx context.Context) error {
		_, err := api.PaneResize(ctx, id, s.d, s.cells)
		if err == nil || !isBorderErr(err) {
			return err
		}
		// No border on that side: move the other one the same way.
		_, err = api.PaneResize(ctx, id, opposite[s.d], -s.cells)
		if isBorderErr(err) {
			return nil // a single pane: nothing to resize
		}
		return err
	}, nil)
}

// isBorderErr reports the daemon's answer to resizing towards a side
// without a border.
func isBorderErr(err error) bool {
	return isCode(err, protocol.ErrorCodeInvalidParams) && strings.Contains(err.Error(), layout.ErrNoBorder.Error())
}

// neighbourInOrder is the next (or previous) pane in layout order,
// wrapping around.
func (a *App) neighbourInOrder(id string, next bool) string {
	order := a.paneOrder()
	i := slices.Index(order, id)
	if i < 0 || len(order) == 0 {
		return ""
	}
	if next {
		return order[(i+1)%len(order)]
	}
	return order[(i-1+len(order))%len(order)]
}

// focusPane focuses a pane (and its tab).
func (a *App) focusPane(id string) {
	api := a.api
	a.call("focus", func(ctx context.Context) error {
		_, err := api.PaneFocus(ctx, id, "")
		return err
	}, nil)
}

// --- tabs ---

func (a *App) envTabs() []*pane.Tab {
	if a.ws.snap == nil {
		return nil
	}
	return a.ws.snap.tabsOf(a.ws.envID)
}

// selectTab shows the environment's i-th tab (from 0).
func (a *App) selectTab(i int) {
	tabs := a.envTabs()
	if i < 0 || i >= len(tabs) {
		return
	}
	a.focusTab(tabs[i].ID)
}

func (a *App) focusTab(id string) {
	s := a.ws.snap
	if t := s.tab(id); t != nil {
		// Show it now; the daemon confirms with an event.
		for _, o := range s.tabsOf(t.EnvironmentID) {
			o.Active = o.ID == id
		}
		a.exitCopy()
		a.afterChange()
	}
	api := a.api
	a.call("switch tab", func(ctx context.Context) error {
		_, err := api.TabFocus(ctx, id)
		return err
	}, nil)
}

func (a *App) cycleTab(delta int) {
	tabs := a.envTabs()
	t := a.tab()
	if t == nil || len(tabs) < 2 {
		return
	}
	i := slices.IndexFunc(tabs, func(x *pane.Tab) bool { return x.ID == t.ID })
	a.selectTab(((i+delta)%len(tabs) + len(tabs)) % len(tabs))
}

// newTab opens a tab in the current environment, creating an environment
// for the working directory first when there is none.
func (a *App) newTab() {
	api, c := a.api, a.c
	if e := a.env(); e != nil {
		envID := e.ID
		a.call("new tab", func(ctx context.Context) error {
			_, err := api.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: envID})
			return err
		}, nil)
		return
	}
	dir := a.opts.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	var taken []string
	if a.ws.snap != nil {
		for _, e := range a.ws.snap.envs {
			taken = append(taken, e.ID)
		}
	}
	id := envName(filepath.Base(dir), taken)
	a.call("create environment", func(ctx context.Context) error {
		if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: id, Root: dir}); err != nil {
			return err
		}
		_, err := api.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: id})
		return err
	}, func(err error) {
		if err == nil {
			a.ws.envID = id
		}
	})
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// envName derives an environment ID from a directory name, unique among
// taken.
func envName(base string, taken []string) string {
	name := strings.Trim(unsafeName.ReplaceAllString(base, "-"), "-.")
	if name == "" {
		name = "workspace"
	}
	id := name
	for n := 2; slices.Contains(taken, id); n++ {
		id = fmt.Sprintf("%s-%d", name, n)
	}
	return id
}

// --- environments ---

func (a *App) cycleEnv(delta int) {
	s := a.ws.snap
	if s == nil || len(s.envs) < 2 {
		return
	}
	i := slices.IndexFunc(s.envs, func(e environment.Environment) bool { return e.ID == a.ws.envID })
	a.switchEnv(s.envs[((i+delta)%len(s.envs)+len(s.envs))%len(s.envs)].ID)
}

func (a *App) switchEnv(id string) {
	if a.ws.snap == nil || a.ws.snap.env(id) == nil || id == a.ws.envID {
		return
	}
	a.ws.envID = id
	a.exitCopy()
	a.afterChange()
	a.claimSize(true)
}

// gotoProcess shows an agent: its pane (switching environment and tab),
// or the Overview for agents without one.
func (a *App) gotoProcess(procID string) {
	s := a.ws.snap
	if s == nil {
		return
	}
	if p := s.paneOfProcess(procID); p != nil {
		a.gotoPane(p.ID)
		return
	}
	a.openOverview(procID)
}

func (a *App) gotoPane(id string) {
	s := a.ws.snap
	p := s.pane(id)
	if p == nil {
		return
	}
	a.ui.overview = nil
	if p.EnvironmentID != a.ws.envID {
		a.ws.envID = p.EnvironmentID
	}
	if t := s.tab(p.TabID); t != nil {
		for _, o := range s.tabsOf(t.EnvironmentID) {
			o.Active = o.ID == t.ID
		}
		t.Focused = p.ID
	}
	a.exitCopy()
	a.afterChange()
	a.focusPane(id)
}

// --- prompts and confirmations ---

func (a *App) renameTab() {
	t := a.tab()
	if t == nil {
		return
	}
	id, api := t.ID, a.api
	a.openPrompt("Rename tab", t.Name, func(name string) {
		if name = strings.TrimSpace(name); name == "" {
			return
		}
		a.call("rename tab", func(ctx context.Context) error {
			_, err := api.TabRename(ctx, id, name)
			return err
		}, nil)
	})
}

func (a *App) renamePane(p *pane.Pane) {
	id, api := p.ID, a.api
	current := p.Name
	if current == "" && p.Process != nil {
		current = commandName(p.Process)
	}
	a.openPrompt("Rename pane", current, func(name string) {
		if name = strings.TrimSpace(name); name == "" {
			return
		}
		a.call("rename pane", func(ctx context.Context) error {
			_, err := api.PaneRename(ctx, id, name)
			return err
		}, nil)
	})
}

func (a *App) confirmClosePane(p *pane.Pane) {
	id, api := p.ID, a.api
	what := a.paneTitle(p, nil)
	question := "Close " + what + "?"
	if p.Process != nil && p.Process.Active() {
		question = "Close " + what + "? Its process will be stopped."
	}
	a.openConfirm("Close pane", question, func() {
		a.call("close pane", func(ctx context.Context) error { return api.PaneClose(ctx, id) }, nil)
	})
}

func (a *App) confirmCloseTab() {
	t := a.tab()
	if t == nil {
		return
	}
	id, api := t.ID, a.api
	n := 0
	for _, p := range a.ws.snap.panes {
		if p.TabID == id {
			n++
		}
	}
	question := fmt.Sprintf("Close tab %q and stop its %d pane(s)?", t.Name, n)
	a.openConfirm("Close tab", question, func() {
		a.call("close tab", func(ctx context.Context) error { return api.TabClose(ctx, id) }, nil)
	})
}
