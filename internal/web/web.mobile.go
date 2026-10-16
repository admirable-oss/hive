package web

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/observer"
)

// The parts of the web observer made for phones: the Attention view (what
// needs someone), compact cards, the bottom tab bar, notification and
// follow settings, and the installable app's shell (manifest, service
// worker, offline page).

// document is an HTML page with the head every page shares: the app's
// styles and scripts, and what makes it installable.
func document(title string, body ...g.Node) g.Node {
	return h.Doctype(h.HTML(h.Lang("en"),
		h.Head(
			h.Meta(h.Charset("utf-8")),
			h.Meta(h.Name("viewport"), h.Content("width=device-width, initial-scale=1, viewport-fit=cover")),
			h.TitleEl(g.Text(title+" · Hive")),
			h.Meta(h.Name("color-scheme"), h.Content("light dark")),
			h.Meta(h.Name("referrer"), h.Content("no-referrer")),
			h.Meta(h.Name("theme-color"), h.Content("#f6f8fa"), g.Attr("media", "(prefers-color-scheme: light)")),
			h.Meta(h.Name("theme-color"), h.Content("#161b22"), g.Attr("media", "(prefers-color-scheme: dark)")),
			h.Meta(h.Name("apple-mobile-web-app-capable"), h.Content("yes")),
			h.Meta(h.Name("mobile-web-app-capable"), h.Content("yes")),
			h.Meta(h.Name("apple-mobile-web-app-status-bar-style"), h.Content("default")),
			h.Meta(h.Name("apple-mobile-web-app-title"), h.Content("Hive")),
			h.Link(h.Rel("manifest"), h.Href("/manifest.webmanifest")),
			h.Link(h.Rel("icon"), h.Href(assetPath("icon.svg")), h.Type("image/svg+xml")),
			h.Link(h.Rel("apple-touch-icon"), h.Href(assetPath("icon-180.png"))),
			h.Link(h.Rel("stylesheet"), h.Href(assetPath("app.css"))),
			h.Script(h.Src(assetPath("theme.js"))),
			h.Script(h.Src(assetPath("app.js")), h.Defer()),
		),
		h.Body(body...),
	))
}

// tabBar is the phone's bottom navigation.
func tabBar(path string) g.Node {
	item := func(href, label, icon string, current bool) g.Node {
		return h.Li(h.A(h.Href(href), g.If(current, h.Aria("current", "page")),
			h.Span(h.Class("tab-icon"), h.Aria("hidden", "true"), g.Text(icon)), h.Span(g.Text(label))))
	}
	return h.Nav(h.Class("tabbar"), h.Aria("label", "Sections"),
		h.Ul(
			item("/attention", "Attention", "●", path == "/attention"),
			item("/", "Agents", "☰", path == "/"),
			item("/machines", "Machines", "▣", path == "/machines" || strings.HasPrefix(path, "/r/")),
			item("/settings", "Settings", "⚙", path == "/settings" || path == "/access"),
		),
	)
}

// bannerFragment says, in words, when what is shown is not current.
func bannerFragment(p *page) g.Node {
	var lines []g.Node
	for _, r := range p.runtimes {
		switch r.v.State {
		case StateLive, StateIdle:
			continue
		case StateRefused:
			lines = append(lines, h.P(g.Textf("%s refused this observer's access: what is shown may be out of date.", r.rt.Name)))
		default:
			if !r.v.Loaded() {
				continue
			}
			lines = append(lines, h.P(g.Textf("%s is unavailable. Showing what was known at ", r.rt.Name), timeEl(r.v.SyncedAt, p.now), g.Text(".")))
		}
	}
	if len(lines) == 0 {
		return h.Div(h.ID("banner"), h.Data("v", fmt.Sprint(p.version())))
	}
	return h.Div(h.ID("banner"), h.Class("banner"), h.Role("alert"), h.Data("v", fmt.Sprint(p.version())), g.Group(lines))
}

// --- attention ---

// attentionShows are the Attention view's quick filters.
var attentionShows = []struct{ value, label string }{
	{"", "Needs me"}, {"working", "Working"}, {"all", "All"},
}

