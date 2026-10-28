package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/fuzzy"
)

// Plugins add to the UI only through data the daemon holds: declared
// contributions and the cards and indicators plugins set. The UI never
// runs plugin code to draw; it runs a plugin's action only when the user
// picks it.

// pluginsState is what plugins contribute, as the daemon last said.
type pluginsState struct {
	contrib plugin.Contributions
	// failed remembers the failures already announced, so each is toasted
	// once.
	failed map[string]string
}

// contributionsMsg carries a fresh read of plugin.contributions.
type contributionsMsg struct {
	contrib plugin.Contributions
	err     error
	gen     int
}

// loadContributions reads what plugins contribute, in the background.
func (a *App) loadContributions() {
	c, gen := a.c, a.gen
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		got, err := client.NewPlugins(c).Contributions(ctx)
		return contributionsMsg{contrib: got, err: err, gen: gen}
	})
}

func (a *App) contributionsLoaded(m contributionsMsg) {
	if m.gen != a.gen || m.err != nil {
		return // an older daemon has none; a failed read keeps what we had
	}
	a.plugins.contrib = m.contrib
	a.dirty = true
}

// applyPluginEvent follows plugin.* events: content and health changes
// reload the contributions; a permission request and a newly failed
// plugin are announced.
func (a *App) applyPluginEvent(ev event.Event) {
	switch ev.Type {
	case plugin.EventPermission:
		var r plugin.PermissionRequest
		if json.Unmarshal(ev.Data, &r) == nil {
			text := fmt.Sprintf("plugin %s asks for %s", r.Plugin, r.Capability)
			if r.Reason != "" {
				text += " (" + r.Reason + ")"
			}
			a.toast(compositor.ToastWarning, text+": hive plugin grant "+r.Plugin+" "+r.Capability)
		}
		return
	case plugin.EventChanged:
		var st plugin.Status
		if json.Unmarshal(ev.Data, &st) == nil {
			if a.plugins.failed == nil {
				a.plugins.failed = map[string]string{}
			}
			if st.Health.State == plugin.StateFailed && a.plugins.failed[st.ID] != st.LastError {
				a.plugins.failed[st.ID] = st.LastError
				a.toast(compositor.ToastError, "plugin "+st.ID+" failed: "+st.LastError)
			} else if st.Health.State != plugin.StateFailed {
				delete(a.plugins.failed, st.ID)
			}
		}
	}
	a.loadContributions()
}

// --- sidebar cards ---

const panelPlugins = "plugins"

// toneColor colours a row or indicator by its tone.
func (a *App) toneColor(tone string) color.Color {
	switch tone {
	case "success":
		return a.theme.Success
	case "warning":
		return a.theme.Warning
	case "error":
		return a.theme.Error
	case "info":
		return a.theme.Info
	}
	return nil
}

// visibleCards are the cards about where the user is: the current
// environment's, and the focused agent's.
func (a *App) visibleCards() []plugin.CardContent {
	focused := ""
	if p := a.focusedPane(); p != nil {
		focused = p.ID
	}
	var out []plugin.CardContent
	for _, c := range a.plugins.contrib.Cards {
		if (c.Environment != "" && c.Environment == a.ws.envID) || (c.Pane != "" && c.Pane == focused) {
			out = append(out, c)
		}
	}
	return out
}

// pluginRows are the sidebar's plugin cards: a header per card, then its
// rows and links. They stay short: labels and texts are bounded by the
// daemon, and the sidebar clips what its width cannot hold.
func (a *App) pluginRows(header func(id, title string, n int) compositor.SidebarRow) []compositor.SidebarRow {
	cards := a.visibleCards()
	if len(cards) == 0 {
		return nil
	}
	rows := []compositor.SidebarRow{{}, header(panelPlugins, "Plugins", len(cards))}
	if a.ui.collapsed[panelPlugins] {
		return rows
	}
	muted := uv.Style{Fg: a.theme.Muted}
	for _, c := range cards {
		rows = append(rows, compositor.SidebarRow{Spans: []compositor.Span{{Text: "▪ " + c.Title, Style: uv.Style{Attrs: uv.AttrBold}}}})
		for _, r := range c.Rows {
			spans := []compositor.Span{{Text: "  "}}
			if r.Label != "" {
				spans = append(spans, compositor.Span{Text: r.Label + " ", Style: muted})
			}
			spans = append(spans, compositor.Span{Text: r.Text, Style: uv.Style{Fg: a.toneColor(r.Tone)}})
			rows = append(rows, compositor.SidebarRow{Spans: spans})
		}
		for _, l := range c.Links {
			rows = append(rows, compositor.SidebarRow{Spans: []compositor.Span{{Text: "  ↗ " + l.Title, Style: uv.Style{Fg: a.theme.Info}}}})
		}
	}
	return rows
}

