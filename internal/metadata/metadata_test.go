package metadata

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReportListAndExpire(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	s := NewStore(func() time.Time { return now })
	title := "reviewer"
	e, err := s.Report(Report{Target: Target{Pane: "p1"}, Tokens: map[string]string{"pr": "PR #12 ✓", "tests": "3/40\n"}, Title: &title, TTL: time.Minute, Source: "pr-status"})
	if err != nil || e.Title != "reviewer" || len(e.Tokens) != 2 || e.Tokens[1].Text != "3/40" {
		t.Fatalf("report = %+v, %v", e, err)
	}
	// A later report replaces one token and keeps the other.
	if e, _ = s.Report(Report{Target: Target{Pane: "p1"}, Tokens: map[string]string{"tests": "40/40", "pr": ""}, TTL: time.Hour}); len(e.Tokens) != 1 || e.Tokens[0].Text != "40/40" {
		t.Fatalf("update = %+v", e)
	}
	if _, err := s.Report(Report{Target: Target{Environment: "api"}, Tokens: map[string]string{"pr": "#3 open"}}); err != nil {
		t.Fatal(err)
	}
	if l := s.List(); len(l) != 2 {
		t.Fatalf("list = %+v", l)
	}
	now = now.Add(2 * time.Minute) // the title (1m) runs out; the tests token (1h) holds
	changed := s.Expire()
	if len(changed) != 1 || changed[0].Pane != "p1" || changed[0].Title != "" || len(changed[0].Tokens) != 1 {
		t.Fatalf("expired = %+v", changed)
	}
	now = now.Add(2 * time.Hour)
	if changed := s.Expire(); len(changed) != 2 || !changed[0].Empty() {
		t.Fatalf("all expired = %+v", changed)
	}
	if l := s.List(); len(l) != 0 {
		t.Fatalf("list after expiry = %+v", l)
	}
}

func TestReportChecks(t *testing.T) {
	s := NewStore(nil)
	for _, r := range []Report{
		{},
		{Target: Target{Pane: "p", Environment: "e"}},
		{Target: Target{Pane: "p"}, Tokens: map[string]string{"bad key": "x"}},
		{Target: Target{Pane: "p"}, TTL: -time.Second},
	} {
		if _, err := s.Report(r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", r, err)
		}
	}
	long := strings.Repeat("x", 100)
	e, _ := s.Report(Report{Target: Target{Pane: "p"}, Tokens: map[string]string{"k": long}})
	if n := len([]rune(e.Tokens[0].Text)); n != MaxTextLen {
		t.Fatalf("long text kept %d runes", n)
	}
	e, _ = s.Report(Report{Target: Target{Pane: "p"}, Clear: true, Tokens: map[string]string{"j": "y"}})
	if len(e.Tokens) != 1 || e.Tokens[0].Key != "j" {
		t.Fatalf("clear = %+v", e)
	}
}
