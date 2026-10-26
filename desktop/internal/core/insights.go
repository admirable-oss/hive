package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Insights is a machine's time and usage today, as the Insights window
// shows it (hive insights).
type Insights struct {
	// Machine is the saved machine they are of ("" for this computer).
	Machine string         `json:"machine,omitempty"`
	From    time.Time      `json:"from"`
	To      time.Time      `json:"to"`
	Rows    []InsightsRow  `json:"rows"`
	Total   InsightsRow    `json:"total"`
	Asks    []Ask          `json:"asks"`
	Budgets []BudgetStatus `json:"budgets"`
}

// InsightsRow is one agent kind's time and usage.
type InsightsRow struct {
	Key       string `json:"key"`
	Agents    int    `json:"agents"`
	WorkingMS int64  `json:"working_ms"`
	BlockedMS int64  `json:"blocked_ms"`
	// IdleMS counts idle and done: ready for a prompt either way.
	IdleMS    int64 `json:"idle_ms"`
	WaitingMS int64 `json:"waiting_ms"`
	Asks      int   `json:"asks"`
	// Tokens and CostUSD are nil when nothing reported them: unknown.
	Tokens  *int64   `json:"tokens"`
	CostUSD *float64 `json:"cost_usd"`
	Sources []string `json:"sources"`
}

// Ask is a time an agent waited on a person.
type Ask struct {
	Who         string    `json:"who"`
	Kind        string    `json:"kind"`
	Environment string    `json:"environment"`
	Task        string    `json:"task,omitempty"`
	Reason      string    `json:"reason"`
	Start       time.Time `json:"start"`
	WaitedMS    int64     `json:"waited_ms"`
	Open        bool      `json:"open"`
}

// BudgetStatus is how a budget stands for one key.
type BudgetStatus struct {
	Name   string `json:"name"`
	Per    string `json:"per"`
	Key    string `json:"key"`
	Window string `json:"window"`
	// Used says what it used of each limit: "$21.30 of $25.00".
	Used     string  `json:"used"`
	Fraction float64 `json:"fraction"`
	Level    string  `json:"level"`
	Action   string  `json:"action"`
	Held     bool    `json:"held"`
	Resumed  bool    `json:"resumed"`
}

// insightsAsks is how many of the latest asks the window shows.
const insightsAsks = 50

func insightsOf(sum hiveapi.InsightsSummary, asks []hiveapi.Ask, budgets []hiveapi.BudgetStatus) Insights {
	out := Insights{From: sum.From, To: sum.To, Rows: []InsightsRow{}, Total: insightsRowOf(sum.Total), Asks: []Ask{}, Budgets: []BudgetStatus{}}
	for _, r := range sum.Rows {
		out.Rows = append(out.Rows, insightsRowOf(r))
	}
	for _, a := range asks {
		who := cmp(a.Name, cmp(a.Kind, a.Agent))
		task := ""
		if a.Task != "" {
			task = a.Plan + "/" + a.Task
		}
		out.Asks = append(out.Asks, Ask{
			Who: who, Kind: a.Kind, Environment: a.Env, Task: task, Reason: a.Reason, Start: a.Start, WaitedMS: a.WaitedMS, Open: a.Open,
		})
	}
	for _, b := range budgets {
		out.Budgets = append(out.Budgets, BudgetStatus{
			Name: b.Budget.Name, Per: b.Budget.Per, Key: b.Key, Window: b.Window, Used: usedText(b.Budget.Limits, b.Used),
			Fraction: b.Fraction, Level: b.Level, Action: cmp(b.Budget.Action, "warn"), Held: b.Held, Resumed: b.Resumed,
		})
	}
	return out
}

func insightsRowOf(r hiveapi.InsightsRow) InsightsRow {
	row := InsightsRow{
		Key: r.Key, Agents: r.Agents, WorkingMS: r.StatesMS["working"], BlockedMS: r.StatesMS["blocked"],
		IdleMS: r.StatesMS["idle"] + r.StatesMS["done"], WaitingMS: r.WaitingMS, Asks: r.Asks, CostUSD: r.CostUSD,
		Sources: nonNil(r.Sources),
	}
	if r.Tokens != nil {
		n := r.Tokens.Total()
		row.Tokens = &n
	}
	return row
}

// usedText is each limit's use: "$21.30 of $25.00 · 3.1M of 20M tokens".
func usedText(l hiveapi.BudgetLimits, u hiveapi.BudgetAmounts) string {
	var parts []string
	if l.CostUSD > 0 {
		used := "unknown"
		if u.CostKnown {
			used = fmt.Sprintf("$%.2f", u.CostUSD)
		}
		parts = append(parts, fmt.Sprintf("%s of $%.2f", used, l.CostUSD))
	}
	if l.Tokens > 0 {
		used := "unknown"
		if u.TokensKnown {
			used = fmt.Sprint(u.Tokens)
		}
		parts = append(parts, fmt.Sprintf("%s of %d tokens", used, l.Tokens))
	}
	if l.WorkingMS > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s working", time.Duration(u.WorkingMS)*time.Millisecond, time.Duration(l.WorkingMS)*time.Millisecond))
	}
	return strings.Join(parts, " · ")
}

// Insights reads today's insights by kind, the latest asks and the
// budgets.
func (h *Hub) Insights() (Insights, error) {
	var out Insights
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		sum, err := c.InsightsSummary(ctx, hiveapi.InsightsQuery{By: hiveapi.InsightsByKind})
		if err != nil {
			return fmt.Errorf("read insights: %w", err)
		}
		asks, err := c.Asks(ctx, hiveapi.InsightsQuery{}, insightsAsks)
		if err != nil {
			return fmt.Errorf("read insights: %w", err)
		}
		budgets, err := c.Budgets(ctx)
		if err != nil {
			return fmt.Errorf("read budgets: %w", err)
		}
		out = insightsOf(sum, asks, budgets)
		return nil
	})
	return out, err
}

// ResumeBudget lets new tasks start again under a budget that holds them,
// until its window ends: for one key, or every key it holds when key is "".
// A person does this; the app only ever calls it on a click.
func (h *Hub) ResumeBudget(name, key string) error {
	if name == "" {
		return fmt.Errorf("resume: name the budget")
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		if _, err := c.ResumeBudget(ctx, name, key); err != nil {
			return fmt.Errorf("resume budget %s: %w", name, err)
		}
		return nil
	})
}

// alertNotification is a budget alert as the app announces it; ok is false
// for one it does not (the daemon's own notifications are on, or it could
// not be read).
func (h *Hub) alertNotification(data json.RawMessage) (Notification, bool) {
	var a hiveapi.BudgetAlert
	if json.Unmarshal(data, &a) != nil || a.Budget == "" {
		return Notification{}, false
	}
	h.mu.Lock()
	system := h.settings.SystemNotifications
	h.mu.Unlock()
	if system {
		return Notification{}, false
	}
	title := "Budget " + a.Budget
	if a.Level == hiveapi.BudgetExceeded {
		title += " exceeded"
	}
	return Notification{ID: "budget:" + a.Budget + ":" + a.Key + ":" + a.Window + ":" + a.Level, Title: title, Body: a.Message}, true
}