// --- tab bar indicators ---

// Narrow screens drop plugin indicators first: the tabs and the mode
// indicator matter more.
const (
	indicatorsFrom    = 100 // columns needed to show any
	indicatorsAllFrom = 160 // columns needed to show more than two
)

func (a *App) pluginStatusSpans() []compositor.Span {
	if a.width < indicatorsFrom {
		return nil
	}
	limit := len(a.plugins.contrib.Status)
	if a.width < indicatorsAllFrom {
		limit = min(limit, 2)
	}
	var out []compositor.Span
	for _, s := range a.plugins.contrib.Status[:limit] {
		style := uv.Style{Fg: a.toneColor(s.Tone)}
		if style.Fg == nil {
			style.Fg = a.theme.Muted
		}
		out = append(out, compositor.Span{Text: s.Text + "  ", Style: style})
	}
	return out
}

// --- the palette ---

// paletteItem is one thing the palette offers.
type paletteItem struct {
	plugin, kind, title, detail string
	action, url                 string
}

type palette struct {
	in       input
	items    []paletteItem
	shown    []fuzzy.Result
	selected int
}

// contexts are where the user is, as contextual actions name them.
func (a *App) contexts() []string {
	out := []string{"session"}
	if a.ws.envID != "" {
		out = append(out, "environment")
	}
	if p := a.focusedPane(); p != nil {
		out = append(out, "pane")
		if s := a.ws.snap; s != nil {
			if ag, ok := s.agents[p.ProcessID]; ok && ag.Kind != "" {
				out = append(out, "agent")
			}
		}
	}
	return out
}

// paletteItems are the available, not hidden contributions that apply
// here.
func (a *App) paletteItems() []paletteItem {
	here := a.contexts()
	names := map[string]string{}
	var items []paletteItem
	seen := map[string]bool{}
	for _, d := range a.plugins.contrib.Declared {
		if d.Hidden || !d.Available {
			continue
		}
		names[d.Plugin] = d.Plugin
		it := paletteItem{plugin: d.Plugin, kind: d.Kind, title: d.Title, action: d.Action, url: d.URL}
		switch d.Kind {
		case plugin.ContribAction:
			if !slices.ContainsFunc(d.Contexts, func(c string) bool { return slices.Contains(here, c) }) {
				continue
			}
			it.detail = "action · " + strings.Join(d.Contexts, ", ")
		case plugin.ContribPalette:
			it.detail = "action"
		case plugin.ContribLink:
			it.detail = "link · " + d.URL
		case plugin.ContribConfig:
			it.detail = "settings"
		default:
			continue
		}
		key := d.Plugin + "\x00" + it.action + it.url + it.kind
		if (it.kind == plugin.ContribAction || it.kind == plugin.ContribPalette) && seen[d.Plugin+"\x00"+it.action] {
			continue // offered both in context and in the palette: once
		}
		seen[key], seen[d.Plugin+"\x00"+it.action] = true, it.action != ""
		items = append(items, it)
	}
	for _, c := range a.visibleCards() {
		for _, l := range c.Links {
			items = append(items, paletteItem{plugin: c.Plugin, kind: plugin.ContribLink, title: l.Title, url: l.URL, detail: "link · " + c.Title})
		}
	}
	return items
}

