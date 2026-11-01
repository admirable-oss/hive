package web

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/task"
)

// The web observer's pages are rendered on the server with gomponents. Parts
// that change live are fragments with stable IDs: the stream (stream.go)
// renders them again when the runtime changes and sends the ones that
// differ, which app.js swaps in. Nothing on any page can act on Hive: there
// are links and one sign-out form, no buttons that reach a runtime.

// page is what every page knows: who is looking, and the runtimes they
// may see.
type page struct {
	grant    Grant
	session  Session
	runtimes []*shown // in name order
	now      time.Time
	path     string
	query    url.Values
	prefs    Prefs
}

// shown is a runtime as a page shows it: its configuration and a copy of
// its model.
type shown struct {
	rt Runtime
	v  RuntimeView
	// c is its connection, for what pages read on demand (insights); nil
	// in tests of the views alone.
	c *conn
}

func (p *page) runtime(name string) *shown {
	for _, r := range p.runtimes {
		if r.rt.Name == name {
			return r
		}
	}
	return nil
}

// sees reports whether the grant shows something at t in a runtime.
func (p *page) sees(t observer.Target) bool { return p.grant.Scope.Sees(t) }

// version sums the shown runtimes' versions: it grows whenever any of
// them changes, so a browser can drop a fragment older than one it has.
func (p *page) version() uint64 {
	var v uint64
	for _, r := range p.runtimes {
		v += r.v.Version
	}
	return v
}

func (p *page) screens(r *shown) bool { return p.grant.Screens && r.rt.Screens }
func (p *page) history(r *shown) bool { return p.grant.History && r.rt.Screens }
func fragment(id string, v uint64, children ...g.Node) g.Node {
	return h.Div(h.ID(id), h.Data("v", strconv.FormatUint(v, 10)), g.Group(children))
}

// layout wraps a page's content.
func layout(p *page, title string, content ...g.Node) g.Node {
	return document(title,
		h.A(h.Class("skip"), h.Href("#main"), g.Text("Skip to content")),
		h.Header(h.Class("top"),
			h.A(h.Class("brand"), h.Href("/attention"), h.Aria("label", "Hive: agents that need you"), g.Text("Hive")),
			h.Nav(h.Class("topnav"), h.Aria("label", "Sections"),
				h.Ul(
					h.Li(navLink("/attention", "Attention", p.path == "/attention")),
					h.Li(navLink("/", "Agents", p.path == "/")),
					g.Map(p.runtimes, func(r *shown) g.Node {
						href := "/r/" + r.rt.Name
						return h.Li(navLink(href, r.rt.Name, strings.HasPrefix(p.path, href)))
					}),
					h.Li(navLink("/settings", "Settings", p.path == "/settings")),
				),
			),
			h.Div(h.Class("tools"),
				connFragment(p),
				h.Span(h.Class("ro"), h.Title("Nothing here can type into, start or stop an agent"), g.Text("Read-only")),
				h.Button(h.Type("button"), h.Class("theme"), h.ID("theme"), h.Aria("label", "Colour theme"), g.Text("Theme")),
			),
		),
		h.Main(h.ID("main"), h.TabIndex("-1"),
			backLink(p),
			bannerFragment(p),
			g.Group(content),
		),
		h.Footer(
			h.P(g.Textf("Observing read-only through “%s”. Access ends %s. ", p.grant.Label, when(p.grant.ExpiresAt, p.now)), h.A(h.Href("/access"), g.Text("Access"))),
		),
		tabBar(p.path),
	)
}

// backLink returns to where a page was opened from (?from=), keeping the
// viewer's place when moving between sessions.
func backLink(p *page) g.Node {
	targets := map[string][2]string{
		"attention":    {"/attention", "Attention"},
		"notification": {"/attention", "Attention"},
		"machines":     {"/machines", "Machines"},
		"agents":       {"/", "Agents"},
	}
	t, ok := targets[p.query.Get("from")]
	if !ok {
		return nil
	}
	return h.P(h.Class("back"), h.A(h.Href(t[0]), h.Data("back", "1"), g.Text("‹ "+t[1])))
}

func navLink(href, text string, current bool) g.Node {
	return h.A(h.Href(href), g.If(current, h.Aria("current", "page")), g.Text(text))
}