func attentionPage(p *page, prefs Prefs) g.Node {
	show := p.query.Get("show")
	return layout(p, "Attention",
		h.H1(g.Text("Attention")),
		h.Nav(h.Class("chips"), h.Aria("label", "Show"),
			g.Map(attentionShows, func(s struct{ value, label string }) g.Node {
				href := "/attention"
				if s.value != "" {
					href += "?show=" + s.value
				}
				return h.A(h.Class("chip"), h.Href(href), g.If(show == s.value, h.Aria("current", "true")), g.Text(s.label))
			}),
		),
		g.Group(attentionFragments(p, prefs)),
	)
}

func attentionFragments(p *page, prefs Prefs) []g.Node {
	show := p.query.Get("show")
	rows := p.agents(p.runtimes, "", "")
	var followed, list []agentRow
	for _, r := range rows {
		following := prefs.Following(r.rt.rt.Name, r.a.ID)
		keep := false
		switch show {
		case "all":
			keep = true
		case "working":
			keep = r.a.State == agent.StateWorking
		default:
			keep = r.a.State == agent.StateBlocked || r.a.State == agent.StateDone
		}
		switch {
		case following:
			followed = append(followed, r)
		case keep:
			list = append(list, r)
		}
	}
	// Newest change first: what just happened is what matters.
	slices.SortStableFunc(list, func(x, y agentRow) int { return y.a.Since.Compare(x.a.Since) })
	empty := "Nothing needs you right now."
	if !anyLoaded(p.runtimes) {
		empty = "Connecting to the runtimes…"
	}
	return []g.Node{
		fragment("attention", p.version(),
			g.If(len(followed) > 0, h.Section(h.Aria("labelledby", "followed-h"),
				h.H2(h.ID("followed-h"), g.Text("Following")),
				agentCards(p, followed, "attention"),
			)),
			h.Section(h.Aria("labelledby", "needs-h"),
				h.H2(h.ID("needs-h"), h.Class("sr"), g.Text("Agents")),
				g.If(len(list) == 0, h.P(h.Class("empty"), g.Text(empty))),
				agentCards(p, list, "attention"),
			),
		),
	}
}

// agentCards are agents as compact cards: the whole card is the link.
func agentCards(p *page, rows []agentRow, from string) g.Node {
	return h.Ul(h.Class("agent-cards"), g.Map(rows, func(r agentRow) g.Node {
		href := fmt.Sprintf("/r/%s/e/%s", r.rt.rt.Name, url.PathEscape(r.a.EnvironmentID))
		if r.a.PaneID != "" {
			href = fmt.Sprintf("/r/%s/p/%s", r.rt.rt.Name, url.PathEscape(r.a.PaneID))
		}
		href += "?from=" + from
		return h.Li(h.Class("agent-card state-card-"+string(r.a.State)),
			h.A(h.Href(href),
				h.Span(h.Class("card-top"),
					h.Strong(g.Text(r.a.Name)),
					stateBadge(r.a.State),
				),
				h.Span(h.Class("card-meta"), g.Text(r.a.Kind+" · "+r.a.EnvironmentID+" · "+r.rt.rt.Name+" · "), timeEl(r.a.Since, p.now)),
				g.If(r.a.Reason != "", h.Span(h.Class("card-why"), g.Text(r.a.Reason))),
			),
		)
	}))
}

// --- machines ---

func machinesPage(p *page) g.Node {
	return layout(p, "Machines",
		h.H1(g.Text("Machines")),
		g.Group(machinesFragments(p)),
	)
}

func machinesFragments(p *page) []g.Node {
	return []g.Node{
		fragment("machines", p.version(),
			g.If(len(p.runtimes) == 0, h.P(h.Class("empty"), g.Text("This link shows no runtimes."))),
			h.Ul(h.Class("cards"), g.Map(p.runtimes, func(r *shown) g.Node {
				envs := p.envsOf(r)
				return h.Li(h.Class("card"),
					h.H2(h.A(h.Href("/r/"+r.rt.Name+"?from=machines"), g.Text(r.rt.Name))),
					h.P(h.Class("muted"), g.Text(r.rt.Where())),
					runtimeState(r, p.now),
					stateCounts(p.agents([]*shown{r}, "", "")),
					g.If(len(envs) > 0, h.Ul(h.Class("env-links"), g.Map(envs, func(e observer.Environment) g.Node {
						return h.Li(h.A(h.Href(fmt.Sprintf("/r/%s/e/%s?from=machines", r.rt.Name, url.PathEscape(e.ID))), g.Text(e.ID)))
					}))),
				)
			})),
		),
	}
}