func (a *App) openPalette() {
	// The palette always offers the Plugins view: what is installed, who
	// stands behind it, and what confines it.
	items := append([]paletteItem{{plugin: "hive", kind: paletteKindPlugins, title: "Plugins…", detail: "installed plugins, their tier, sandbox and revocations"}}, a.paletteItems()...)
	p := &palette{items: items}
	p.rank()
	a.ui.overlay = p
}

func (p *palette) label(it paletteItem) string { return it.plugin + ": " + it.title }

func (p *palette) rank() {
	labels := make([]string, len(p.items))
	for i, it := range p.items {
		labels[i] = p.label(it)
	}
	p.shown = fuzzy.Rank(p.in.String(), labels)
	p.selected = min(p.selected, max(len(p.shown)-1, 0))
}

func (p *palette) key(a *App, k uv.Key) {
	switch keyName(k) {
	case "esc", "ctrl+c":
		a.closeOverlay()
	case "enter":
		p.choose(a)
	case "up", "ctrl+p", "ctrl+k":
		p.selected = max(p.selected-1, 0)
	case "down", "ctrl+n", "ctrl+j":
		p.selected = min(p.selected+1, max(len(p.shown)-1, 0))
	default:
		if p.in.edit(k) {
			p.selected = 0
			p.rank()
		}
	}
}

func (p *palette) paste(_ *App, text string) { p.in.insert(text); p.selected = 0; p.rank() }

func (p *palette) click(a *App, line int) {
	if line >= 0 && line < len(p.shown) {
		p.selected = line
		p.choose(a)
	}
}

func (p *palette) choose(a *App) {
	if p.selected >= len(p.shown) {
		return
	}
	it := p.items[p.shown[p.selected].Index]
	a.closeOverlay()
	switch it.kind {
	case plugin.ContribLink:
		if plugin.SafeURL(it.url) {
			a.open(it.url)
		}
	case plugin.ContribConfig:
		a.openPluginConfig(it.plugin)
	case paletteKindPlugins:
		a.openPluginList()
	default:
		a.runPluginAction(it.plugin, it.action, it.title)
	}
}

// runPluginAction runs a plugin's action where the user is.
func (a *App) runPluginAction(id, action, title string) {
	req := client.PluginRun{Plugin: id, Action: action, Environment: a.ws.envID}
	if p := a.focusedPane(); p != nil {
		req.Pane = p.ID
	}
	if t := a.tab(); t != nil {
		req.Tab = t.ID
	}
	cl := a.c
	a.spawn(func(ctx context.Context) Msg {
		res, err := client.NewPlugins(cl).Run(ctx, req)
		switch {
		case err == nil && res.Async:
			return commandDoneMsg{title: title, out: "started " + res.Run}
		case err == nil && res.ExitCode != 0:
			err = fmt.Errorf("exited with status %d: %s", res.ExitCode, lastLine(res.Output))
		}
		return commandDoneMsg{title: title, out: res.Output, err: err}
	})
}

func (p *palette) view(a *App) compositor.Overlay {
	muted := uv.Style{Fg: a.theme.Muted}
	match := uv.Style{Fg: a.theme.Accent, Attrs: uv.AttrBold}
	var lines []compositor.Spans
	for _, r := range p.shown {
		it := p.items[r.Index]
		line := highlightMatches(p.label(it), r.Positions, match)
		line = append(line, compositor.Span{Text: "  " + it.detail, Style: muted})
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, compositor.Spans{{Text: "nothing matches", Style: muted}})
	}
	return compositor.Overlay{
		Title: "Plugins", Width: 76, Input: p.in.view("› "), Lines: lines, Selected: p.selected,
		Footer: []compositor.Span{{Text: "enter run · esc close", Style: muted}},
	}
}

// --- the settings form ---

type configForm struct {
	cfg      plugin.ConfigView
	selected int
}

// configMsg carries a plugin's settings for the form, or the result of
// changing one.
type configMsg struct {
	view  plugin.ConfigView
	err   error
	saved string // the setting just saved
}

func (a *App) openPluginConfig(id string) {
	cl := a.c
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		v, err := client.NewPlugins(cl).Config(ctx, id)
		return configMsg{view: v, err: err}
	})
}

