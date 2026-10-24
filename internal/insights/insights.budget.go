package insights

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/task"
)

// windowOf is a budget's window now: the day, or a plan's life.
func windowOf(b Budget, day string) string {
	if b.Per == PerPlan {
		return "life"
	}
	return day
}

// raisedKey names a budget's key in a window, for levels and holds.
func raisedKey(b Budget, window, key string) string {
	return b.Name + "\x00" + window + "\x00" + key
}

// matches reports whether a record's who counts for b (its filters).
func (b Budget) matches(w Who) bool {
	return (b.Environment == "" || w.Env == b.Environment) && (b.Kind == "" || w.Kind == b.Kind)
}

// keyOf is the key a record counts under for b: "" for none.
func (b Budget) keyOf(w Who) string {
	switch b.Per {
	case PerDay:
		return "today"
	case PerEnvironment:
		return w.Env
	case PerKind:
		return w.Kind
	case PerPlan:
		return w.Plan
	}
	return ""
}

// usedLocked is what each key of b used in its window, open working spans
// counted to now.
func (s *Service) usedLocked(b Budget, now time.Time) map[string]Amounts {
	used := map[string]Amounts{}
	add := func(w Who, a Amounts) {
		if !b.matches(w) {
			return
		}
		if k := b.keyOf(w); k != "" {
			used[k] = addAmounts(used[k], a)
		}
	}
	if b.Per == PerPlan {
		for plan, byScope := range s.state.Plans {
			for scope, a := range byScope {
				env, kind, _ := strings.Cut(scope, "\x00")
				add(Who{Plan: plan, Env: env, Kind: kind}, a)
			}
		}
	} else {
		for _, r := range s.today {
			switch {
			case r.Span != nil && r.Span.State == StateWorking:
				add(r.Span.Who, Amounts{WorkingMS: r.Span.Duration().Milliseconds()})
			case r.Usage != nil:
				var a Amounts
				if r.Usage.Tokens != nil {
					a.Tokens, a.TokensKnown = r.Usage.Tokens.Total(), true
				}
				if r.Usage.CostUSD != nil {
					a.CostUSD, a.CostKnown = *r.Usage.CostUSD, true
				}
				add(r.Usage.Who, a)
			}
		}
	}
	for _, sp := range s.open {
		if sp.State == StateWorking && now.After(sp.Start) {
			add(sp.Who, Amounts{WorkingMS: now.Sub(sp.Start).Milliseconds()})
		}
	}
	return used
}

// fraction is the highest share of any of b's limits that a used.
func (b Budget) fraction(a Amounts) float64 {
	f := 0.0
	if b.CostUSD > 0 {
		f = max(f, a.CostUSD/b.CostUSD)
	}
	if b.Tokens > 0 {
		f = max(f, float64(a.Tokens)/float64(b.Tokens))
	}
	if b.WorkingMS > 0 {
		f = max(f, float64(a.WorkingMS)/float64(b.WorkingMS))
	}
	return f
}

// unenforceable reports whether b limits something nobody reports while
// its scope works: a cost limit with no cost known, say.
func (b Budget) unenforceable(a Amounts) bool {
	if a.WorkingMS == 0 {
		return false
	}
	costOnly := b.CostUSD > 0 && !a.CostKnown
	tokensOnly := b.Tokens > 0 && !a.TokensKnown
	return (costOnly || tokensOnly) && b.WorkingMS == 0
}

var levelRank = map[string]int{"": 0, LevelUnenforceable: 1, LevelWarning: 2, LevelExceeded: 3}

// checkLocked raises each budget level reached since the last check, once
// per key and window, and holds what exceeded pausing budgets cover.
func (s *Service) checkLocked(now time.Time) []Alert {
	if len(s.cfg.Budgets) == 0 {
		return nil
	}
	var alerts []Alert
	changed := false
	for _, b := range s.cfg.Budgets {
		window := windowOf(b, s.day)
		used := s.usedLocked(b, now)
		for _, key := range sortedKeys(used) {
			a := used[key]
			f := b.fraction(a)
			level := ""
			switch {
			case f >= 1:
				level = LevelExceeded
			case f >= b.WarnAt:
				level = LevelWarning
			case b.unenforceable(a):
				level = LevelUnenforceable
			}
			rk := raisedKey(b, window, key)
			if levelRank[level] <= levelRank[s.state.Raised[rk]] {
				continue
			}
			s.state.Raised[rk] = level
			alert := Alert{
				Time: now, Budget: b.Name, Per: b.Per, Key: key, Window: window, Level: level,
				Used: a, Limit: b.Limits, Action: b.Action,
			}
			alert.Message = alertMessage(b, key, level, a, f)
			if level == LevelExceeded && b.Action == ActionPause && !s.state.Resumed[rk] {
				s.state.Held[rk] = true
				alert.Held = true
				alert.Message += "; new work is held (hive insights resume " + b.Name + ")"
				changed = true
			}
			s.recordLocked(Record{Type: RecordAlert, Alert: &alert})
			alerts = append(alerts, alert)
		}
	}
	if changed {
		s.refreshHoldsLocked(now)
	}
	return alerts
}