// connFragment says whether what is shown is live: per runtime when they
// differ.
func connFragment(p *page) g.Node {
	worst, oldest := StateLive, time.Time{}
	var problems []string
	rank := map[string]int{StateLive: 0, StateIdle: 1, StateConnecting: 2, StateRetrying: 3, StateRefused: 4}
	for _, r := range p.runtimes {
		if rank[r.v.State] > rank[worst] {
			worst = r.v.State
		}
		if !r.v.SyncedAt.IsZero() && (oldest.IsZero() || r.v.SyncedAt.Before(oldest)) {
			oldest = r.v.SyncedAt
		}
		if r.v.State != StateLive && r.v.State != StateIdle {
			problems = append(problems, r.rt.Name+": "+r.v.State)
		}
	}
	text := map[string]string{
		StateLive: "Live", StateIdle: "Live", StateConnecting: "Connecting…",
		StateRetrying: "Reconnecting…", StateRefused: "Access refused",
	}[worst]
	synced := "never"
	if !oldest.IsZero() {
		synced = oldest.Local().Format(time.TimeOnly)
	}
	return h.Div(h.ID("conn"), h.Class("conn conn-"+worst), h.Role("status"), h.Aria("live", "polite"),
		h.Data("v", strconv.FormatUint(p.version(), 10)),
		h.Data("synced", syncedAttr(oldest)),
		h.Span(h.Class("dot"), h.Aria("hidden", "true")),
		h.Span(h.Class("conn-text"), g.Text(text)),
		h.Span(h.Class("conn-sync"), g.Text(" · synced "+synced)),
		g.If(len(problems) > 0, h.Span(h.Class("sr"), g.Text(" ("+strings.Join(problems, ", ")+")"))),
	)
}

func syncedAttr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.UnixMilli(), 10)
}

// when is a time relative to now ("in 3h", "5m ago").
func when(t, now time.Time) string {
	d := t.Sub(now)
	switch {
	case d > 0:
		return "in " + short(d)
	case -d < 10*time.Second:
		return "just now"
	}
	return short(-d) + " ago"
}

