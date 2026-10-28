package mux

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// The Plugins view lists the installed plugins with who stands behind
// each (its tier), what confines it (its sandbox) and whether any release
// of it was revoked, and opens one's details. It is opened from the
// palette.

// paletteKindPlugins is the palette's own entry that opens the view.
const paletteKindPlugins = "plugins"

type pluginList struct {
	plugins  []plugin.Status
	selected int
	// detail is the plugin shown in full, or -1 for the list.
	detail int
}

// pluginListMsg carries a fresh read of plugin.list.
type pluginListMsg struct {
	plugins []plugin.Status
	err     error
}

func (a *App) openPluginList() {
	cl := a.c
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		got, err := client.NewPlugins(cl).List(ctx)
		return pluginListMsg{plugins: got, err: err}
	})
}

func (a *App) pluginListLoaded(m pluginListMsg) {
	if m.err != nil {
		a.toast(compositor.ToastError, "plugins: "+errText(m.err))
		return
	}
	l := &pluginList{plugins: m.plugins, detail: -1}
	if old, ok := a.ui.overlay.(*pluginList); ok {
		l.selected = min(old.selected, max(len(m.plugins)-1, 0))
	}
	a.ui.overlay = l
}

func (l *pluginList) key(a *App, k uv.Key) {
	switch keyName(k) {
	case "esc", "ctrl+c", "q":
		if l.detail >= 0 {
			l.detail = -1
			return
		}
		a.closeOverlay()
	case "up", "k", "ctrl+p":
		if l.detail < 0 {
			l.selected = max(l.selected-1, 0)
		}
	case "down", "j", "ctrl+n":
		if l.detail < 0 {
			l.selected = min(l.selected+1, max(len(l.plugins)-1, 0))
		}
	case "enter", "space":
		if l.detail < 0 && l.selected < len(l.plugins) {
			l.detail = l.selected
		}
	case "r":
		a.openPluginList()
	}
}

func (l *pluginList) paste(*App, string) {}

func (l *pluginList) click(_ *App, line int) {
	if l.detail < 0 && line >= 0 && line < len(l.plugins) {
		if l.selected == line {
			l.detail = line
		}
		l.selected = line
	}
}

// tierColor marks a tier: official and verified stand out, community and
// unlisted do not.
func (a *App) tierColor(tier string) color.Color {
	switch tier {
	case plugin.TierOfficial:
		return a.theme.Accent
	case plugin.TierVerified:
		return a.theme.Success
	}
	return a.theme.Muted
}

func (l *pluginList) view(a *App) compositor.Overlay {
	muted := uv.Style{Fg: a.theme.Muted}
	if l.detail >= 0 && l.detail < len(l.plugins) {
		return l.detailView(a, l.plugins[l.detail])
	}
	var lines []compositor.Spans
	for _, p := range l.plugins {
		version := p.Version
		if p.Manifest != nil {
			version = p.Manifest.Version
		}
		tier := cmpOr(p.Tier, "unchecked")
		line := compositor.Spans{
			{Text: p.ID, Style: uv.Style{Attrs: uv.AttrBold}},
			{Text: " " + version, Style: muted},
			{Text: " [" + tier + "]", Style: uv.Style{Fg: a.tierColor(tier)}},
			{Text: " " + sandboxLabel(p.Sandbox), Style: muted},
		}
		switch {
		case p.Revoked != nil:
			line = append(line, compositor.Span{Text: "  revoked: " + p.Revoked.Reason, Style: uv.Style{Fg: a.theme.Error, Attrs: uv.AttrBold}})
		case p.Problem != "":
			line = append(line, compositor.Span{Text: "  " + p.Problem, Style: uv.Style{Fg: a.theme.Error}})
		case !p.Enabled:
			line = append(line, compositor.Span{Text: "  disabled", Style: muted})
		default:
			line = append(line, compositor.Span{Text: "  " + p.Health.State, Style: muted})
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, compositor.Spans{{Text: "no plugins installed (hive plugin search)", Style: muted}})
	}
	return compositor.Overlay{
		Title: "Plugins", Width: 88, Lines: lines, Selected: l.selected,
		Footer: []compositor.Span{{Text: "enter details · r refresh · esc close", Style: muted}},
	}
}

func (l *pluginList) detailView(a *App, p plugin.Status) compositor.Overlay {
	muted := uv.Style{Fg: a.theme.Muted}
	row := func(label, value string, style uv.Style) compositor.Spans {
		return compositor.Spans{{Text: fmt.Sprintf("%-11s", label), Style: muted}, {Text: value, Style: style}}
	}
	tier := cmpOr(p.Tier, "unchecked")
	lines := []compositor.Spans{
		row("tier", tier+" · "+plugin.TierMeaning(p.Tier), uv.Style{Fg: a.tierColor(tier)}),
	}
	if p.Publisher != "" {
		lines = append(lines, row("publisher", p.Publisher, uv.Style{}))
	}
	if p.Registry {
		lines = append(lines, row("signed by", cmpOr(p.SignedBy, "nobody (not signed)"), uv.Style{}))
	}
	if p.Provenance != nil {
		lines = append(lines, row("built by", p.Provenance.String(), uv.Style{}))
	}
	lines = append(lines, row("sandbox", sandboxLabel(p.Sandbox), uv.Style{}))
	if p.Revoked != nil {
		text := p.Revoked.Reason
		if p.Revoked.Advisory != "" {
			text += " · " + p.Revoked.Advisory
		}
		lines = append(lines, row("revoked", text, uv.Style{Fg: a.theme.Error, Attrs: uv.AttrBold}))
	}
	if p.Version != "" {
		lines = append(lines, row("release", p.Version+" · "+shortCommit(p.Commit), uv.Style{}))
	}
	lines = append(lines,
		row("source", p.Source, uv.Style{}),
		row("holds", cmpOr(strings.Join(p.Grant.Effective, ", "), "nothing"), uv.Style{}))
	if !p.CheckedAt.IsZero() {
		lines = append(lines, row("checked", p.CheckedAt.Local().Format(time.DateTime), muted))
	}
	return compositor.Overlay{
		Title: "Plugin · " + p.ID, Width: 88, Lines: lines, Selected: -1,
		Footer: []compositor.Span{{Text: "esc back · hive plugin doctor checks the registry again", Style: muted}},
	}
}

// sandboxLabel names what confines a plugin.
func sandboxLabel(s string) string {
	switch s {
	case "", plugin.SandboxNone:
		return "unsandboxed"
	}
	return "sandboxed (" + s + ")"
}

func shortCommit(c string) string { return c[:min(len(c), 12)] }
