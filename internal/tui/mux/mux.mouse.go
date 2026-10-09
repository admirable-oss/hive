package mux

import (
	"context"
	"regexp"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/vt"
)

// Mouse tuning.
const (
	doubleClick    = 400 * time.Millisecond
	wheelLines     = 3
	minSidebarW    = 16
	minPaneColumns = 20 // the sidebar never squeezes the panes below this
)

// dragKind is what a held mouse button is doing.
type dragKind uint8

const (
	dragNone    dragKind = iota
	dragForward          // the pane's program tracks the mouse: forward it
	dragSelect           // selecting text
	dragSidebar          // moving the sidebar's edge
	dragBorder           // moving a border between panes
)

type mouseState struct {
	drag   dragKind
	paneID string
	origin uv.Position // pane cell where the drag started
	moved  bool

	// dragBorder: the pane whose side moves, which side, and the screen
	// coordinate of the border when the drag started.
	border    layout.Direction
	borderAt  int
	sent      int // cells already requested
	resizing  bool
	queued    int // cells to request once the call in flight returns
	lastClick time.Time
	clickAt   uv.Position
	clicks    int
}

// paneAt returns the pane under screen cell (x, y) and the cell's position
// inside it.
func (a *App) paneAt(x, y int) (string, uv.Position, bool) {
	h, ok := a.last.HitAt(x, y)
	if !ok || h.Kind != compositor.HitPane {
		return "", uv.Position{}, false
	}
	r, ok := a.geometry()[h.ID]
	if !ok {
		return h.ID, uv.Position{}, true
	}
	area := a.regions().Panes
	return h.ID, uv.Pos(x-area.Min.X-r.X, y-area.Min.Y-r.Y), true
}

func (a *App) mouseDown(m uv.Mouse) {
	h, ok := a.last.HitAt(m.X, m.Y)
	if a.ui.overlay != nil || a.ui.overview != nil {
		if ok && h.Kind == compositor.HitOverlayLine {
			if a.ui.overlay != nil {
				a.ui.overlay.click(a, h.Index)
			} else {
				a.ui.overview.click(a, h.Index)
			}
		} else if !ok || h.Kind != compositor.HitPane {
			// A click outside the box closes it.
			if a.ui.overlay != nil {
				a.closeOverlay()
			}
		}
		return
	}
	if !ok {
		a.borderDown(m)
		return
	}
	switch h.Kind {
	case compositor.HitTab:
		a.focusTab(h.ID)
	case compositor.HitNewTab:
		a.newTab()
	case compositor.HitEnvironment, compositor.HitAgent, compositor.HitSidebarHeader:
		if a.sidebarFull() {
			a.ui.sidebar = false
		}
		a.sidebarSelect(h.Kind, h.ID)
	case compositor.HitSidebarBorder:
		a.ui.mouse.drag = dragSidebar
	case compositor.HitPaneTitle:
		a.titleDown(h.ID, m)
	case compositor.HitPane:
		a.paneDown(h.ID, m)
	}
}

// titleDown focuses a pane from its title; the title line of a pane below
// another is also their border, which drags.
func (a *App) titleDown(id string, m uv.Mouse) {
	if t := a.tab(); t != nil && t.Focused != id {
		a.focusPane(id)
	}
	if r, ok := a.geometry()[id]; ok && r.Y > 0 && !a.isPopup(id) {
		a.ui.mouse = mouseState{drag: dragBorder, paneID: id, border: layout.Up, borderAt: m.Y, lastClick: a.ui.mouse.lastClick}
	}
}

// borderDown starts dragging the vertical border at (x, y), if there is
// one: the column right of a pane.
func (a *App) borderDown(m uv.Mouse) {
	area := a.regions().Panes
	if !uv.Pos(m.X, m.Y).In(area) {
		return
	}
	x, y := m.X-area.Min.X, m.Y-area.Min.Y
	for id, r := range a.geometry() {
		if a.isPopup(id) {
			continue
		}
		if r.X+r.W == x && y >= r.Y-1 && y < r.Y+r.H {
			a.ui.mouse = mouseState{drag: dragBorder, paneID: id, border: layout.Right, borderAt: m.X}
			return
		}
	}
}

func (a *App) isPopup(id string) bool {
	if t := a.tab(); t != nil {
		for _, p := range t.Popups {
			if p.Pane == id {
				return true
			}
		}
	}
	return false
}

