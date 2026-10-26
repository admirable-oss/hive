package main

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/config"
	"github.com/admirable-oss/hive/internal/insights"
)

// TestInsightsConfig: [insights] reaches the daemon, its budgets in
// package insights' terms, and config accepts what insights knows.
func TestInsightsConfig(t *testing.T) {
	if !slices.Equal(config.BudgetPers, insights.Pers) || !slices.Equal(config.BudgetActions, []string{insights.ActionWarn, insights.ActionPause}) {
		t.Fatalf("config's budgets differ from insights': %v %v", config.BudgetPers, config.BudgetActions)
	}
	a := newTestApp(t)
	a.cfg.Insights = config.Insights{Budgets: []config.Budget{
		{Name: "daily", Per: "day", Cost: 25, Tokens: 100, Working: 10 * time.Hour, WarnAt: 0.8, Action: "pause"},
	}}
	a.cfg.Retention.InsightsDays = 30
	got := a.insightsConfig()
	want := []insights.Budget{{
		Name: "daily", Per: insights.PerDay,
		Limits: insights.Limits{CostUSD: 25, Tokens: 100, WorkingMS: (10 * time.Hour).Milliseconds()},
		WarnAt: 0.8, Action: insights.ActionPause,
	}}
	if !got.Disabled || got.KeepDays != 30 || !reflect.DeepEqual(got.Budgets, want) {
		t.Fatalf("insights config = %+v", got)
	}
}
