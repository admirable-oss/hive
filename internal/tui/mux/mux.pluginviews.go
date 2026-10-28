package mux

import (
	"context"
	"fmt"
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/vt"
)

// Pane views: a pane that shows a plugin's view draws the document the
// daemon holds, in cells. Clicking a button, or pressing enter on the
// selected one (tab moves the selection), asks the daemon to run the
// action the button names; a link opens only on a click.

// viewsMsg carries a fresh read of plugin.views.
type viewsMsg struct {
	views []plugin.ViewContent
	err   error
	gen   int
}

// viewButton is where a drawn button is, in its pane's cells.
type viewButton struct {
	y, x1, x2 int
	id, url   string
}

// loadViews reads the plugins' views, in the background.
func (a *App) loadViews() {
	c, gen := a.c, a.gen
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		got, err := client.NewPlugins(c).Views(ctx)
		return viewsMsg{views: got, err: err, gen: gen}
	})
}

func (a *App) viewsLoaded(m viewsMsg) {
	if m.gen != a.gen || m.err != nil {
		return // an older daemon has none; a failed read keeps what we had
	}
	a.plugins.views = map[string]plugin.ViewContent{}
	for _, v := range m.views {
		a.plugins.views[v.Plugin+"/"+v.View] = v
	}
	a.dirty = true
}

// drawViewPane fills a view pane's lines from its document, and notes
// where its buttons and links are for clicks.
func (a *App) drawViewPane(cp *compositor.Pane, p *pane.Pane, w, h int) {
	v, ok := a.plugins.views[p.View]
	if cp.Title == p.ID {
		cp.Title = p.View
		if ok && v.Title != "" {
			cp.Title = v.Title
		}
	}
	cp.Badges = append(cp.Badges, compositor.Span{Text: "[view]", Style: uv.Style{Fg: a.theme.Muted}})
	if a.plugins.buttons == nil {
		a.plugins.buttons = map[string][]viewButton{}
	}
	delete(a.plugins.buttons, p.ID)
	switch {
	case a.plugins.views == nil:
		cp.Placeholder = "loading…"
		return
	case !ok:
		cp.Placeholder = "no plugin view " + p.View + "\n\nhive plugin views lists them"
		return
	case !v.Available:
		cp.Dim = true
		cp.Placeholder = "the plugin cannot run (hive plugin doctor)"
		return
	case v.Doc == nil:
		cp.Placeholder = "nothing yet"
		return
	}
	lines := plugin.DrawView(*v.Doc, max(w-2, 1))
	selected := a.selectedButton(p.ID, lines)
	var buttons []viewButton
	cells := make([][]vt.Cell, 0, min(len(lines), h))
	for y, l := range lines {
		if y >= h {
			break
		}
		row := []vt.Cell{blankCell()}
		for _, s := range l {
			st := a.viewStyle(s.Style, s.Button != "" && s.Button == selected)
			x1 := len(row)
			gr := uniseg.NewGraphemes(s.Text)
			for gr.Next() {
				cw := gr.Width()
				row = append(row, vt.Cell{Content: gr.Str(), Width: uint8(cw), Style: st, Link: s.URL})
				for range cw - 1 {
					row = append(row, vt.Cell{Width: 0, Style: st})
				}
			}
			if s.Button != "" || s.URL != "" {
				buttons = append(buttons, viewButton{y: y, x1: x1, x2: len(row), id: s.Button, url: s.URL})
			}
		}
		cells = append(cells, row)
	}
	cp.Lines = cells
	a.plugins.buttons[p.ID] = buttons
}

func blankCell() vt.Cell { return vt.Cell{Content: " ", Width: 1} }

// selectedButton is the pane's selected button, while it is still drawn.
func (a *App) selectedButton(paneID string, lines []plugin.ViewLine) string {
	sel := a.plugins.selected[paneID]
	for _, l := range lines {
		for _, s := range l {
			if s.Button != "" && s.Button == sel {
				return sel
			}
		}
	}
	return ""
}