func (a *App) paneDown(id string, m uv.Mouse) {
	_, pos, _ := a.paneAt(m.X, m.Y)
	t := a.tab()
	if t != nil && t.Focused != id {
		a.focusPane(id)
		t.Focused = id // type into it at once; the daemon confirms
	}
	if c := a.ui.copy; c != nil && c.paneID != id {
		a.exitCopy()
	}

	p := a.ws.snap.pane(id)
	v := (*view)(nil)
	if p != nil {
		v = a.views[p.ProcessID]
	}
	// The program asked for the mouse, and shift does not override it.
	if v != nil && mouseTracking(v.modes()) && !m.Mod.Contains(uv.ModShift) && a.ui.copy == nil {
		a.ui.mouse = mouseState{drag: dragForward, paneID: id}
		a.send(v, encodeMouse(mousePress, m, pos.X, pos.Y, v.modes()))
		return
	}
	if m.Button != uv.MouseLeft {
		return
	}
	if m.Mod.Contains(uv.ModCtrl) || m.Mod.Contains(uv.ModSuper) {
		if url := a.urlAt(id, pos); url != "" {
			a.openURL(url)
		}
		return
	}

	ms := &a.ui.mouse
	now := a.now()
	if now.Sub(ms.lastClick) < doubleClick && ms.clickAt == uv.Pos(m.X, m.Y) {
		ms.clicks++
	} else {
		ms.clicks = 1
	}
	ms.lastClick, ms.clickAt = now, uv.Pos(m.X, m.Y)
	ms.drag, ms.paneID, ms.origin, ms.moved = dragSelect, id, pos, false

	if ms.clicks >= 2 {
		// Double click: copy the word.
		if a.ui.copy == nil {
			a.enterCopy(id, false, 0)
			if a.ui.copy == nil {
				return
			}
			a.ui.copy.mouse = true
		}
		if c := a.ui.copy; c != nil && c.c != nil {
			c.c.SelectWord(pos.X, pos.Y)
			if text := c.c.SelectionText(); strings.TrimSpace(text) != "" {
				a.copyText(text)
			}
			if c.mouse {
				a.exitCopy()
			}
		}
		ms.drag = dragNone
		return
	}
	if c := a.ui.copy; c != nil && c.c != nil {
		c.c.MoveTo(pos.X, pos.Y)
	}
}

func (a *App) mouseMove(m uv.Mouse) {
	ms := &a.ui.mouse
	switch ms.drag {
	case dragNone:
		// Hover: only programs that asked for every movement see it.
		if id, pos, ok := a.paneAt(m.X, m.Y); ok {
			if p := a.ws.snap.pane(id); p != nil {
				if v := a.views[p.ProcessID]; v != nil && v.modes()&vt.ModeMouseAny != 0 {
					v.send(encodeMouse(mouseMotion, m, pos.X, pos.Y, v.modes()))
				}
			}
		}
	case dragForward:
		if v := a.paneView(ms.paneID); v != nil {
			x, y := a.panePosClamped(ms.paneID, m.X, m.Y)
			v.send(encodeMouse(mouseMotion, m, x, y, v.modes()))
		}
	case dragSidebar:
		w := min(max(m.X, minSidebarW), a.width-minPaneColumns)
		if w != a.ui.sidebarW && w >= minSidebarW {
			a.ui.sidebarW = w
			a.dirty = true
			a.syncViews()
		}
	case dragBorder:
		a.dragBorderTo(m)
	case dragSelect:
		x, y := a.panePos(ms.paneID, m.X, m.Y)
		if !ms.moved && uv.Pos(x, y) == ms.origin {
			return
		}
		a.dirty = true
		if !ms.moved {
			ms.moved = true
			if a.ui.copy == nil {
				a.enterCopy(ms.paneID, false, 0)
				if a.ui.copy == nil {
					ms.drag = dragNone
					return
				}
				a.ui.copy.mouse = true
			}
			if c := a.ui.copy; c != nil && c.c != nil {
				c.c.SelectFrom(ms.origin.X, ms.origin.Y)
			}
		}
		if c := a.ui.copy; c != nil && c.c != nil {
			r := a.geometry()[ms.paneID]
			// Dragging past the top or bottom scrolls.
			switch {
			case y < 0:
				c.c.Scroll(y)
				y = 0
			case y >= r.H && r.H > 0:
				c.c.Scroll(y - r.H + 1)
				y = r.H - 1
			}
			c.c.MoveTo(max(x, 0), y)
		}
	}
}

func (a *App) mouseUp(m uv.Mouse) {
	ms := &a.ui.mouse
	switch ms.drag {
	case dragForward:
		if v := a.paneView(ms.paneID); v != nil {
			x, y := a.panePosClamped(ms.paneID, m.X, m.Y)
			a.send(v, encodeMouse(mouseRelease, m, x, y, v.modes()))
		}
	case dragSidebar:
		a.afterResize()
	case dragBorder:
		a.dragBorderTo(m)
	case dragSelect:
		if c := a.ui.copy; ms.moved && c != nil && c.c != nil {
			if text := c.c.SelectionText(); text != "" {
				a.copyText(text)
			}
			if c.mouse {
				a.exitCopy()
			}
		}
	}
	ms.drag = dragNone
}

// dragBorderTo moves the dragged border to the mouse, one resize call at
// a time.
func (a *App) dragBorderTo(m uv.Mouse) {
	ms := &a.ui.mouse
	at := m.X
	if ms.border == layout.Up {
		at = m.Y
	}
	delta := at - ms.borderAt
	if ms.border == layout.Up {
		delta = -delta // dragging the top border up grows the pane
	}
	ms.queued = delta - ms.sent
	a.flushBorder()
}