// --- settings ---

func settingsPage(p *page, prefs Prefs, devices []Subscription, pushOn bool, notice string) g.Node {
	checkbox := func(name, label string, on bool) g.Node {
		return h.Label(h.Class("check"), h.Input(h.Type("checkbox"), h.Name(name), h.Value("1"), g.If(on, h.Checked())), g.Text(" "+label))
	}
	radio := func(name, value, label string, on bool) g.Node {
		return h.Label(h.Class("check"), h.Input(h.Type("radio"), h.Name(name), h.Value(value), g.If(on, h.Checked())), g.Text(" "+label))
	}
	return layout(p, "Settings",
		h.H1(g.Text("Settings")),
		g.If(notice != "", h.P(h.Class("notice"), h.Role("status"), g.Text(notice))),

		h.Section(h.Aria("labelledby", "push-h"),
			h.H2(h.ID("push-h"), g.Text("Notifications on this device")),
			g.If(!pushOn, h.P(h.Class("empty"), g.Text("Notifications are turned off on this server."))),
			g.If(pushOn, g.Group{
				h.P(h.ID("push-state"), h.Class("muted"), h.Aria("live", "polite"), g.Text("Checking…")),
				h.P(
					h.Button(h.Type("button"), h.ID("push-on"), g.Attr("hidden"), g.Text("Turn on notifications")), g.Text(" "),
					h.Button(h.Type("button"), h.ID("push-off"), g.Attr("hidden"), g.Text("Turn off on this device")), g.Text(" "),
					h.Button(h.Type("button"), h.ID("push-test"), g.Attr("hidden"), g.Text("Send a test")),
				),
				h.P(h.Class("muted small"), g.Text("On iPhone and iPad, add Hive to the Home Screen first (Share → Add to Home Screen), then turn notifications on from there.")),
				g.If(len(devices) > 0, h.P(h.Class("muted small"), g.Textf("%d device(s) get notifications through this sign-in.", len(devices)))),
			}),
		),

		h.Form(h.Method("post"), h.Action("/settings"), h.Class("settings"),
			h.Input(h.Type("hidden"), h.Name("action"), h.Value("prefs")),
			h.FieldSet(h.Legend(g.Text("Tell me when an agent")),
				checkbox("blocked", "is waiting for me (blocked)", prefs.Blocked),
				checkbox("done", "finishes a task (done)", prefs.Done),
				checkbox("failed", "stops with an error", prefs.Failed),
			),
			h.FieldSet(h.Legend(g.Text("From")),
				g.Map(p.runtimes, func(r *shown) g.Node {
					return checkbox("on_"+r.rt.Name, r.rt.Name+" ("+r.rt.Where()+")", !slices.Contains(prefs.Off, r.rt.Name))
				}),
			),
			h.FieldSet(h.Legend(g.Text("Show in notifications")),
				radio("detail", DetailNames, "names of agents, environments and machines", prefs.Detail != DetailPrivate),
				radio("detail", DetailPrivate, "nothing but “An agent needs you”", prefs.Detail == DetailPrivate),
			),
			h.P(h.Class("muted small"), g.Text("Notifications never include what is on a screen. Whether they show on the lock screen is up to your phone's settings.")),
			h.Button(h.Type("submit"), g.Text("Save")),
		),

		h.Section(h.Aria("labelledby", "mutes-h"),
			h.H2(h.ID("mutes-h"), g.Text("Muted")),
			g.If(len(prefs.Mutes) == 0, h.P(h.Class("empty"), g.Text("Nothing is muted. Mute an agent or an environment from its page."))),
			h.Ul(h.Class("plain"), g.Map(indexed(prefs.Mutes), func(m indexedMute) g.Node {
				what := m.Runtime + " / " + m.Environment
				if m.Agent != "" {
					what = m.Runtime + " / agent " + agentName(p, m.Runtime, m.Agent)
				}
				until := "until unmuted"
				if m.Until != nil {
					until = "until " + m.Until.Local().Format("Mon 15:04")
				}
				return h.Li(h.Form(h.Method("post"), h.Action("/settings"), h.Class("inline"),
					h.Input(h.Type("hidden"), h.Name("action"), h.Value("unmute")),
					h.Input(h.Type("hidden"), h.Name("index"), h.Value(fmt.Sprint(m.i))),
					g.Text(what+", "+until+" "),
					h.Button(h.Type("submit"), g.Text("Unmute")),
				))
			})),
		),
		h.P(h.A(h.Href("/access"), g.Text("Your access and sign-out"))),
	)
}