func (a *App) configLoaded(m configMsg) {
	if m.err != nil {
		a.toast(compositor.ToastError, "plugin settings: "+errText(m.err))
		return
	}
	if m.saved != "" {
		a.toast(compositor.ToastSuccess, m.view.Plugin+": "+m.saved+" saved")
	}
	f := &configForm{cfg: m.view}
	if old, ok := a.ui.overlay.(*configForm); ok && old.cfg.Plugin == m.view.Plugin {
		f.selected = min(old.selected, max(len(m.view.Settings)-1, 0))
	}
	a.ui.overlay = f
}

// saveSetting changes one setting (nil value: back to its default).
func (a *App) saveSetting(id, name string, value any) {
	cl := a.c
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		var unset []string
		set := map[string]any{name: value}
		if value == nil {
			set, unset = nil, []string{name}
		}
		v, err := client.NewPlugins(cl).SetConfig(ctx, id, set, unset)
		return configMsg{view: v, err: err, saved: name}
	})
}

func (f *configForm) key(a *App, k uv.Key) {
	settings := f.cfg.Settings
	switch keyName(k) {
	case "esc", "ctrl+c", "q":
		a.closeOverlay()
	case "up", "k", "ctrl+p":
		f.selected = max(f.selected-1, 0)
	case "down", "j", "ctrl+n":
		f.selected = min(f.selected+1, max(len(settings)-1, 0))
	case "d":
		if f.selected < len(settings) {
			a.saveSetting(f.cfg.Plugin, settings[f.selected].Name, nil)
		}
	case "enter", "space":
		if f.selected >= len(settings) {
			return
		}
		s := settings[f.selected]
		id := f.cfg.Plugin
		if s.Type == "boolean" {
			on, _ := f.cfg.Values[s.Name].(bool)
			a.saveSetting(id, s.Name, !on)
			return
		}
		current := ""
		if v, ok := f.cfg.Values[s.Name]; ok {
			current = fmt.Sprint(v)
		}
		a.openPrompt(fmt.Sprintf("%s (%s)", s.Name, s.Type), current, func(text string) {
			v, err := plugin.ParseSetting(s.Type, text)
			if err != nil {
				a.toast(compositor.ToastError, s.Name+": "+err.Error())
				a.openPluginConfig(id)
				return
			}
			a.saveSetting(id, s.Name, v)
		})
	}
}

func (f *configForm) paste(*App, string) {}

func (f *configForm) click(a *App, line int) {
	if line >= 0 && line < len(f.cfg.Settings) {
		f.selected = line
	}
}

func (f *configForm) view(a *App) compositor.Overlay {
	muted := uv.Style{Fg: a.theme.Muted}
	var lines []compositor.Spans
	for _, s := range f.cfg.Settings {
		value := "(unset)"
		if v, ok := f.cfg.Values[s.Name]; ok {
			value = fmt.Sprint(v)
		}
		line := compositor.Spans{{Text: s.Name, Style: uv.Style{Attrs: uv.AttrBold}}, {Text: " = " + value}}
		if s.Required {
			line = append(line, compositor.Span{Text: " *", Style: uv.Style{Fg: a.theme.Warning}})
		}
		line = append(line, compositor.Span{Text: "  " + s.Type + " · " + s.Description, Style: muted})
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, compositor.Spans{{Text: "this plugin declares no settings", Style: muted}})
	}
	for _, w := range f.cfg.Warnings {
		lines = append(lines, compositor.Spans{{Text: "! " + w, Style: uv.Style{Fg: a.theme.Warning}}})
	}
	if f.cfg.Error != "" {
		lines = append(lines, compositor.Spans{{Text: "✗ " + f.cfg.Error, Style: uv.Style{Fg: a.theme.Error}}})
	}
	return compositor.Overlay{
		Title: "Settings · " + cmpOr(f.cfg.Name, f.cfg.Plugin), Width: 76, Lines: lines, Selected: f.selected,
		Footer: []compositor.Span{{Text: "enter edit · space toggle · d default · esc close", Style: muted}},
	}
}
