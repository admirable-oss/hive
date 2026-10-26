package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/insights"
)

// Insights is the typed insights API (insights.*) over any Client.
type Insights struct {
	c Client
}

// NewInsights wraps c.
func NewInsights(c Client) Insights { return Insights{c: c} }

func insightsCall[R any](ctx context.Context, in Insights, method string, params any) (R, error) {
	var r R
	err := in.c.Call(ctx, method, params, &r)
	return r, err
}

// Summary is time and usage over a range, by a grouping.
func (in Insights) Summary(ctx context.Context, q insights.QueryParams) (insights.Summary, error) {
	return insightsCall[insights.Summary](ctx, in, "insights.summary", q)
}

// Asks lists the times agents waited on a person, newest first; at most
// limit when positive.
func (in Insights) Asks(ctx context.Context, q insights.QueryParams, limit int) ([]insights.Ask, error) {
	return insightsCall[[]insights.Ask](ctx, in, "insights.asks", insights.AsksParams{QueryParams: q, Limit: limit})
}

// ReportUsage records an agent's usage.
func (in Insights) ReportUsage(ctx context.Context, p insights.UsageParams) (insights.UsageResult, error) {
	return insightsCall[insights.UsageResult](ctx, in, "insights.usage", p)
}

// Budgets is how each budget stands, per key of its window.
func (in Insights) Budgets(ctx context.Context) ([]insights.BudgetStatus, error) {
	return insightsCall[[]insights.BudgetStatus](ctx, in, "insights.budgets", struct{}{})
}

// Resume lifts a budget's hold on new work until its window ends: of one
// key, or of every held one when key is "".
func (in Insights) Resume(ctx context.Context, budget, key string) ([]insights.BudgetStatus, error) {
	return insightsCall[[]insights.BudgetStatus](ctx, in, "insights.resume", insights.ResumeParams{Budget: budget, Key: key})
}

// Export returns the records of a range: spans, usage or alerts.
func (in Insights) Export(ctx context.Context, q insights.QueryParams, what string) ([]insights.Record, error) {
	return insightsCall[[]insights.Record](ctx, in, "insights.export", insights.ExportParams{QueryParams: q, What: what})
}
