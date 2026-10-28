package web

import (
	"context"
	"time"

	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/plugin"
)

// Plugins (M26) on a runtime's page, for links that see the whole runtime:
// each installed plugin with who stands behind it (its tier and publisher),
// what confines it, and a revoked release in red. Read through
// observe.plugins; a link scoped to part of the runtime sees none, since
// plugins act on all of it.

// pluginsEvery is how long plugins read for a page stand.
const pluginsEvery = time.Minute

// pluginsRead is the plugins as read at a time; nil when the runtime did
// not answer (one older than M26).
type pluginsRead struct {
	at      time.Time
	plugins []observer.Plugin
	ok      bool
}

// Plugins returns the runtime's plugins, read at most every pluginsEvery;
// false while it is not connected or does not say.
func (c *conn) Plugins() ([]observer.Plugin, bool) {
	now := c.now()
	c.insightsMu.Lock()
	got := c.plugins
	c.insightsMu.Unlock()
	if !got.at.IsZero() && now.Sub(got.at) < pluginsEvery {
		return got.plugins, got.ok
	}
	cl := c.current()
	if cl == nil {
		return got.plugins, got.ok // as last read, if ever
	}
	ctx, cancel := context.WithTimeout(context.Background(), insightsWait)
	defer cancel()
	list, err := cl.Plugins(ctx)
	if err != nil {
		c.log.Debug("no plugins (a runtime older than M26?)", "err", err)
	}
	read := pluginsRead{at: now, plugins: list, ok: err == nil}
	c.insightsMu.Lock()
	c.plugins = read
	c.insightsMu.Unlock()
	return read.plugins, read.ok
}

// pluginsSection is a runtime's plugins; nothing for a link scoped to part
// of the runtime, a runtime that does not say, or one with none.
func pluginsSection(p *page, r *shown) g.Node {
	if !p.grant.Scope.All || r.c == nil || !r.v.Loaded() {
		return g.Group(nil)
	}
	list, ok := r.c.Plugins()
	if !ok || len(list) == 0 {
		return g.Group(nil)
	}
	return h.Section(h.Aria("labelledby", "plugins-h"),
		h.H2(h.ID("plugins-h"), g.Text("Plugins")),
		h.Table(h.Class("agents plugins"),
			h.Caption(h.Class("sr"), g.Text("Installed plugins, who stands behind each, and what confines it")),
			h.THead(h.Tr(
				h.Th(h.Scope("col"), g.Text("Plugin")), h.Th(h.Scope("col"), g.Text("Tier")),
				h.Th(h.Scope("col"), g.Text("Signed by")), h.Th(h.Scope("col"), g.Text("Sandbox")),
				h.Th(h.Scope("col"), g.Text("State")),
			)),
			h.TBody(g.Map(list, func(pl observer.Plugin) g.Node {
				tier := cmpText(pl.Tier, "unchecked")
				state := cmpText(pl.State, "ready")
				if !pl.Enabled {
					state = "disabled"
				}
				sandbox := "none"
				if pl.Sandbox != "" && pl.Sandbox != "none" {
					sandbox = pl.Sandbox
				}
				return h.Tr(g.If(pl.Revoked != nil, h.Class("revoked")),
					h.Th(h.Scope("row"), g.Text(pl.Name), h.Span(h.Class("kind"), g.Text(" "+pl.ID+" "+pl.Version))),
					h.Td(h.Span(h.Class("badge tier-"+tier), h.Title(plugin.TierMeaning(pl.Tier)), g.Text(tier)),
						g.If(pl.Publisher != "", h.Span(h.Class("kind"), g.Text(" "+pl.Publisher)))),
					h.Td(h.Class("muted"), g.Text(cmpText(pl.SignedBy, "-"))),
					h.Td(g.Text(sandbox)),
					h.Td(
						g.Text(state),
						g.Iff(pl.Revoked != nil, func() g.Node {
							return h.Strong(h.Class("revoked-why"), g.Text(" Revoked: "+pl.Revoked.Reason),
								g.If(pl.Revoked.Advisory != "", h.A(h.Href(pl.Revoked.Advisory), h.Rel("noopener noreferrer"), g.Text(" advisory"))))
						}),
					),
				)
			})),
		),
	)
}