func (a *App) flushBorder() {
	ms := &a.ui.mouse
	if ms.resizing || ms.queued == 0 {
		return
	}
	cells := ms.queued
	ms.sent += cells
	ms.queued = 0
	ms.resizing = true
	id, d, api := ms.paneID, ms.border, a.api
	a.call("resize", func(ctx context.Context) error {
		_, err := api.PaneResize(ctx, id, d, cells)
		if isBorderErr(err) {
			return nil
		}
		return err
	}, func(error) {
		a.ui.mouse.resizing = false
		a.flushBorder()
	})
}

func (a *App) wheel(m uv.Mouse) {
	if a.ui.overlay != nil || a.ui.overview != nil {
		k := uv.Key{Code: uv.KeyDown}
		if m.Button == uv.MouseWheelUp {
			k.Code = uv.KeyUp
		}
		a.key(k)
		return
	}
	id, pos, ok := a.paneAt(m.X, m.Y)
	if !ok {
		return
	}
	up := m.Button == uv.MouseWheelUp
	if c := a.ui.copy; c != nil && c.paneID == id {
		if c.c == nil {
			if up {
				c.scroll -= wheelLines
			}
			return
		}
		if !up && c.c.AtBottom() {
			a.exitCopy() // scrolled back to the live screen
			return
		}
		n := wheelLines
		if up {
			n = -n
		}
		c.c.Scroll(n)
		return
	}
	v := a.paneView(id)
	if v == nil {
		return
	}
	modes := v.modes()
	switch {
	case mouseTracking(modes):
		v.send(encodeMouse(mouseWheel, m, pos.X, pos.Y, modes))
	case modes&vt.ModeAltScreen != 0:
		// Full-screen programs without the mouse get arrow keys, as
		// terminals do in the alternate screen.
		k := uv.Key{Code: uv.KeyDown}
		if up {
			k.Code = uv.KeyUp
		}
		seq := encodeKey(k, modes)
		v.send([]byte(strings.Repeat(string(seq), wheelLines)))
	case up:
		a.enterCopy(id, true, -wheelLines)
	}
}

func (a *App) paneView(id string) *view {
	if p := a.ws.snap.pane(id); p != nil {
		return a.views[p.ProcessID]
	}
	return nil
}

// panePos converts a screen cell to a pane's cell, even outside the pane
// (a drag that left it).
func (a *App) panePos(id string, x, y int) (int, int) {
	r := a.geometry()[id]
	area := a.regions().Panes
	return x - area.Min.X - r.X, y - area.Min.Y - r.Y
}

// panePosClamped is panePos kept inside the pane, for reports to its
// program: a drag that leaves the pane reports its edge, as terminals do.
func (a *App) panePosClamped(id string, x, y int) (int, int) {
	r := a.geometry()[id]
	x, y = a.panePos(id, x, y)
	return min(max(x, 0), max(r.W-1, 0)), min(max(y, 0), max(r.H-1, 0))
}

// --- links ---

var urlRe = regexp.MustCompile(`(?:https?|file|ftp)://[^\s<>"'` + "`" + `]+`)

// urlAt returns the URL under a pane cell, from the screen's text.
func (a *App) urlAt(paneID string, pos uv.Position) string {
	p := a.ws.snap.pane(paneID)
	if p == nil {
		return ""
	}
	unlock := a.lockViews()
	defer unlock()
	scr, _ := a.screenOf(p.ProcessID)
	if scr == nil || pos.Y < 0 || pos.Y >= len(scr.Lines) {
		return ""
	}
	return urlInLine(scr.Lines[pos.Y], pos.X)
}

// urlInLine finds the URL covering column x of a line.
func urlInLine(line []vt.Cell, x int) string {
	var b strings.Builder
	var cols []int // byte offset → column
	for col, c := range line {
		if c.Width == 0 {
			continue
		}
		s := c.Content
		if s == "" {
			s = " "
		}
		for range len(s) {
			cols = append(cols, col)
		}
		b.WriteString(s)
	}
	text := b.String()
	for _, m := range urlRe.FindAllStringIndex(text, -1) {
		u := trimURL(text[m[0]:m[1]])
		end := m[0] + len(u)
		if end > m[0] && cols[m[0]] <= x && x <= cols[end-1] {
			return u
		}
	}
	return ""
}

// trimURL drops punctuation that ends a sentence rather than the link,
// and a closing bracket without its opening one.
func trimURL(u string) string {
	for len(u) > 0 {
		last := u[len(u)-1]
		switch {
		case strings.IndexByte(".,;:!?'\"", last) >= 0:
			u = u[:len(u)-1]
		case last == ')' && strings.Count(u, "(") < strings.Count(u, ")"),
			last == ']' && strings.Count(u, "[") < strings.Count(u, "]"):
			u = u[:len(u)-1]
		default:
			return u
		}
	}
	return u
}