func short(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// timeEl shows a time. Its text is absolute, so a fragment does not change
// as time passes; app.js shows it relative ("5m ago") and keeps it so.
func timeEl(t, now time.Time) g.Node {
	if t.IsZero() {
		return g.Text("–")
	}
	text := t.Local().Format(time.TimeOnly)
	if now.Sub(t) > 20*time.Hour || t.Sub(now) > 20*time.Hour {
		text = t.Local().Format(time.DateTime)
	}
	return h.Time(h.DateTime(t.UTC().Format(time.RFC3339)), h.Title(t.Local().Format(time.DateTime)), g.Text(text))
}

// stateRank orders agents: those needing someone first.
var stateRank = map[agent.State]int{
	agent.StateBlocked: 0, agent.StateDone: 1, agent.StateWorking: 2, agent.StateIdle: 3, agent.StateUnknown: 4, agent.StateExited: 5,
}

var stateLabel = map[agent.State]string{
	agent.StateBlocked: "Blocked", agent.StateDone: "Done", agent.StateWorking: "Working",
	agent.StateIdle: "Idle", agent.StateUnknown: "Unknown", agent.StateExited: "Exited",
}

func stateBadge(s agent.State) g.Node {
	label := stateLabel[s]
	if label == "" {
		label = string(s)
	}
	return h.Span(h.Class("badge state-"+string(s)), g.Text(label))
}

// agentRow is one agent for a table.
type agentRow struct {
	rt *shown
	a  observer.Agent
}

// agents lists the agents a page may show in runtimes, filtered.
func (p *page) agents(rts []*shown, q, state string) []agentRow {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []agentRow
	for _, r := range rts {
		for _, a := range r.v.Agents {
			if !p.sees(observer.Target{Environment: a.EnvironmentID, Pane: a.PaneID}) {
				continue
			}
			if state != "" && string(a.State) != state {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(strings.Join([]string{a.Name, a.Kind, a.EnvironmentID, r.rt.Name, a.Reason, paneName(r, a.PaneID)}, " ")), q) {
				continue
			}
			out = append(out, agentRow{r, a})
		}
	}
	slices.SortFunc(out, func(x, y agentRow) int {
		if d := stateRank[x.a.State] - stateRank[y.a.State]; d != 0 {
			return d
		}
		return strings.Compare(x.a.Name+x.rt.rt.Name, y.a.Name+y.rt.rt.Name)
	})
	return out
}

func paneName(r *shown, id string) string {
	if p, ok := r.v.Panes[id]; ok {
		return paneLabel(p)
	}
	return ""
}

func agentsTable(p *page, rows []agentRow, empty string) g.Node {
	if len(rows) == 0 {
		return h.P(h.Class("empty"), g.Text(empty))
	}
	return h.Table(h.Class("agents"),
		h.Caption(h.Class("sr"), g.Text("Agents and their states")),
		h.THead(h.Tr(
			h.Th(h.Scope("col"), g.Text("Agent")), h.Th(h.Scope("col"), g.Text("State")),
			h.Th(h.Scope("col"), g.Text("Where")), h.Th(h.Scope("col"), g.Text("Since")), h.Th(h.Scope("col"), g.Text("Why")),
		)),
		h.TBody(g.Map(rows, func(r agentRow) g.Node {
			name := g.Text(r.a.Name)
			if r.a.PaneID != "" {
				name = h.A(h.Href(fmt.Sprintf("/r/%s/p/%s", r.rt.rt.Name, url.PathEscape(r.a.PaneID))), g.Text(r.a.Name))
			}
			return h.Tr(h.Class("state-row-"+string(r.a.State)),
				h.Td(name, h.Span(h.Class("kind"), g.Text(" "+r.a.Kind))),
				h.Td(stateBadge(r.a.State)),
				h.Td(
					h.A(h.Href("/r/"+r.rt.rt.Name), g.Text(r.rt.rt.Name)), g.Text(" / "),
					h.A(h.Href(fmt.Sprintf("/r/%s/e/%s", r.rt.rt.Name, url.PathEscape(r.a.EnvironmentID))), g.Text(r.a.EnvironmentID)),
				),
				h.Td(timeEl(r.a.Since, p.now)),
				h.Td(h.Class("why"), g.Text(r.a.Reason)),
			)
		})),
	)
}

// stateCounts counts agents by state.
func stateCounts(rows []agentRow) g.Node {
	n := map[agent.State]int{}
	for _, r := range rows {
		n[r.a.State]++
	}
	var parts []g.Node
	for _, s := range agent.States {
		if n[s] > 0 {
			parts = append(parts, h.Li(stateBadge(s), g.Textf(" %d", n[s])))
		}
	}
	if len(parts) == 0 {
		return h.P(h.Class("muted"), g.Text("No agents"))
	}
	return h.Ul(h.Class("counts"), g.Group(parts))
}

func runtimeState(r *shown, now time.Time) g.Node {
	text := map[string]string{StateIdle: "not watched", StateConnecting: "connecting", StateLive: "live", StateRetrying: "reconnecting", StateRefused: "refused"}[r.v.State]
	return h.Span(h.Class("badge conn-"+r.v.State),
		g.Text(text),
		g.Iff(r.v.State != StateLive && !r.v.SyncedAt.IsZero(), func() g.Node { return g.Group{g.Text(", data from "), timeEl(r.v.SyncedAt, now)} }),
	)
}

// --- dashboard ---

func dashboardPage(p *page) g.Node {
	q, state := p.query.Get("q"), p.query.Get("state")
	return layout(p, "Agents",
		h.H1(g.Text("Agents")),
		h.Form(h.Class("filters"), h.Method("get"), h.Action("/"), h.Role("search"),
			h.Label(h.For("q"), h.Class("sr"), g.Text("Search")),
			h.Input(h.Type("search"), h.ID("q"), h.Name("q"), h.Value(q), h.Placeholder("Search agents, environments, panes  ( / )"), h.AutoComplete("off")),
			h.Label(h.For("state"), h.Class("sr"), g.Text("State")),
			h.Select(h.ID("state"), h.Name("state"),
				h.Option(h.Value(""), g.Text("Any state")),
				g.Map(agent.States, func(s agent.State) g.Node {
					return h.Option(h.Value(string(s)), g.If(string(s) == state, h.Selected()), g.Text(stateLabel[s]))
				}),
			),
			h.NoScript(h.Button(h.Type("submit"), g.Text("Filter"))),
		),
		g.Group(dashboardFragments(p)),
	)
}

func dashboardFragments(p *page) []g.Node {
	q, state := p.query.Get("q"), p.query.Get("state")
	rows := p.agents(p.runtimes, q, state)
	return []g.Node{
		fragment("runtimes", p.version(),
			h.Section(h.Aria("labelledby", "runtimes-h"),
				h.H2(h.ID("runtimes-h"), g.Text("Runtimes")),
				g.If(len(p.runtimes) == 0, h.P(h.Class("empty"), g.Text("This link shows no runtimes."))),
				h.Ul(h.Class("cards"), g.Map(p.runtimes, func(r *shown) g.Node {
					return h.Li(h.Class("card"),
						h.H3(h.A(h.Href("/r/"+r.rt.Name), g.Text(r.rt.Name))),
						h.P(h.Class("muted"), g.Text(r.rt.Where())),
						runtimeState(r, p.now),
						stateCounts(p.agents([]*shown{r}, "", "")),
					)
				})),
			),
		),
		fragment("agents", p.version(),
			h.Section(h.Aria("labelledby", "agents-h"),
				h.H2(h.ID("agents-h"), g.Textf("%d agent(s)", len(rows))),
				agentsTable(p, rows, emptyAgents(p, q, state)),
			),
		),
	}
}

func emptyAgents(p *page, q, state string) string {
	switch {
	case q != "" || state != "":
		return "No agent matches."
	case !anyLoaded(p.runtimes):
		return "Connecting to the runtimes…"
	}
	return "No agents are running where this link can see."
}

func anyLoaded(rts []*shown) bool {
	return slices.ContainsFunc(rts, func(r *shown) bool { return r.v.Loaded() })
}

// --- runtime (one session on one machine) ---

func runtimePage(p *page, r *shown) g.Node {
	return layout(p, r.rt.Name,
		h.Nav(h.Class("crumbs"), h.Aria("label", "Breadcrumb"), h.A(h.Href("/"), g.Text("Agents")), g.Text(" › "+r.rt.Name)),
		h.H1(g.Text(r.rt.Name)),
		g.Group(runtimeFragments(p, r)),
	)
}

func runtimeFragments(p *page, r *shown) []g.Node {
	envs := p.envsOf(r)
	return []g.Node{
		fragment("rt-head", r.v.Version,
			h.Dl(h.Class("facts"),
				fact("Where", g.Text(r.rt.Where())),
				fact("Connection", runtimeState(r, p.now)),
				g.Iff(r.v.Status != nil, func() g.Node {
					return g.Group{
						fact("Hive", g.Text(statusVersion(r.v.Status))),
						fact("Up since", timeEl(statusStarted(r.v.Status), p.now)),
					}
				}),
				fact("Last synchronised", timeEl(r.v.SyncedAt, p.now)),
				fact("Screens", g.Text(yesNo(p.screens(r)))),
			),
		),
		fragment("envs", r.v.Version,
			h.Section(h.Aria("labelledby", "envs-h"),
				h.H2(h.ID("envs-h"), g.Text("Environments")),
				g.If(len(envs) == 0, h.P(h.Class("empty"), g.Text(emptyText(r, "No environments this link can see.")))),
				h.Ul(h.Class("cards"), g.Map(envs, func(e observer.Environment) g.Node {
					return h.Li(h.Class("card"),
						h.H3(h.A(h.Href(fmt.Sprintf("/r/%s/e/%s", r.rt.Name, url.PathEscape(e.ID))), g.Text(e.ID))),
						h.P(h.Class("muted path"), g.Text(e.Path)),
						g.Iff(e.Git != nil, func() g.Node { return h.P(h.Class("git"), g.Text(gitText(e))) }),
						stateCounts(p.agentsIn(r, e.ID)),
					)
				})),
			),
		),
		fragment("agents", r.v.Version,
			h.Section(h.Aria("labelledby", "agents-h"),
				h.H2(h.ID("agents-h"), g.Text("Agents")),
				agentsTable(p, p.agents([]*shown{r}, "", ""), emptyText(r, "No agents this link can see.")),
			),
		),
		fragment("insights", r.v.Version, insightsSection(p, r)),
		fragment("plugins", r.v.Version, pluginsSection(p, r)),
		fragment("plans", r.v.Version, plansSection(p, r)),
		fragment("activity", r.v.Version, activityList(p, r, "", "")),
	}
}

// plansOf lists the plans a page may show of a runtime, newest first, each
// with only the tasks the page sees. A plan known only as a whole (no task
// seen) shows only to a page that sees everything.
func (p *page) plansOf(r *shown) []observer.Plan {
	var out []observer.Plan
	for _, pl := range r.v.Plans {
		var tasks []observer.PlanTask
		for _, t := range pl.Tasks {
			if p.sees(t.Target()) {
				tasks = append(tasks, t)
			}
		}
		if len(tasks) == 0 && !p.sees(observer.Target{}) {
			continue
		}
		pl.Tasks = tasks
		out = append(out, pl)
	}
	slices.SortFunc(out, func(a, b observer.Plan) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out
}

// plansSection is the runtime's plans: each one's progress, and its tasks
// that run or wait.
func plansSection(p *page, r *shown) g.Node {
	plans := p.plansOf(r)
	if len(plans) == 0 {
		return g.Group(nil) // most runtimes run no plans: no empty section
	}
	return h.Section(h.Aria("labelledby", "plans-h"),
		h.H2(h.ID("plans-h"), g.Text("Plans")),
		h.Table(h.Class("agents plans"),
			h.Caption(h.Class("sr"), g.Text("Plans and their progress")),
			h.THead(h.Tr(
				h.Th(h.Scope("col"), g.Text("Plan")), h.Th(h.Scope("col"), g.Text("State")),
				h.Th(h.Scope("col"), g.Text("Progress")), h.Th(h.Scope("col"), g.Text("Running and waiting")),
				h.Th(h.Scope("col"), g.Text("Started")),
			)),
			h.TBody(g.Map(plans, func(pl observer.Plan) g.Node {
				var active []string
				for _, t := range pl.Tasks {
					switch t.State {
					case task.StateRunning, task.StateBlocked, task.StateApproval:
						active = append(active, t.ID+" ("+string(t.State)+")")
					}
				}
				name := pl.ID
				if pl.Name != "" {
					name = pl.Name + " · " + pl.ID
				}
				return h.Tr(h.Class("state-row-"+string(pl.State)),
					h.Td(g.Text(name)),
					h.Td(h.Span(h.Class("badge plan-"+string(pl.State)), g.Text(string(pl.State)))),
					h.Td(g.Text(planProgress(pl))),
					h.Td(h.Class("why"), g.Text(strings.Join(active, ", "))),
					h.Td(timeEl(pl.CreatedAt, p.now)),
				)
			})),
		),
	)
}

// planProgress is "3/10 succeeded · 2 running".
func planProgress(pl observer.Plan) string {
	parts := []string{fmt.Sprintf("%d/%d succeeded", pl.Counts[task.StateSucceeded], pl.Total)}
	for _, st := range []task.State{task.StateRunning, task.StateBlocked, task.StateApproval, task.StateQueued, task.StateFailed, task.StateSkipped, task.StateCancelled} {
		if n := pl.Counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, st))
		}
	}
	return strings.Join(parts, " · ")
}

