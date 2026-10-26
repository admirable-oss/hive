package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/notify"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// Insights (hive insights) show in the Overview under the workflows:
// today's time in each state and usage by kind, the time agents waited on
// you, and the budgets near or over their limits. Time grows without
// events, so they are read when the Overview opens, every
// insightsEvery while it is open, and when a budget alerts.

// insightsEvery is how often an open Overview reads insights again.
const insightsEvery = 15 * time.Second

// insightsView is what the Overview shows of insights; nil from a daemon
// without them.
type insightsView struct {
	sum     insights.Summary
	budgets []insights.BudgetStatus
}

type insightsMsg struct {
	gen  int
	view *insightsView
}

// insightsTickMsg reads insights again while the Overview is open.
type insightsTickMsg struct{ gen int }

// loadInsights reads today's summary by kind and the budgets. A daemon
// without insights (older than M25) has none, which is not an error.
func loadInsights(ctx context.Context, c client.Client) *insightsView {
	in := client.NewInsights(c)
	sum, err := in.Summary(ctx, insights.QueryParams{By: insights.ByKind})
	if err != nil {
		return nil
	}
	budgets, _ := in.Budgets(ctx)
	return &insightsView{sum: sum, budgets: budgets}
}

// readInsights reads insights in the background.
func (a *App) readInsights() {
	c, gen := a.c, a.gen
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		return insightsMsg{gen: gen, view: loadInsights(ctx, c)}
	})
}

func (a *App) insightsLoaded(m insightsMsg) {
	if m.gen != a.gen {
		return
	}
	a.ws.insights = m.view
	a.dirty = true
}

// insightsTick reads insights again, and schedules the next read, while
// the Overview is open.
func (a *App) insightsTick(m insightsTickMsg) {
	if a.ui.overview == nil || m.gen != a.gen {
		a.ui.insightsTicking = false
		return
	}
	a.readInsights()
	a.after(insightsEvery, insightsTickMsg{gen: a.gen})
}

// followInsights starts reading insights for the Overview.
func (a *App) followInsights() {
	a.readInsights()
	if !a.ui.insightsTicking {
		a.ui.insightsTicking = true
		a.after(insightsEvery, insightsTickMsg{gen: a.gen})
	}
}

// applyInsightsEvent raises a budget's alert, and reads insights again
// when the Overview shows them.
func (a *App) applyInsightsEvent(ev event.Event) {
	if ev.Type == insights.EventAlert {
		var al insights.Alert
		if json.Unmarshal(ev.Data, &al) == nil {
			kind := compositor.ToastWarning
			if al.Level == insights.LevelExceeded {
				kind = compositor.ToastError
			}
			a.raise(notify.Note{Title: "budget " + al.Budget, Body: al.Message}, kind, al.Message)
		}
	}
	if a.ui.overview != nil {
		a.readInsights()
	}
}

// insightsLines are the Overview's insights rows: each kind's time and
// usage today, the waiting, and the budgets near their limits.
func (a *App) insightsLines(width int) []compositor.Spans {
	v := a.ws.insights
	if v == nil || len(v.sum.Rows) == 0 && len(v.budgets) == 0 {
		return nil
	}
	muted := uv.Style{Fg: a.theme.Muted}
	lines := []compositor.Spans{{}, {{Text: "  INSIGHTS · today", Style: muted}}}
	for _, r := range v.sum.Rows {
		lines = append(lines, compositor.Spans{
			{Text: fmt.Sprintf("  %-10s ", compositor.Truncate(r.Key, 10))},
			{Text: "work " + durText(r.StatesMS[insights.StateWorking]), Style: uv.Style{Fg: a.theme.Info}},
			{Text: "  blocked " + durText(r.StatesMS[insights.StateBlocked]), Style: uv.Style{Fg: a.theme.Warning}},
			{Text: "  idle " + durText(r.StatesMS[insights.StateIdle]+r.StatesMS[insights.StateDone]), Style: muted},
		}, compositor.Spans{
			{Text: "             " + compositor.Truncate(usageLine(r), max(width-14, 8)), Style: muted},
		})
	}
	if t := v.sum.Total; t.WaitingMS > 0 {
		lines = append(lines, compositor.Spans{{
			Text:  fmt.Sprintf("  waiting on you %s · %d %s", durText(t.WaitingMS), t.Asks, map[bool]string{true: "ask", false: "asks"}[t.Asks == 1]),
			Style: uv.Style{Fg: a.theme.Warning},
		}})
	}
	for _, b := range v.budgets {
		if b.Level == "" && !b.Held {
			continue
		}
		c := a.theme.Warning
		if b.Level == insights.LevelExceeded {
			c = a.theme.Error
		}
		name := b.Budget.Name
		if b.Budget.Per != insights.PerDay {
			name += " · " + b.Key
		}
		text := fmt.Sprintf("  ⚠ %s %.0f%% %s", name, b.Fraction*100, b.Level)
		switch {
		case b.Held:
			text += " · holding new tasks"
		case b.Resumed:
			text += " · resumed"
		}
		lines = append(lines, compositor.Spans{{Text: compositor.Truncate(text, max(width-2, 8)), Style: uv.Style{Fg: c}}})
	}
	return lines
}

// usageLine is a row's tokens and cost, "unknown" for what nothing
// reported.
func usageLine(r insights.Row) string {
	tokens, cost := "tokens unknown", "cost unknown"
	if r.Tokens != nil {
		tokens = countText(r.Tokens.Total()) + " tokens"
	}
	if r.CostUSD != nil {
		cost = fmt.Sprintf("$%.2f", *r.CostUSD)
	}
	text := tokens + " · " + cost
	if len(r.Sources) > 0 {
		text += " (" + strings.Join(r.Sources, ", ") + ")"
	}
	return text
}

// durText is a duration in milliseconds, roughly: 1h20m, 12m, 45s, 0.
func durText(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d <= 0:
		return "0"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// countText is 950, 12.3k or 4.5M.
func countText(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e3), ".0") + "k"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
}
