package core

import (
	"encoding/json"
	"testing"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func TestInsightsViewModel(t *testing.T) {
	cost := 1.5
	sum := hiveapi.InsightsSummary{
		By: "kind",
		Rows: []hiveapi.InsightsRow{
			{
				Key: "claude", Agents: 2, StatesMS: map[string]int64{"working": 60000, "blocked": 5000, "idle": 1000, "done": 2000}, WaitingMS: 5000, Asks: 1,
				Tokens: &hiveapi.InsightsTokens{Input: 100, Output: 20, CacheRead: 5}, CostUSD: &cost, Sources: []string{"transcript"},
			},
			{Key: "codex", Agents: 1, StatesMS: map[string]int64{"working": 1000}},
		},
	}
	asks := []hiveapi.Ask{{Who: hiveapi.InsightsWho{Agent: "a1", Kind: "claude", Name: "Claude Code", Env: "api", Plan: "p1", Task: "fix"}, State: "blocked", Reason: "run the tests?", WaitedMS: 5000}}
	budgets := []hiveapi.BudgetStatus{{
		Budget: hiveapi.Budget{Name: "daily", Per: "day", Limits: hiveapi.BudgetLimits{CostUSD: 1}, Action: "pause"},
		Key:    "today", Used: hiveapi.BudgetAmounts{CostUSD: 1.5, CostKnown: true}, Fraction: 1.5, Level: "exceeded", Held: true,
	}}
	in := insightsOf(sum, asks, budgets)
	c := in.Rows[0]
	if c.WorkingMS != 60000 || c.BlockedMS != 5000 || c.IdleMS != 3000 || c.Tokens == nil || *c.Tokens != 125 || *c.CostUSD != 1.5 {
		t.Fatalf("claude: %+v", c)
	}
	if x := in.Rows[1]; x.Tokens != nil || x.CostUSD != nil || x.Sources == nil {
		t.Fatalf("codex reported nothing, so its usage is unknown: %+v", x)
	}
	if a := in.Asks[0]; a.Who != "Claude Code" || a.Task != "p1/fix" || a.Reason != "run the tests?" {
		t.Fatalf("ask: %+v", a)
	}
	if b := in.Budgets[0]; b.Used != "$1.50 of $1.00" || !b.Held || b.Action != "pause" {
		t.Fatalf("budget: %+v", b)
	}
	// Empty lists stay lists for the frontend.
	if e := insightsOf(hiveapi.InsightsSummary{}, nil, nil); e.Rows == nil || e.Asks == nil || e.Budgets == nil {
		t.Fatalf("empty: %+v", e)
	}
}

func TestBudgetAlertsNotify(t *testing.T) {
	h := &Hub{settings: DefaultSettings()}
	raw, _ := json.Marshal(hiveapi.BudgetAlert{Budget: "daily", Key: "today", Window: "2026-10-25", Level: "exceeded", Message: "budget daily is exceeded"})
	n, ok := h.alertNotification(raw)
	if !ok || n.Title != "Budget daily exceeded" || n.Body != "budget daily is exceeded" {
		t.Fatalf("alert: %+v %v", n, ok)
	}
	h.settings.SystemNotifications = true
	if _, ok := h.alertNotification(raw); ok {
		t.Fatal("the daemon's own notifications are on: the app does not repeat them")
	}
}