func emptyText(r *shown, loaded string) string {
	if !r.v.Loaded() {
		return "Connecting…"
	}
	return loaded
}

func statusVersion(st *observer.Status) string {
	return st.Version + " (session " + st.Session + ")"
}

func statusStarted(st *observer.Status) time.Time { return st.StartedAt }

func fact(name string, value g.Node) g.Node {
	return g.Group{h.Dt(g.Text(name)), h.Dd(value)}
}

func yesNo(b bool) string {
	if b {
		return "shared"
	}
	return "not shared"
}

func gitText(e observer.Environment) string {
	gs := e.Git
	branch := gs.Branch
	if gs.Detached {
		branch = "detached at " + gs.Head
	}
	var marks []string
	if gs.Ahead > 0 {
		marks = append(marks, fmt.Sprintf("↑%d", gs.Ahead))
	}
	if gs.Behind > 0 {
		marks = append(marks, fmt.Sprintf("↓%d", gs.Behind))
	}
	if n := gs.Staged + gs.Modified + gs.Untracked; n > 0 {
		marks = append(marks, fmt.Sprintf("%d changed", n))
	}
	if gs.Conflicts > 0 {
		marks = append(marks, fmt.Sprintf("%d conflicts", gs.Conflicts))
	}
	return strings.TrimSpace(branch + " " + strings.Join(marks, " "))
}

