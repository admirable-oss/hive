package web

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/observer"
)

func alertEvent(seq uint64, a insights.Alert) observer.Update {
	raw, _ := json.Marshal(a)
	return observer.Update{Event: &observer.Event{Seq: seq, Type: insights.EventAlert, Time: time.Now(), Data: raw}}
}

func TestInsightsSection(t *testing.T) {
	cost := 2.5
	in := &observer.Insights{
		Summary: insights.Summary{Rows: []insights.Row{
			{
				Key: "claude", StatesMS: map[string]int64{"working": 4_800_000, "blocked": 300_000, "idle": 60_000}, WaitingMS: 300_000, Asks: 1,
				Tokens: &insights.Tokens{Input: 1_000_000, Output: 200_000}, CostUSD: &cost, Sources: []string{"transcript"},
			},
			{Key: "codex", StatesMS: map[string]int64{"working": 60_000}},
		}},
		Budgets: []insights.BudgetStatus{
			{Budget: insights.Budget{Name: "daily", Per: insights.PerDay}, Key: "today", Fraction: 1.25, Level: insights.LevelExceeded, Held: true},
			{Budget: insights.Budget{Name: "quiet", Per: insights.PerDay}, Key: "today", Fraction: 0.1},
		},
		Asks: []insights.Ask{{Who: insights.Who{Kind: "claude", Name: "Claude Code", Env: "dev"}, Start: time.Now(), WaitedMS: 300_000, Reason: "SECRET run the tests?"}},
	}
	c := newConn(Runtime{Name: "laptop"}, slog.New(slog.DiscardHandler))
	c.insights["all"] = insightsRead{at: time.Now(), in: in}
	c.insights[observer.Scope{Environments: []string{"dev"}}.String()] = insightsRead{at: time.Now(), in: in}
	r := &shown{rt: Runtime{Name: "laptop", Screens: true}, v: RuntimeView{loaded: true}, c: c}

	all := &page{grant: Grant{Scope: observer.Scope{All: true}, Screens: true}, now: time.Now()}
	html := renderString(insightsSection(all, r))
	for _, want := range []string{"Insights today", "claude", "1h 20m", "1.2M", "$2.50", "transcript", "codex", "unknown", "Budget daily", "125% exceeded · holding new tasks", "Claude Code in dev waited 5m", "SECRET run the tests?"} {
		if !strings.Contains(html, want) {
			t.Errorf("the section lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "quiet") {
		t.Error("a budget far from its limits is not shown")
	}
	// A link without screens never shows what agents asked; a scoped link
	// reads only its own scope's insights.
	scoped := &page{grant: Grant{Scope: observer.Scope{Environments: []string{"dev"}}}, now: time.Now()}
	if html := renderString(insightsSection(scoped, r)); !strings.Contains(html, "Claude Code in dev waited 5m") || strings.Contains(html, "SECRET") {
		t.Fatalf("without screens: %s", html)
	}
	if in.Asks[0].Reason == "" {
		t.Fatal("the cached insights were changed")
	}
	other := &page{grant: Grant{Scope: observer.Scope{Environments: []string{"ops"}}}, now: time.Now()}
	if html := renderString(insightsSection(other, r)); html != "" {
		t.Fatalf("a scope not read (and the runtime not connected) shows nothing: %s", html)
	}
}

func TestModelNotesBudgetAlerts(t *testing.T) {
	m := NewModel()
	m.Apply(observer.Update{Snapshot: &observer.Snapshot{Seq: 1}}, time.Now())
	m.Apply(alertEvent(2, insights.Alert{Budget: "e", Per: insights.PerEnvironment, Key: "dev", Level: insights.LevelWarning, Message: "budget e for environment dev is at 80%"}), time.Now())
	v := m.View()
	if len(v.Activity) != 1 || v.Activity[0].Kind != "budget" || v.Activity[0].Environment != "dev" || !strings.Contains(v.Activity[0].Text, "is at 80%") {
		t.Fatalf("activity: %+v", v.Activity)
	}
}

func TestNotifierAnnouncesBudgetAlerts(t *testing.T) {
	n, _ := newTestNotifier(t)
	var got []string
	n.alert = func(rt string, a insights.Alert) { got = append(got, rt+":"+a.Budget+":"+a.Level) }
	al := insights.Alert{Budget: "daily", Per: insights.PerDay, Key: "today", Level: insights.LevelExceeded, Message: "budget daily is exceeded ($3.10 of $3.00)"}
	n.update("rt", alertEvent(1, al)) // before the first snapshot: not primed
	n.update("rt", snap())
	n.update("rt", alertEvent(2, al))
	if len(got) != 1 || got[0] != "rt:daily:exceeded" {
		t.Fatalf("alerts: %v", got)
	}
	var nt Notification
	_ = json.Unmarshal(alertMessage("rt", al, "", time.Now()).Payload, &nt)
	if nt.Kind != KindBudget || nt.Title != "Budget daily exceeded" || !strings.Contains(nt.Body, "$3.10 of $3.00") || nt.URL != "/r/rt?from=notification" {
		t.Fatalf("notification: %+v", nt)
	}
	_ = json.Unmarshal(alertMessage("rt", al, DetailPrivate, time.Now()).Payload, &nt)
	if nt.Title != "Hive" || strings.Contains(nt.Body, "daily") {
		t.Fatalf("a private notification names nothing: %+v", nt)
	}
}
