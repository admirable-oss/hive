package hiveapi

import (
	"context"

	"github.com/admirable-oss/hive/internal/insights"
)

// Insights: where agents' time went (time in each state, waiting on a
// person and what they asked) and what they used (tokens and cost, where
// an agent reported them), kept on the daemon's machine; budgets that warn
// and can hold new tasks (`hive insights`).
//
//	sum, err := c.InsightsSummary(ctx, hiveapi.InsightsQuery{By: hiveapi.InsightsByKind})
//	for _, row := range sum.Rows {
//		fmt.Println(row.Key, row.StatesMS["working"], row.CostUSD) // CostUSD is nil when unknown
//	}
type (
	InsightsQuery   = insights.QueryParams
	InsightsSummary = insights.Summary
	InsightsRow     = insights.Row
	InsightsTokens  = insights.Tokens
	InsightsWho     = insights.Who
	InsightsSpan    = insights.Span
	InsightsUsage   = insights.Usage
	InsightsRecord  = insights.Record
	Ask             = insights.Ask
	UsageReport     = insights.UsageParams
	UsageResult     = insights.UsageResult
	Budget          = insights.Budget
	BudgetLimits    = insights.Limits
	BudgetAmounts   = insights.Amounts
	BudgetStatus    = insights.BudgetStatus
	BudgetAlert     = insights.Alert
)

// Summary groupings (InsightsQuery.By).
const (
	InsightsByKind        = insights.ByKind
	InsightsByAgent       = insights.ByAgent
	InsightsByEnvironment = insights.ByEnvironment
	InsightsByTask        = insights.ByTask
	InsightsByDay         = insights.ByDay
	InsightsByModel       = insights.ByModel
)

// What ExportInsights exports.
const (
	InsightsSpans  = insights.RecordSpan
	InsightsUsages = insights.RecordUsage
	InsightsAlerts = insights.RecordAlert
)

// Where usage numbers come from (UsageReport.Source; Row.Sources).
const (
	UsageFromTranscript = insights.SourceTranscript
	UsageFromStatusline = insights.SourceStatusline
	UsageFromReport     = insights.SourceReport
)

// Budget alert levels (BudgetAlert.Level, BudgetStatus.Level).
const (
	BudgetWarning       = insights.LevelWarning
	BudgetExceeded      = insights.LevelExceeded
	BudgetUnenforceable = insights.LevelUnenforceable
)

// Insights events (see Events).
const (
	EventInsightsAlert   = insights.EventAlert
	EventInsightsResumed = insights.EventResumed
)

// InsightsSummary is time in each state and usage over a range (today by
// default), grouped by kind unless q says.
func (c *Client) InsightsSummary(ctx context.Context, q InsightsQuery) (InsightsSummary, error) {
	return c.ins.Summary(ctx, q)
}

// Asks lists the times agents waited on a person, newest first, with what
// they asked; at most limit when positive.
func (c *Client) Asks(ctx context.Context, q InsightsQuery, limit int) ([]Ask, error) {
	return c.ins.Asks(ctx, q, limit)
}

// ReportUsage records an agent's usage: its session's totals so far
// (r.Total), or an increment.
func (c *Client) ReportUsage(ctx context.Context, r UsageReport) (UsageResult, error) {
	return c.ins.ReportUsage(ctx, r)
}

// Budgets is how each budget stands, per key of its window.
func (c *Client) Budgets(ctx context.Context) ([]BudgetStatus, error) { return c.ins.Budgets(ctx) }

// ResumeBudget lets new tasks start again under a budget that holds them,
// until its window ends: for one key, or every key it holds when key is "".
func (c *Client) ResumeBudget(ctx context.Context, budget, key string) ([]BudgetStatus, error) {
	return c.ins.Resume(ctx, budget, key)
}

// ExportInsights returns a range's records: InsightsSpans, InsightsUsages
// or InsightsAlerts.
func (c *Client) ExportInsights(ctx context.Context, q InsightsQuery, what string) ([]InsightsRecord, error) {
	return c.ins.Export(ctx, q, what)
}
