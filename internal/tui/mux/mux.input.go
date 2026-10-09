package mux

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/vt"
)

// HandleEvent processes one terminal event: a key, the mouse, a paste, a
// resize, focus, or the terminal's background colour.
func (a *App) HandleEvent(ev uv.Event) {
	switch ev.(type) {
	case uv.KeyPressEvent, uv.PasteEvent, uv.MouseClickEvent, uv.MouseReleaseEvent, uv.MouseWheelEvent:
		a.lastInput = a.now()
	}
	switch ev := ev.(type) {
	case uv.WindowSizeEvent:
		a.resize(ev.Width, ev.Height)
	case uv.KeyPressEvent:
		a.urgent, a.dirty = true, true
		a.key(uv.Key(ev))
	case uv.PasteEvent:
		a.urgent, a.dirty = true, true
		a.paste(ev.Content)
	case uv.MouseClickEvent:
		a.urgent, a.dirty = true, true
		a.mouseDown(uv.Mouse(ev))
	case uv.MouseMotionEvent:
		a.mouseMove(uv.Mouse(ev))
	case uv.MouseReleaseEvent:
		a.urgent, a.dirty = true, true
		a.mouseUp(uv.Mouse(ev))
	case uv.MouseWheelEvent:
		a.urgent, a.dirty = true, true
		a.wheel(uv.Mouse(ev))
	case uv.FocusEvent:
		a.focusReport(true)
	case uv.BlurEvent:
		a.focusReport(false)
	case uv.BackgroundColorEvent:
		a.theme = a.opts.resolveTheme(ev.IsDark())
		a.dirty = true
	}
}

// resize follows the terminal's new size.
func (a *App) resize(w, h int) {
	first := !a.sized
	a.width, a.height, a.sized = max(w, 1), max(h, 1), true
	if first && w <= compositor.MobileWidth {
		a.ui.sidebar = false // a phone opens on the panes
	}
	a.dirty = true
	a.afterResize()
}

// afterResize follows a change of the pane area: the tab, the pane
// streams and copy mode take the new sizes.
func (a *App) afterResize() {
	a.syncViews()
	a.claimSize(true)
}

// key routes a key press: to the top overlay, the Overview, the sidebar
// menu, or by input mode.
func (a *App) key(k uv.Key) {
	switch {
	case a.ui.overlay != nil:
		a.ui.overlay.key(a, k)
		return
	case a.ui.mode == keymap.ModePrefix:
		a.ui.mode = keymap.ModeTerminal
		if act, ok := a.km.Lookup(keymap.ModePrefix, k); ok {
			a.do(act)
		}
		return
	}
	if _, ok := a.km.IsPrefix(k); ok && a.ui.mode != keymap.ModeCopy {
		a.ui.mode, a.ui.prefixKey = keymap.ModePrefix, k
		return
	}
	switch {
	case a.ui.overview != nil:
		a.ui.overview.key(a, k)
	case a.sidebarFull():
		a.sidebarKey(k)
	case a.ui.mode == keymap.ModeTerminal:
		if act, ok := a.km.Lookup(keymap.ModeTerminal, k); ok {
			a.do(act)
			return
		}
		a.typeKey(k)
	case a.ui.mode == keymap.ModeCopy:
		a.copyKey(k)
	default: // navigate, resize: sticky until left
		if act, ok := a.km.Lookup(a.ui.mode, k); ok {
			a.do(act)
		}
	}
}

// typeKey sends a key to the focused pane, encoded for its program.
func (a *App) typeKey(k uv.Key) {
	v := a.focusedView()
	if v == nil {
		return
	}
	a.send(v, encodeKey(k, v.modes()))
}

// send types data into a pane's agent. Typing makes this client the one
// whose size the tab takes.
func (a *App) send(v *view, data []byte) {
	if len(data) == 0 {
		return
	}
	a.claimSize(true)
	v.send(a.ctx, data)
}

// focusedView is the live view of the focused pane.
func (a *App) focusedView() *view {
	p := a.focusedPane()
	if p == nil {
		return nil
	}
	return a.views[p.ProcessID]
}

// paste delivers pasted text: to an input line if one is open, else to the
// focused pane, bracketed when its program asked for that.
func (a *App) paste(text string) {
	if a.ui.overlay != nil {
		a.ui.overlay.paste(a, text)
		return
	}
	if a.ui.overview != nil || a.ui.mode == keymap.ModeCopy {
		return
	}
	if v := a.focusedView(); v != nil {
		a.send(v, pasteBytes(text, v.modes()))
	}
}

// pasteBytes is what a terminal sends for a paste: wrapped in bracketed
// paste markers when the program enabled them, else with newlines as the
// Enter key sends them.
func pasteBytes(text string, modes vt.Modes) []byte {
	if modes&vt.ModeBracketedPaste != 0 {
		// A paste must not end the bracket early.
		text = strings.ReplaceAll(text, "\x1b[201~", "")
		return []byte("\x1b[200~" + text + "\x1b[201~")
	}
	text = strings.ReplaceAll(text, "\r\n", "\r")
	return []byte(strings.ReplaceAll(text, "\n", "\r"))
}

// focusReport tells the focused pane's program that the terminal gained or
// lost focus, when it asked to know.
func (a *App) focusReport(in bool) {
	v := a.focusedView()
	if v == nil || v.modes()&vt.ModeFocusEvents == 0 {
		return
	}
	if in {
		v.send(a.ctx, []byte("\x1b[I"))
	} else {
		v.send(a.ctx, []byte("\x1b[O"))
	}
}

// sidebarKey drives the full-screen sidebar of a narrow screen: move,
// choose, leave.
func (a *App) sidebarKey(k uv.Key) {
	rows := a.sidebarRows()
	sel := a.selectableRows(rows)
	switch keymap.Names(k)[len(keymap.Names(k))-1] {
	case "up", "k", "shift+tab":
		a.ui.sidebarAt = max(a.ui.sidebarAt-1, 0)
	case "down", "j", "tab":
		a.ui.sidebarAt = min(a.ui.sidebarAt+1, max(len(sel)-1, 0))
	case "enter", "space", "l", "right":
		if a.ui.sidebarAt < len(sel) {
			r := rows[sel[a.ui.sidebarAt]]
			a.ui.sidebar = false
			a.sidebarSelect(r.Kind, r.ID)
		}
	case "esc", "q", "h", "left":
		a.ui.sidebar = false
		a.afterResize()
	}
}

// sidebarSelect acts on a sidebar row: switch environment, or go to an
// agent's pane.
func (a *App) sidebarSelect(kind compositor.HitKind, id string) {
	switch kind {
	case compositor.HitEnvironment:
		a.switchEnv(id)
	case compositor.HitAgent:
		a.gotoProcess(id)
	case compositor.HitSidebarHeader:
		a.ui.collapsed[id] = !a.ui.collapsed[id]
	}
	a.afterResize()
}