func (p *page) envsOf(r *shown) []observer.Environment {
	var out []observer.Environment
	for _, e := range r.v.Envs {
		if p.grant.Scope.Environment(e.ID) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b observer.Environment) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (p *page) agentsIn(r *shown, env string) []agentRow {
	var out []agentRow
	for _, row := range p.agents([]*shown{r}, "", "") {
		if row.a.EnvironmentID == env {
			out = append(out, row)
		}
	}
	return out
}

func activityList(p *page, r *shown, env, pane string) g.Node {
	var items []Activity
	for i := len(r.v.Activity) - 1; i >= 0 && len(items) < 50; i-- {
		a := r.v.Activity[i]
		if (env != "" && a.Environment != env) || (pane != "" && a.Pane != pane) {
			continue
		}
		if !p.sees(observer.Target{Environment: a.Environment, Pane: a.Pane}) {
			continue
		}
		items = append(items, a)
	}
	return h.Section(h.Aria("labelledby", "activity-h"),
		h.H2(h.ID("activity-h"), g.Text("Activity")),
		g.If(len(items) == 0, h.P(h.Class("empty"), g.Text("Nothing has happened since this page was opened."))),
		h.Ol(h.Class("activity"), g.Map(items, func(a Activity) g.Node {
			return h.Li(h.Class("act-"+a.Kind), timeEl(a.Time, p.now), g.Text(" "+a.Text))
		})),
	)
}

// --- environment ---

func environmentPage(p *page, r *shown, env string) g.Node {
	return layout(p, env,
		h.Nav(h.Class("crumbs"), h.Aria("label", "Breadcrumb"),
			h.A(h.Href("/"), g.Text("Agents")), g.Text(" › "), h.A(h.Href("/r/"+r.rt.Name), g.Text(r.rt.Name)), g.Text(" › "+env)),
		h.H1(g.Text(env)),
		g.Group(environmentFragments(p, r, env)),
	)
}

func environmentFragments(p *page, r *shown, env string) []g.Node {
	e, ok := r.v.Envs[env]
	var tabs []observer.Tab
	for _, t := range r.v.Tabs {
		if t.EnvironmentID == env {
			tabs = append(tabs, t)
		}
	}
	slices.SortFunc(tabs, func(a, b observer.Tab) int { return a.CreatedAt.Compare(b.CreatedAt) })
	md, hasMeta := r.v.Meta[observer.Target{Environment: env}]
	return []g.Node{
		fragment("env-head", r.v.Version,
			g.If(!ok, h.P(h.Class("empty"), g.Text(emptyText(r, "This environment is gone.")))),
			g.If(ok, h.Dl(h.Class("facts"),
				fact("Directory", h.Code(g.Text(e.Path))),
				g.Iff(e.Worktree != nil, func() g.Node { return fact("Worktree", g.Text(e.Worktree.Branch+" of "+e.Worktree.Repo)) }),
				g.Iff(e.Git != nil, func() g.Node { return fact("Git", g.Text(gitText(e))) }),
				g.If(hasMeta, fact("Reported", metaNode(md))),
				peopleFact(r, env),
				fact("Connection", runtimeState(r, p.now)),
			)),
		),
		fragment("tabs", r.v.Version,
			h.Section(h.Aria("labelledby", "tabs-h"),
				h.H2(h.ID("tabs-h"), g.Text("Tabs and panes")),
				g.If(len(tabs) == 0, h.P(h.Class("empty"), g.Text(emptyText(r, "No tabs.")))),
				g.Map(tabs, func(t observer.Tab) g.Node {
					return h.Article(h.Class("tab"),
						h.H3(g.Text(t.Name), g.If(t.Active, h.Span(h.Class("muted"), g.Text(" (in front)")))),
						h.Ul(h.Class("panes"), g.Map(append(slices.Clone(t.Panes), t.Popups...), func(id string) g.Node {
							return paneItem(p, r, id)
						})),
					)
				}),
			),
		),
		fragment("activity", r.v.Version, activityList(p, r, env, "")),
	}
}

func paneItem(p *page, r *shown, id string) g.Node {
	pn, ok := r.v.Panes[id]
	if !ok {
		return nil
	}
	var a *observer.Agent
	for _, x := range r.v.Agents {
		if x.PaneID == id {
			a = &x
		}
	}
	return h.Li(
		h.A(h.Href(fmt.Sprintf("/r/%s/p/%s", r.rt.Name, url.PathEscape(id))), g.Text(paneLabel(pn))),
		g.Iff(pn.Process != nil, func() g.Node {
			return h.Span(h.Class("muted"), g.Text(" "+pn.Process.Program+" · "+string(pn.Process.Status)))
		}),
		g.Iff(a != nil, func() g.Node { return g.Group{g.Text(" "), stateBadge(a.State)} }),
		g.If(pn.Focused, h.Span(h.Class("muted"), g.Text(" · focused"))),
		typingNode(r, id),
	)
}

// peopleFact names who is in environment env, in a shared runtime.
func peopleFact(r *shown, env string) g.Node {
	if r.c == nil {
		return nil
	}
	p := r.c.Presence()
	if p == nil {
		return nil
	}
	var names []string
	for _, x := range p.People {
		if x.Environment == env && !slices.Contains(names, x.Name) {
			names = append(names, x.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return fact("Here", g.Text(strings.Join(names, ", ")))
}

// typingNode says who holds a pane's input, in a shared runtime.
func typingNode(r *shown, id string) g.Node {
	f, ok := r.v.Floors[id]
	if !ok {
		return nil
	}
	return h.Span(h.Class("typing"), g.Text(" · "+f.Holder+" is typing"))
}

func metaNode(md observer.Metadata) g.Node {
	return g.Group{
		g.If(md.Title != "", h.Strong(g.Text(md.Title+" "))),
		g.Map(md.Tokens, func(t metadata.Token) g.Node {
			return h.Span(h.Class("token"), g.Text(t.Key+": "+t.Text))
		}),
	}
}

// --- pane ---

func panePage(p *page, r *shown, id string, scr screenState, output *outputState) g.Node {
	title := id
	if pn, ok := r.v.Panes[id]; ok {
		title = paneLabel(pn)
	}
	return layout(p, title,
		h.Nav(h.Class("crumbs"), h.Aria("label", "Breadcrumb"),
			h.A(h.Href("/"), g.Text("Agents")), g.Text(" › "), h.A(h.Href("/r/"+r.rt.Name), g.Text(r.rt.Name)), g.Text(" › "),
			g.Iff(r.v.Panes[id].EnvironmentID != "" && p.grant.Scope.Environment(r.v.Panes[id].EnvironmentID), func() g.Node {
				env := r.v.Panes[id].EnvironmentID
				return g.Group{h.A(h.Href(fmt.Sprintf("/r/%s/e/%s", r.rt.Name, url.PathEscape(env))), g.Text(env)), g.Text(" › ")}
			}),
			g.Text(title)),
		h.H1(g.Text(title)),
		g.Group(paneFragments(p, r, id, scr)),
		outputSection(p, r, id, output),
	)
}

// screenState is a pane's screen as the stream knows it.
type screenState struct {
	screen  observer.Screen
	have    bool
	err     string
	version uint64
}

func paneFragments(p *page, r *shown, id string, scr screenState) []g.Node {
	pn, ok := r.v.Panes[id]
	var a *observer.Agent
	for _, x := range r.v.Agents {
		if x.PaneID == id {
			a = &x
		}
	}
	md, hasMeta := r.v.Meta[observer.Target{Pane: id}]
	return []g.Node{
		fragment("pane-head", r.v.Version,
			g.If(!ok, h.P(h.Class("empty"), g.Text(emptyText(r, "This pane is closed.")))),
			g.If(ok, h.Dl(h.Class("facts"),
				g.Iff(a != nil, func() g.Node {
					return g.Group{
						fact("Agent", g.Text(a.Name+" ("+a.Kind+")")),
						fact("State", g.Group{stateBadge(a.State), g.Text(" since "), timeEl(a.Since, p.now)}),
						g.If(a.Reason != "", fact("Why", g.Text(a.Reason))),
						g.If(!a.LastOutput.IsZero(), fact("Last output", timeEl(a.LastOutput, p.now))),
					}
				}),
				g.Iff(pn.Process != nil, func() g.Node {
					return fact("Program", g.Text(pn.Process.Program+" · "+string(pn.Process.Status)))
				}),
				g.If(hasMeta, fact("Reported", metaNode(md))),
				g.Iff(r.v.Floors[id].Holder != "", func() g.Node {
					return fact("Typing", g.Text(r.v.Floors[id].Holder+" holds this pane's input"))
				}),
				fact("Connection", runtimeState(r, p.now)),
			)),
			g.If(ok, followMuteControls(p, r, a, p.prefs, p.path)),
		),
		fragment("screen", scr.version, screenSection(p, r, scr)),
		fragment("activity", r.v.Version, activityList(p, r, "", id)),
	}
}

func screenSection(p *page, r *shown, scr screenState) g.Node {
	var body g.Node
	switch {
	case !p.screens(r):
		body = h.P(h.Class("empty"), g.Text("Screen contents are not shared with this link."))
	case !scr.have && scr.err != "":
		body = h.P(h.Class("empty"), g.Text("The screen is unavailable: "+scr.err))
	case !scr.have:
		body = h.P(h.Class("empty"), g.Text("Loading the screen…"))
	default:
		lines := scr.screen.Lines
		last := len(lines)
		for last > 0 && strings.TrimSpace(lines[last-1]) == "" {
			last--
		}
		stale := r.v.State != StateLive && r.v.State != StateIdle
		body = g.Group{
			h.Div(h.Class("screen-tools"), g.Attr("hidden"), h.Role("toolbar"), h.Aria("label", "Screen view"),
				h.Button(h.Type("button"), h.Data("screen", "smaller"), h.Aria("label", "Smaller text"), g.Text("A−")),
				h.Button(h.Type("button"), h.Data("screen", "larger"), h.Aria("label", "Larger text"), g.Text("A+")),
				h.Button(h.Type("button"), h.Data("screen", "fit"), g.Text("Fit")),
				h.Button(h.Type("button"), h.Data("screen", "wrap"), g.Attr("aria-pressed", "false"), g.Text("Wrap")),
				h.Button(h.Type("button"), h.Data("screen", "follow"), g.Attr("aria-pressed", "true"), g.Text("Auto-scroll")),
			),
			h.Pre(h.Class("screen"), h.TabIndex("0"), h.Data("cols", fmt.Sprint(scr.screen.Width)),
				h.Aria("label", fmt.Sprintf("Screen, %d by %d, read-only", scr.screen.Width, scr.screen.Height)),
				g.Text(strings.Join(lines[:last], "\n"))),
			g.If(stale, h.P(h.Class("stale"), g.Text("As of "), timeEl(r.v.SyncedAt, p.now), g.Text(": the runtime is not connected, so this screen is not updating."))),
			g.If(scr.err != "" && !stale, h.P(h.Class("muted"), g.Text("Not updating: "+scr.err))),
		}
	}
	return h.Section(h.Aria("labelledby", "screen-h"), h.H2(h.ID("screen-h"), g.Text("Screen")), body)
}

// outputState is history a viewer asked for.
type outputState struct {
	lines []string
	err   string
}

// historyLines is how much output history one request shows.
const historyLines = 200

func outputSection(p *page, r *shown, id string, out *outputState) g.Node {
	if !p.history(r) {
		return h.Section(h.Aria("labelledby", "output-h"), h.H2(h.ID("output-h"), g.Text("Output history")),
			h.P(h.Class("empty"), g.Text("Output history is not shared with this link.")))
	}
	return h.Section(h.Aria("labelledby", "output-h"), h.H2(h.ID("output-h"), g.Text("Output history")),
		h.P(h.Class("muted"), g.Textf("History is read only when you ask, and is not kept in this browser.")),
		h.A(h.Class("button"), h.ID("load-output"), h.Href(fmt.Sprintf("/r/%s/p/%s?output=%d#output", r.rt.Name, url.PathEscape(id), historyLines)),
			h.Data("output", fmt.Sprintf("/r/%s/p/%s/output?lines=%d", r.rt.Name, url.PathEscape(id), historyLines)),
			g.Textf("Show the last %d lines", historyLines)),
		outputFragment(out),
	)
}

func outputFragment(out *outputState) g.Node {
	switch {
	case out == nil:
		return h.Div(h.ID("output"))
	case out.err != "":
		return h.Div(h.ID("output"), h.P(h.Class("empty"), g.Text("Could not read the output: "+out.err)))
	}
	return h.Div(h.ID("output"), h.Pre(h.Class("screen output"), h.TabIndex("0"), g.Text(strings.Join(out.lines, "\n"))))
}

// --- access, sign-in, messages ---

func accessPage(p *page) g.Node {
	gr := p.grant
	scope := "everything in them"
	if !gr.Scope.All {
		scope = gr.Scope.String()
	}
	return layout(p, "Access",
		h.H1(g.Text("Your access")),
		h.Dl(h.Class("facts"),
			fact("Link", g.Text(gr.Label)),
			fact("Runtimes", g.Text(strings.Join(gr.Runtimes, ", "))),
			fact("Scope", g.Text(scope)),
			fact("Screens", g.Text(yesNo(gr.Screens))),
			fact("Output history", g.Text(yesNo(gr.History))),
			fact("Ends", g.Group{timeEl(gr.ExpiresAt, p.now), g.Text(" (" + gr.ExpiresAt.Local().Format(time.DateTime) + ")")}),
			fact("Signed in", timeEl(p.session.CreatedAt, p.now)),
		),
		h.P(g.Text("This view is read-only: it cannot type into terminals, change layouts or start and stop anything. Whoever shared it can end it at any time.")),
		h.Form(h.Method("post"), h.Action("/logout"), h.Button(h.Type("submit"), g.Text("Sign out"))),
	)
}

func loginPage(reason, next string) g.Node {
	msg := map[string]string{
		"revoked": "Your access was revoked.",
		"expired": "Your access has expired.",
		"refused": "That link is invalid, already used, or expired. Ask for a new one.",
		"out":     "You are signed out.",
	}[reason]
	return document("Sign in",
		h.Main(h.ID("main"), h.Class("login"),
			h.H1(g.Text("Hive")),
			g.If(msg != "", h.P(h.Class("notice"), h.Role("status"), g.Text(msg))),
			h.P(g.Text("Open the link you were sent, or paste its code. Access is read-only.")),
			h.Form(h.ID("login"), h.Method("post"), h.Action("/login"),
				h.Label(h.For("code"), g.Text("Access code")),
				h.Input(h.Type("password"), h.ID("code"), h.Name("code"), h.AutoComplete("off"), h.Required(), h.SpellCheck("false")),
				g.If(next != "", h.Input(h.Type("hidden"), h.Name("next"), h.Value(next))),
				h.Button(h.Type("submit"), g.Text("Sign in")),
			),
			h.P(h.Class("muted small"), g.Text("In the installed app on iPhone or iPad, paste the code: the app signs in separately from Safari.")),
		),
	)
}

func messagePage(p *page, title, text string) g.Node {
	return layout(p, title, h.H1(g.Text(title)), h.P(g.Text(text)), h.P(h.A(h.Href("/"), g.Text("All agents"))))
}
