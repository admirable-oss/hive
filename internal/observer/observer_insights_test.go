package observer

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/insights"
)

// twoAgents is one agent in dev (pane p1) and one in ops (pane p2).
type twoAgents struct{}

var observedAgents = []agent.Agent{
	{ID: "a1", PaneID: "p1", Kind: "claude", EnvironmentID: "dev", Status: agent.Status{State: agent.StateBlocked, Reason: "SECRET may I run the tests?"}},
	{ID: "a2", PaneID: "p2", Kind: "codex", EnvironmentID: "ops", Status: agent.Status{State: agent.StateBlocked, Reason: "SECRET deploy now?"}},
}

func (twoAgents) List(context.Context, bool) ([]agent.Agent, error) { return observedAgents, nil }
func (twoAgents) Get(_ context.Context, id string) (agent.Agent, error) {
	for _, a := range observedAgents {
		if a.ID == id || a.PaneID == id {
			return a, nil
		}
	}
	return agent.Agent{}, errors.New("no such agent")
}

// TestInsightsAreScoped: observe.insights counts only what the credential
// sees (and what Within narrows it to), shows only the budgets whose keys
// it sees, and asks' reasons need screens.
func TestInsightsAreScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ins := insights.NewService(insights.Config{Budgets: []insights.Budget{
		{Name: "daily", Per: insights.PerDay, Limits: insights.Limits{CostUSD: 10}},
		{Name: "per-env", Per: insights.PerEnvironment, Limits: insights.Limits{CostUSD: 1}},
		{Name: "ops-only", Per: insights.PerDay, Environment: "ops", Limits: insights.Limits{CostUSD: 1}},
	}}, insights.NewStore(t.TempDir()), insights.Ports{Agents: twoAgents{}})
	if err := ins.Load(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p1", "p2"} {
		cost := 0.5
		if _, err := ins.ReportUsage(ctx, insights.UsageReport{ID: id, CostUSD: &cost}); err != nil {
			t.Fatal(err)
		}
	}
	s, store := newTestServer(t)
	s.cfg.Sources.Insights = ins

	connect := func(req CreateRequest) *Client {
		t.Helper()
		_, token, err := store.Create(req)
		if err != nil {
			t.Fatal(err)
		}
		c, err := Connect(ctx, ClientConfig{Dial: serve(t, s), Token: token})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	kinds := func(in Insights) []string {
		var out []string
		for _, r := range in.Summary.Rows {
			out = append(out, r.Key)
		}
		return out
	}
	budgets := func(in Insights) []string {
		var out []string
		for _, b := range in.Budgets {
			out = append(out, b.Budget.Name+":"+b.Key)
		}
		return out
	}

	dev := connect(CreateRequest{Name: "dev", Scope: Scope{Environments: []string{"dev"}}, TTL: MinTTL})
	got, err := dev.Insights(ctx, InsightsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(got); !slices.Equal(k, []string{"claude"}) || got.Summary.Total.CostUSD == nil || *got.Summary.Total.CostUSD != 0.5 {
		t.Fatalf("a dev credential counts only dev: %v %+v", k, got.Summary.Total)
	}
	if b := budgets(got); !slices.Equal(b, []string{"per-env:dev"}) {
		t.Fatalf("a dev credential sees only dev's budget keys: %v", b)
	}
	if len(got.Asks) != 1 || got.Asks[0].Env != "dev" || got.Asks[0].Reason != "" {
		t.Fatalf("asks without screens have no reasons: %+v", got.Asks)
	}
	if data, _ := json.Marshal(got); strings.Contains(string(data), "SECRET") {
		t.Fatalf("what agents asked leaks without screens: %s", data)
	}

	all := connect(CreateRequest{Name: "all", Scope: Scope{All: true}, Screens: true, TTL: MinTTL})
	got, err = all.Insights(ctx, InsightsRequest{By: insights.ByEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(got); !slices.Equal(k, []string{"dev", "ops"}) {
		t.Fatalf("all sees every environment: %v", k)
	}
	if b := budgets(got); !slices.Equal(b, []string{"daily:today", "per-env:dev", "per-env:ops", "ops-only:today"}) {
		t.Fatalf("all sees every budget: %v", b)
	}
	if len(got.Asks) != 2 || !strings.HasPrefix(got.Asks[0].Reason, "SECRET") {
		t.Fatalf("screens show what was asked: %+v", got.Asks)
	}

	// Within narrows a credential's view (a web page's link), never widens it.
	got, err = all.Insights(ctx, InsightsRequest{Within: &Scope{Panes: []string{"p2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(got); !slices.Equal(k, []string{"codex"}) {
		t.Fatalf("within pane p2: %v", k)
	}
	if b := budgets(got); !slices.Equal(b, nil) {
		t.Fatalf("a pane sees no environment's budgets: %v", b)
	}
	got, err = dev.Insights(ctx, InsightsRequest{Within: &Scope{All: true}})
	if err != nil || !slices.Equal(kinds(got), []string{"claude"}) {
		t.Fatalf("within all, dev still sees only dev: %v %v", kinds(got), err)
	}
	if _, err := dev.Insights(ctx, InsightsRequest{By: "colour"}); code(err) != "invalid_params" {
		t.Fatalf("a bad grouping: %v", err)
	}
}

func TestBudgetAlertsReachObserversInScope(t *testing.T) {
	alert := func(a insights.Alert) Event {
		raw, _ := json.Marshal(a)
		ev, ok := Translate(event.Event{Seq: 1, Type: insights.EventAlert, Data: raw}, fakeLocator{})
		if !ok {
			t.Fatal("alerts reach observers")
		}
		return ev
	}
	dev, all := Scope{Environments: []string{"dev"}}, Scope{All: true}
	if ev := alert(insights.Alert{Budget: "daily", Per: insights.PerDay, Key: "today"}); ev.Visible(dev) || !ev.Visible(all) {
		t.Fatal("a day's budget spans environments: only for all")
	}
	if ev := alert(insights.Alert{Budget: "e", Per: insights.PerEnvironment, Key: "dev"}); !ev.Visible(dev) {
		t.Fatal("an environment's budget is seen in it")
	}
	if ev := alert(insights.Alert{Budget: "o", Per: insights.PerKind, Key: "codex", Environment: "ops"}); ev.Visible(dev) {
		t.Fatal("a budget of ops is not seen in dev")
	}
}