func alertMessage(b Budget, key, level string, a Amounts, f float64) string {
	what := "budget " + b.Name
	if b.Per != PerDay {
		what += " for " + b.Per + " " + key
	}
	switch level {
	case LevelUnenforceable:
		return what + " limits usage that nothing here reports; it cannot be enforced"
	case LevelWarning:
		return fmt.Sprintf("%s is at %.0f%% (%s)", what, f*100, amountsText(b.Limits, a))
	}
	return fmt.Sprintf("%s is exceeded (%s)", what, amountsText(b.Limits, a))
}

// amountsText says what a used of each limit.
func amountsText(l Limits, a Amounts) string {
	var parts []string
	if l.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f of $%.2f", a.CostUSD, l.CostUSD))
	}
	if l.Tokens > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d tokens", a.Tokens, l.Tokens))
	}
	if l.WorkingMS > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s working", time.Duration(a.WorkingMS)*time.Millisecond/time.Second*time.Second, time.Duration(l.WorkingMS)*time.Millisecond))
	}
	return strings.Join(parts, ", ")
}

// refreshHoldsLocked publishes the holds in force for Hold.
func (s *Service) refreshHoldsLocked(now time.Time) {
	var hs []hold
	for _, b := range s.cfg.Budgets {
		if b.Action != ActionPause {
			continue
		}
		window := windowOf(b, s.day)
		prefix := b.Name + "\x00" + window + "\x00"
		for rk := range s.state.Held {
			key, ok := strings.CutPrefix(rk, prefix)
			if !ok || s.state.Resumed[rk] {
				continue
			}
			what := "budget " + b.Name
			if b.Per != PerDay {
				what += " (" + b.Per + " " + key + ")"
			}
			hs = append(hs, hold{budget: b, key: key, reason: "held by " + what + " until " + holdEnd(b, now)})
		}
	}
	slices.SortFunc(hs, func(a, b hold) int { return strings.Compare(a.reason, b.reason) })
	s.holds.Store(&hs)
}

func holdEnd(b Budget, now time.Time) string {
	if b.Per == PerPlan {
		return "hive insights resume " + b.Name
	}
	return "tomorrow, or hive insights resume " + b.Name
}

// Hold says why a queued task must not start yet: a pausing budget over
// its limit covers it. "" lets it start. The task scheduler calls it under
// its own lock, so it reads only what refreshHoldsLocked published.
func (s *Service) Hold(p *task.Plan, t *task.Task) string {
	hs := *s.holds.Load()
	if len(hs) == 0 {
		return ""
	}
	w := Who{Plan: p.ID, Env: t.Spec.EnvironmentID, Kind: t.Spec.Kind}
	for _, h := range hs {
		if !h.budget.matches(w) {
			continue
		}
		if k := h.budget.keyOf(w); k == h.key {
			return h.reason
		}
	}
	return ""
}

// Budgets reports every budget's use, by key, in its window now.
func (s *Service) Budgets(ctx context.Context) []BudgetStatus {
	if s.ready(ctx) != nil {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked(now)
	out := []BudgetStatus{}
	for _, b := range s.cfg.Budgets {
		window := windowOf(b, s.day)
		used := s.usedLocked(b, now)
		if b.Per == PerDay && len(used) == 0 {
			used["today"] = Amounts{}
		}
		for _, key := range sortedKeys(used) {
			rk := raisedKey(b, window, key)
			out = append(out, BudgetStatus{
				Budget: b, Key: key, Window: window, Used: used[key], Fraction: b.fraction(used[key]),
				Level: s.state.Raised[rk], Held: s.state.Held[rk] && !s.state.Resumed[rk], Resumed: s.state.Resumed[rk],
			})
		}
	}
	return out
}

// Resume lifts a pausing budget's hold, for one key or every key, until
// its window ends.
func (s *Service) Resume(ctx context.Context, name, key string) ([]BudgetStatus, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	idx := slices.IndexFunc(s.cfg.Budgets, func(b Budget) bool { return b.Name == name })
	if idx < 0 {
		var names []string
		for _, b := range s.cfg.Budgets {
			names = append(names, b.Name)
		}
		return nil, fmt.Errorf("%w: no budget %q (have %s)", ErrNotFound, name, strings.Join(names, ", "))
	}
	b := s.cfg.Budgets[idx]
	now := s.now()
	s.mu.Lock()
	window := windowOf(b, s.day)
	prefix := b.Name + "\x00" + window + "\x00"
	var lifted []string
	for rk := range s.state.Held {
		k, ok := strings.CutPrefix(rk, prefix)
		if !ok || key != "" && k != key {
			continue
		}
		s.state.Resumed[rk] = true
		delete(s.state.Held, rk)
		lifted = append(lifted, k)
	}
	if key != "" && len(lifted) == 0 {
		// Resumed before it was held: it will not hold this window.
		s.state.Resumed[raisedKey(b, window, key)] = true
		lifted = append(lifted, key)
	}
	s.refreshHoldsLocked(now)
	s.dirty = true
	s.mu.Unlock()
	for _, k := range lifted {
		s.ports.Events.Publish(EventResumed, map[string]string{"budget": b.Name, "key": k})
	}
	s.log.Info("budget resumed", "budget", b.Name, "keys", strings.Join(lifted, ", "))
	var out []BudgetStatus
	for _, st := range s.Budgets(ctx) {
		if st.Budget.Name == name {
			out = append(out, st)
		}
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
