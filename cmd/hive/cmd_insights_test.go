package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/insights"
)

func TestInsightsRanges(t *testing.T) {
	now := time.Date(2026, 10, 25, 14, 30, 0, 0, time.Local)
	today := time.Date(2026, 10, 25, 0, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		since string
		want  time.Time
	}{
		{"", time.Time{}},
		{"today", today},
		{"yesterday", today.AddDate(0, 0, -1)},
		{"1d", today},
		{"7d", today.AddDate(0, 0, -6)},
		{"2w", today.AddDate(0, 0, -13)},
		{"12h", now.Add(-12 * time.Hour)},
		{"2026-10-01", time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)},
		{"2026-10-01T08:00:00Z", time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)},
	} {
		got, err := parseSince(tc.since, now)
		if err != nil || !got.Equal(tc.want) {
			t.Errorf("--since %q = %v, %v; want %v", tc.since, got, err, tc.want)
		}
	}
	if got, err := parseUntil("2026-10-20", now); err != nil || !got.Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local)) {
		t.Errorf("--until a date is its end: %v %v", got, err)
	}
	for _, bad := range []string{"soon", "0d", "-3d", "-1h"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("--since %q is accepted", bad)
		}
	}
	if _, err := (insightsRange{since: "today", until: "2026-10-01"}).query(now); err == nil {
		t.Error("--until before --since is accepted")
	}
}

func TestInsightsTable(t *testing.T) {
	now := time.Now()
	cost := 3.4
	sum := insights.Summary{From: startOfLocalDay(now), To: now, By: insights.ByKind, Rows: []insights.Row{
		{
			Key: "claude", Agents: 2, StatesMS: map[string]int64{"working": 4_800_000, "blocked": 720_000, "idle": 60_000}, WaitingMS: 720_000, Asks: 3,
			Tokens: &insights.Tokens{Input: 1_000_000, Output: 200_000, CacheRead: 50_000}, CostUSD: &cost, Sources: []string{"statusline", "transcript"},
		},
		{Key: "gemini", Agents: 1, StatesMS: map[string]int64{"working": 30_000}},
	}}
	sum.Total = insights.Row{Key: "total", Agents: 3, StatesMS: map[string]int64{"working": 4_830_000}, WaitingMS: 720_000, Asks: 3}
	asks := []insights.Ask{{Who: insights.Who{Kind: "claude", Name: "Claude Code", Pane: "p1", Plan: "p9", Task: "fix"}, Start: now, WaitedMS: 300_000, Reason: "Run the tests?\nmore"}}
	budgets := []insights.BudgetStatus{
		{Budget: insights.Budget{Name: "daily", Per: insights.PerDay, Limits: insights.Limits{CostUSD: 3}}, Key: "today", Used: insights.Amounts{CostUSD: 3.4, CostKnown: true}, Fraction: 1.13, Level: insights.LevelExceeded, Held: true},
		{Budget: insights.Budget{Name: "quiet", Per: insights.PerDay, Limits: insights.Limits{CostUSD: 100}}, Key: "today", Fraction: 0.03},
	}
	var b bytes.Buffer
	if err := printInsights(&b, sum, asks, budgets, now); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"Today (00:00 – ", "by kind", "KIND", "WAITING ON YOU", "1h20m", "12m (3 asks)", "1.2M (in 1M · out 200k · cache 50k)", "$3.40", "statusline, transcript",
		"gemini", "unknown", `Claude Code (p1) task p9/fix waited 5m: "Run the tests?"`,
		"daily: $3.40 of $3.00, 113%, exceeded, holding new tasks (hive insights resume daily)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "quiet") || strings.Contains(out, "more") {
		t.Errorf("budgets far from their limits, and asks past their first line, are not shown:\n%s", out)
	}
}

func TestInsightsCSV(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	cost := 0.25
	recs := []insights.Record{
		{Type: insights.RecordUsage, Usage: &insights.Usage{Who: insights.Who{Agent: "a1", Kind: "claude"}, Time: at, Source: "statusline", CostUSD: &cost}},
		{Type: insights.RecordUsage, Usage: &insights.Usage{Who: insights.Who{Agent: "a2", Kind: "codex"}, Time: at, Source: "report", Tokens: &insights.Tokens{Input: 10, Output: 2}}},
	}
	var b bytes.Buffer
	if err := exportRecords(&b, "csv", insights.RecordUsage, recs); err != nil {
		t.Fatal(err)
	}
	want := "time,agent,pane,kind,name,environment,plan,task,session,model,source,input,output,cache_read,cache_write,cost_usd\n" +
		"2026-10-25T09:00:00Z,a1,,claude,,,,,,,statusline,,,,,0.25\n" +
		"2026-10-25T09:00:00Z,a2,,codex,,,,,,,report,10,2,0,0,\n"
	if b.String() != want {
		t.Fatalf("usage CSV (unknown is empty, not zero):\n%s\nwant:\n%s", b.String(), want)
	}
	b.Reset()
	sp := insights.Span{Who: insights.Who{Agent: "a1", Kind: "claude"}, State: "blocked", Start: at, End: at.Add(90 * time.Second), Reason: `asks "why", twice`}
	if err := exportRecords(&b, "csv", insights.RecordSpan, []insights.Record{{Type: insights.RecordSpan, Span: &sp}}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(b.String(), "blocked,2026-10-25T09:00:00Z,2026-10-25T09:01:30Z,90000,\"asks \"\"why\"\", twice\"\n") {
		t.Fatalf("spans CSV: %s", b.String())
	}
	b.Reset()
	if err := exportRecords(&b, "json", insights.RecordSpan, []insights.Record{{Type: insights.RecordSpan, Span: &sp}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"duration_ms": 90000`) {
		t.Fatalf("spans JSON: %s", b.String())
	}
}

func TestStatusline(t *testing.T) {
	var in statuslineInput
	in.Model.DisplayName = "Opus"
	cost := 1.234
	if got := statusline(in, &cost, nil); got != "Opus · $1.23" {
		t.Errorf("status line: %q", got)
	}
	budgets := []insights.BudgetStatus{
		{Budget: insights.Budget{Name: "a"}, Fraction: 0.85, Level: insights.LevelWarning},
		{Budget: insights.Budget{Name: "b"}, Fraction: 1.2, Level: insights.LevelExceeded, Held: true},
		{Budget: insights.Budget{Name: "c"}, Level: insights.LevelUnenforceable},
	}
	if got := statusline(in, nil, budgets); got != "Opus · held by budget b 120%" {
		t.Errorf("the budget nearest its limit: %q", got)
	}
	if got := statusline(statuslineInput{}, nil, nil); got != "claude" {
		t.Errorf("knowing nothing: %q", got)
	}
}
