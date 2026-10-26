package insights

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

// QueryParams select records over the wire.
type QueryParams struct {
	From        time.Time `json:"from,omitzero" desc:"from this time (default: the start of today, local time)"`
	To          time.Time `json:"to,omitzero" desc:"until this time (default: now)"`
	By          string    `json:"by,omitempty" desc:"group rows by kind (default), agent, environment, task, day or model"`
	Kind        string    `json:"kind,omitempty" desc:"only this agent kind"`
	Environment string    `json:"environment,omitempty" desc:"only this environment"`
}

func (p QueryParams) query() Query {
	return Query{From: p.From, To: p.To, By: p.By, Kind: p.Kind, Environment: p.Environment}
}

// AsksParams is insights.asks's request.
type AsksParams struct {
	QueryParams
	Limit int `json:"limit,omitempty" desc:"at most this many (default and most 1000)"`
}

// UsageParams is insights.usage's request: an agent's usage, as its
// session's totals so far (total) or an increment.
type UsageParams struct {
	ID         string   `json:"id" desc:"the agent's pane or process ID"`
	Session    string   `json:"session,omitempty" desc:"the agent's session (default: the agent): totals are per session"`
	Model      string   `json:"model,omitempty" desc:"the model the usage is for"`
	Source     string   `json:"source,omitempty" desc:"where the numbers come from: report (default), transcript, statusline, or a name of the reporter's"`
	Input      *int64   `json:"input,omitempty" desc:"input tokens (not cached)"`
	Output     *int64   `json:"output,omitempty" desc:"output tokens"`
	CacheRead  *int64   `json:"cache_read,omitempty" desc:"input tokens read from a cache"`
	CacheWrite *int64   `json:"cache_write,omitempty" desc:"input tokens written to a cache"`
	CostUSD    *float64 `json:"cost_usd,omitempty" desc:"cost in US dollars, as the agent or provider stated it"`
	Total      bool     `json:"total,omitempty" desc:"the numbers are the session's totals so far (what grew is recorded), not an increment"`
}

func (p UsageParams) report() UsageReport {
	r := UsageReport{ID: p.ID, Session: p.Session, Model: p.Model, Source: p.Source, CostUSD: p.CostUSD, Total: p.Total}
	if p.Input != nil || p.Output != nil || p.CacheRead != nil || p.CacheWrite != nil {
		val := func(v *int64) int64 {
			if v == nil {
				return 0
			}
			return *v
		}
		r.Tokens = &Tokens{Input: val(p.Input), Output: val(p.Output), CacheRead: val(p.CacheRead), CacheWrite: val(p.CacheWrite)}
	}
	return r
}

// ResumeParams is insights.resume's request.
type ResumeParams struct {
	Budget string `json:"budget" desc:"the budget's name"`
	Key    string `json:"key,omitempty" desc:"one key of it (a plan, environment or kind; default: every held one)"`
}

// ExportParams is insights.export's request.
type ExportParams struct {
	QueryParams
	What string `json:"what" desc:"spans, usage or alerts"`
}

// What normalizes an export's what: span, usage or alert.
func What(s string) string {
	switch strings.TrimSuffix(strings.ToLower(s), "s") {
	case "span":
		return RecordSpan
	case "usage":
		return RecordUsage
	case "alert":
		return RecordAlert
	}
	return s
}

// Register exposes the service on the wire as insights.*.
func Register(r *protocol.Router, s *Service) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }

	m("insights.summary", protocol.Method(func(ctx context.Context, p QueryParams) (Summary, error) {
		sum, err := s.Summary(ctx, p.query())
		return sum, wireError(err)
	}))
	m("insights.asks", protocol.Method(func(ctx context.Context, p AsksParams) ([]Ask, error) {
		asks, err := s.Asks(ctx, p.query(), p.Limit)
		return asks, wireError(err)
	}))
	m("insights.usage", protocol.Method(func(ctx context.Context, p UsageParams) (UsageResult, error) {
		res, err := s.ReportUsage(ctx, p.report())
		return res, wireError(err)
	}))
	m("insights.budgets", protocol.Method(func(ctx context.Context, _ struct{}) ([]BudgetStatus, error) {
		return s.Budgets(ctx), nil
	}))
	m("insights.resume", protocol.Method(func(ctx context.Context, p ResumeParams) ([]BudgetStatus, error) {
		st, err := s.Resume(ctx, p.Budget, p.Key)
		return st, wireError(err)
	}))
	m("insights.export", protocol.Method(func(ctx context.Context, p ExportParams) ([]Record, error) {
		recs, err := s.Export(ctx, p.query(), What(p.What))
		return recs, wireError(err)
	}))
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	}
	return err
}