// viewStyle colours a span by its style.
func (a *App) viewStyle(style string, selected bool) vt.Style {
	var st vt.Style
	switch style {
	case "muted", "divider":
		st.Fg = vtColor(a.theme.Muted)
	case "bold":
		st.Attrs |= vt.AttrBold
	case "code":
		st.Fg = vtColor(a.theme.Info)
	case "ok", "success", "progress":
		st.Fg = vtColor(a.theme.Success)
	case "warn", "warning":
		st.Fg = vtColor(a.theme.Warning)
	case "error":
		st.Fg = vtColor(a.theme.Error)
	case "info":
		st.Fg = vtColor(a.theme.Info)
	case "button":
		st.Fg, st.Attrs = vtColor(a.theme.Accent), vt.AttrBold
	case "link":
		st.Fg, st.Underline = vtColor(a.theme.Info), 1 // single
	}
	if selected {
		st.Attrs |= vt.AttrReverse
	}
	return st
}

// vtColor converts a theme colour to a cell's.
func vtColor(c color.Color) vt.Color {
	switch c := c.(type) {
	case nil:
		return vt.DefaultColor
	case ansi.BasicColor:
		return vt.Indexed(uint8(c))
	case ansi.IndexedColor:
		return vt.Indexed(uint8(c))
	}
	r, g, b, _ := c.RGBA()
	return vt.RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// viewClick handles a click on a view pane: a button runs its action, a
// link opens.
func (a *App) viewClick(p *pane.Pane, pos uv.Position) {
	for _, b := range a.plugins.buttons[p.ID] {
		if pos.Y != b.y || pos.X < b.x1 || pos.X >= b.x2 {
			continue
		}
		if b.url != "" {
			a.open(b.url)
			return
		}
		a.selectButton(p.ID, b.id)
		a.pressButton(p, b.id)
		return
	}
}

func (a *App) selectButton(paneID, id string) {
	if a.plugins.selected == nil {
		a.plugins.selected = map[string]string{}
	}
	a.plugins.selected[paneID] = id
	a.dirty = true
}

// viewKey handles a key typed into a view pane: tab and shift+tab (or the
// arrows) move between its buttons, enter presses the selected one.
func (a *App) viewKey(p *pane.Pane, k uv.Key) {
	var ids []string
	for _, b := range a.plugins.buttons[p.ID] {
		if b.id != "" {
			ids = append(ids, b.id)
		}
	}
	if len(ids) == 0 {
		return
	}
	cur := -1
	for i, id := range ids {
		if id == a.plugins.selected[p.ID] {
			cur = i
		}
	}
	switch keyName(k) {
	case "tab", "down", "right", "j", "l":
		a.selectButton(p.ID, ids[(cur+1)%len(ids)])
	case "shift+tab", "up", "left", "k", "h":
		a.selectButton(p.ID, ids[(max(cur, 0)+len(ids)-1)%len(ids)])
	case "enter", "space":
		if cur >= 0 {
			a.pressButton(p, ids[cur])
		}
	}
}

// pressButton asks the daemon to run the button's action, and says how
// it went.
func (a *App) pressButton(p *pane.Pane, id string) {
	plug, view, _ := strings.Cut(p.View, "/")
	cl := a.c
	click := plugin.ViewClick{Plugin: plug, View: view, Node: id, Environment: p.EnvironmentID, Tab: p.TabID, Pane: p.ID}
	a.spawn(func(ctx context.Context) Msg {
		res, err := client.NewPlugins(cl).ClickView(ctx, click)
		if err == nil && res.ExitCode != 0 {
			err = fmt.Errorf("exited with status %d: %s", res.ExitCode, lastLine(res.Output))
		}
		return commandDoneMsg{title: plug + ": " + id, out: res.Output, err: err}
	})
}
