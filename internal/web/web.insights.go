package web

import (
	"fmt"
	"strings"
	"time"

	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"

	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/observer"
)

// Insights (M25) on a runtime's page: today's time in each state and the
// usage agents reported, by kind, of what the page's link sees; the budgets
// near or over their limits; and the latest times agents waited on someone
// (what they asked only for links with screens). Read through
// observe.insights, so a credential sees only its scope.

// insightsOf reads a runtime's insights as the page may show them: within
// the link's scope, and without what agents asked unless it has screens.
func (p *page) insightsOf(r *shown) *observer.Insights {
	if r.c == nil || !r.v.Loaded() {
		return nil
	}
	var within *observer.Scope
	if !p.grant.Scope.All {
		s := p.grant.Scope
		within = &s
	}
	in := r.c.Insights(within)
	if in == nil {
		return nil
	}
	out := *in
	if !p.screens(r) {
		out.Asks = append([]insights.Ask(nil), in.Asks...)
		for i := range out.Asks {
			out.Asks[i].Reason = ""
		}
	}
	return &out
}

// insightsSection is a runtime's insights for today; nothing for a
// runtime that keeps none, or before anything was recorded.
func insightsSection(p *page, r *shown) g.Node {
	in := p.insightsOf(r)
	if in == nil || len(in.Summary.Rows) == 0 && len(in.Budgets) == 0 {
		return g.Group(nil)
	}
	var near []insights.BudgetStatus
	for _, b := range in.Budgets {
		if b.Level != "" || b.Held {
			near = append(near, b)
		}
	}
	return h.Section(h.Aria("labelledby", "insights-h"),
		h.H2(h.ID("insights-h"), g.Text("Insights today")),
		g.If(len(in.Summary.Rows) > 0, h.Table(h.Class("agents insights"),
			h.Caption(h.Class("sr"), g.Text("Time in each state and reported usage, by agent kind")),
			h.THead(h.Tr(
				h.Th(h.Scope("col"), g.Text("Kind")), h.Th(h.Scope("col"), g.Text("Working")),
				h.Th(h.Scope("col"), g.Text("Blocked")), h.Th(h.Scope("col"), g.Text("Idle")),
				h.Th(h.Scope("col"), g.Text("Waiting on you")), h.Th(h.Scope("col"), g.Text("Tokens")),
				h.Th(h.Scope("col"), g.Text("Cost")), h.Th(h.Scope("col"), g.Text("Source")),
			)),
			h.TBody(g.Map(in.Summary.Rows, func(row insights.Row) g.Node {
				waiting := msShort(row.WaitingMS)
				if row.Asks > 0 {
					waiting += fmt.Sprintf(" (%d)", row.Asks)
				}
				return h.Tr(
					h.Th(h.Scope("row"), g.Text(row.Key)),
					h.Td(g.Text(msShort(row.StatesMS[insights.StateWorking]))),
					h.Td(g.Text(msShort(row.StatesMS[insights.StateBlocked]))),
					h.Td(g.Text(msShort(row.StatesMS[insights.StateIdle]+row.StatesMS[insights.StateDone]))),
					h.Td(g.Text(waiting)),
					h.Td(g.Text(tokensShort(row.Tokens))),
					h.Td(g.Text(costShort(row.CostUSD))),
					h.Td(h.Class("muted"), g.Text(cmpText(strings.Join(row.Sources, ", "), "-"))),
				)
			})),
		)),
		g.If(len(near) > 0, h.Ul(h.Class("budgets"), g.Map(near, func(b insights.BudgetStatus) g.Node {
			name := b.Budget.Name
			if b.Budget.Per != insights.PerDay {
				name += " · " + b.Key
			}
			text := fmt.Sprintf("%.0f%% %s", b.Fraction*100, b.Level)
			if b.Held {
				text += " · holding new tasks"
			}
			return h.Li(h.Class("budget budget-"+b.Level), h.Strong(g.Text("Budget "+name)), g.Text(" "+text))
		}))),
		g.If(len(in.Asks) > 0, h.Ul(h.Class("asks"), g.Map(in.Asks, func(a insights.Ask) g.Node {
			who := cmpText(a.Name, cmpText(a.Kind, "an agent"))
			waited := "waited " + msShort(a.WaitedMS)
			if a.Open {
				waited = "waiting " + msShort(a.WaitedMS)
			}
			return h.Li(
				timeEl(a.Start, p.now), g.Text(" "+who+" in "+a.Env+" "+waited),
				g.If(a.Reason != "", h.Span(h.Class("why"), g.Text(": "+truncate(a.Reason, 120)))),
			)
		}))),
	)
}

func cmpText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// msShort is milliseconds, roughly: 1h 20m, 12m, 45s, 0.
func msShort(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d <= 0:
		return "0"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

func tokensShort(t *insights.Tokens) string {
	if t == nil {
		return "unknown"
	}
	n := t.Total()
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e3), ".0") + "k"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
}

func costShort(c *float64) string {
	if c == nil {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f", *c)
}