type indexedMute struct {
	Mute
	i int
}

func indexed(ms []Mute) []indexedMute {
	out := make([]indexedMute, len(ms))
	for i, m := range ms {
		out[i] = indexedMute{m, i}
	}
	return out
}

func agentName(p *page, rt, id string) string {
	if r := p.runtime(rt); r != nil {
		if a, ok := r.v.Agents[id]; ok {
			return a.Name
		}
	}
	return id
}

// followMuteControls are a pane's follow and mute buttons: settings of
// this browser, nothing that reaches Hive.
func followMuteControls(p *page, r *shown, a *observer.Agent, prefs Prefs, back string) g.Node {
	if a == nil {
		return nil
	}
	hidden := func(name, value string) g.Node { return h.Input(h.Type("hidden"), h.Name(name), h.Value(value)) }
	following := prefs.Following(r.rt.Name, a.ID)
	action := "follow"
	label := "Follow"
	if following {
		action, label = "unfollow", "Unfollow"
	}
	muted := prefs.muted(r.rt.Name, a.EnvironmentID, a.ID, p.now)
	return h.Div(h.Class("controls"),
		h.Form(h.Method("post"), h.Action("/settings"), h.Class("inline"),
			hidden("action", action), hidden("runtime", r.rt.Name), hidden("agent", a.ID), hidden("back", back),
			h.Button(h.Type("submit"), g.Attr("aria-pressed", fmt.Sprint(following)), g.Text(label)),
		),
		g.Iff(!muted, func() g.Node {
			return h.Form(h.Method("post"), h.Action("/settings"), h.Class("inline"),
				hidden("action", "mute"), hidden("runtime", r.rt.Name), hidden("agent", a.ID), hidden("back", back),
				h.Label(h.For("mute-for"), h.Class("sr"), g.Text("Mute for")),
				h.Select(h.ID("mute-for"), h.Name("for"),
					h.Option(h.Value("1h"), g.Text("for an hour")),
					h.Option(h.Value("tomorrow"), g.Text("until tomorrow")),
					h.Option(h.Value("always"), g.Text("until unmuted")),
				),
				h.Button(h.Type("submit"), g.Text("Mute")),
			)
		}),
		g.If(muted, h.Span(h.Class("muted"), g.Text("Notifications muted (Settings)"))),
	)
}

// muteUntil turns a mute duration into its end (nil: until unmuted).
func muteUntil(d string, now time.Time) (*time.Time, bool) {
	switch d {
	case "1h":
		t := now.Add(time.Hour)
		return &t, true
	case "tomorrow":
		y, m, day := now.Local().Date()
		t := time.Date(y, m, day+1, 8, 0, 0, 0, now.Local().Location())
		return &t, true
	case "always":
		return nil, true
	}
	return nil, false
}

// --- offline ---

// offlinePage is what the installed app shows with no network. It is
// cached, so it holds nothing about any agent.
func offlinePage() g.Node {
	return document("Offline",
		h.Main(h.ID("main"), h.Class("login"),
			h.H1(g.Text("You are offline")),
			h.P(g.Text("Hive will show your agents again as soon as this device is back online.")),
			h.P(h.A(h.Href("/attention"), g.Text("Try again"))),
		),
	)
}
