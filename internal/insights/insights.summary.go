package insights

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// span returns a query's range: today when it gives none, never more than
// the days kept.
func (s *Service) span(q Query) (time.Time, time.Time) {
	now := s.now()
	from, to := q.From, q.To
	if to.IsZero() || to.After(now) {
		to = now
	}
	if from.IsZero() {
		from = startOfDay(now)
	}
	if oldest := startOfDay(now).AddDate(0, 0, -s.cfg.KeepDays); from.Before(oldest) {
		from = oldest
	}
	if from.After(to) {
		from = to
	}
	return from.In(s.cfg.Location), to.In(s.cfg.Location)
}

// keeps reports whether q selects a record of who.
func (q Query) keeps(w Who) bool {
	return (q.Kind == "" || w.Kind == q.Kind) && (q.Environment == "" || w.Env == q.Environment) &&
		(q.Sees == nil || q.Sees(w.Env, w.Pane))
}

// records reads q's records, with the open spans as they are now.
func (s *Service) records(ctx context.Context, q Query) ([]Record, time.Time, time.Time, error) {
	if err := s.ready(ctx); err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	from, to := s.span(q)
	recs, _, err := s.store.Read(from, to, s.cfg.Location)
	now := s.now()
	s.mu.Lock()
	for _, sp := range s.open {
		cur := *sp
		cur.End = now
		recs = append(recs, Record{Type: RecordSpan, Span: &cur})
	}
	s.mu.Unlock()
	out := recs[:0]
	for _, r := range recs {
		switch {
		case r.Span != nil:
			sp := *r.Span
			if !q.keeps(sp.Who) || !sp.End.After(from) || !sp.Start.Before(to) {
				continue
			}
			if sp.Start.Before(from) {
				sp.Start = from
			}
			if sp.End.After(to) {
				sp.End = to
			}
			r.Span = &sp
		case r.Usage != nil:
			if !q.keeps(r.Usage.Who) || r.Usage.Time.Before(from) || r.Usage.Time.After(to) {
				continue
			}
		case r.Alert != nil:
			if r.Alert.Time.Before(from) || r.Alert.Time.After(to) {
				continue
			}
		}
		out = append(out, r)
	}
	return out, from, to, err
}

// Summary is time in each state and usage over q's range, grouped by q.By.
func (s *Service) Summary(ctx context.Context, q Query) (Summary, error) {
	q.By = cmp.Or(q.By, ByKind)
	if !slices.Contains(Bys, q.By) {
		return Summary{}, invalid("by %q (want one of %v)", q.By, Bys)
	}
	recs, from, to, err := s.records(ctx, q)
	sum := Summary{From: from, To: to, By: q.By, Rows: []Row{}}
	rows := map[string]*rowAcc{}
	total := newRowAcc("")
	for _, r := range recs {
		var who Who
		var at time.Time
		switch {
		case r.Span != nil:
			if q.By == ByModel {
				continue // time has no model
			}
			who, at = r.Span.Who, r.Span.Start
		case r.Usage != nil:
			who, at = r.Usage.Who, r.Usage.Time
		default:
			continue
		}
		key := groupKey(q.By, who, at.In(s.cfg.Location), r.Usage)
		acc := rows[key]
		if acc == nil {
			if len(rows) >= MaxRows {
				continue
			}
			acc = newRowAcc(key)
			rows[key] = acc
		}
		acc.add(r)
		total.add(r)
	}
	for _, key := range sortedKeys(rows) {
		sum.Rows = append(sum.Rows, rows[key].row())
	}
	sum.Total = total.row()
	sum.Total.Key = "total"
	return sum, err
}

func groupKey(by string, w Who, at time.Time, u *Usage) string {
	unknown := func(s string) string { return cmp.Or(s, "(none)") }
	switch by {
	case ByAgent:
		if w.Name != "" && w.Name != w.Agent {
			return w.Agent + " " + w.Name
		}
		return w.Agent
	case ByEnvironment:
		return unknown(w.Env)
	case ByTask:
		if w.Plan == "" {
			return "(none)"
		}
		return w.Plan + "/" + w.Task
	case ByDay:
		return at.Format(dayLayout)
	case ByModel:
		if u != nil {
			return unknown(u.Model)
		}
		return "(none)"
	}
	return unknown(w.Kind)
}

// rowAcc adds records into a Row.
type rowAcc struct {
	r       Row
	agents  map[string]bool
	sources map[string]bool
	models  map[string]bool
}

func newRowAcc(key string) *rowAcc {
	return &rowAcc{r: Row{Key: key, StatesMS: map[string]int64{}}, agents: map[string]bool{}, sources: map[string]bool{}, models: map[string]bool{}}
}

func (a *rowAcc) add(r Record) {
	switch {
	case r.Span != nil:
		sp := r.Span
		d := sp.Duration().Milliseconds()
		a.r.StatesMS[sp.State] += d
		if sp.State == StateBlocked || sp.State == StateApproval {
			a.r.WaitingMS += d
			a.r.Asks++
		}
		if sp.State != StateApproval {
			a.agents[sp.Agent] = true
		}
	case r.Usage != nil:
		u := r.Usage
		a.agents[u.Agent] = true
		a.sources[u.Source] = true
		if u.Model != "" {
			a.models[u.Model] = true
		}
		if u.Tokens != nil {
			t := Tokens{}
			if a.r.Tokens != nil {
				t = *a.r.Tokens
			}
			t = t.add(*u.Tokens)
			a.r.Tokens = &t
		}
		if u.CostUSD != nil {
			c := *u.CostUSD
			if a.r.CostUSD != nil {
				c += *a.r.CostUSD
			}
			a.r.CostUSD = &c
		}
	}
}

func (a *rowAcc) row() Row {
	r := a.r
	r.Agents = len(a.agents)
	r.Sources = sortedKeys(a.sources)
	r.Models = sortedKeys(a.models)
	return r
}

// Asks lists the times agents waited on a person in q's range, newest
// first, at most limit (MaxAsks).
func (s *Service) Asks(ctx context.Context, q Query, limit int) ([]Ask, error) {
	if limit <= 0 || limit > MaxAsks {
		limit = MaxAsks
	}
	recs, _, to, err := s.records(ctx, q)
	s.mu.Lock()
	open := map[string]bool{}
	for k, sp := range s.open {
		open[k+"\x00"+sp.Start.String()] = true
	}
	s.mu.Unlock()
	out := []Ask{}
	for _, r := range recs {
		sp := r.Span
		if sp == nil || sp.State != StateBlocked && sp.State != StateApproval {
			continue
		}
		key := sp.Agent
		if sp.State == StateApproval {
			key = "approval:" + sp.Agent
		}
		a := Ask{Who: sp.Who, State: sp.State, Start: sp.Start, End: sp.End, WaitedMS: sp.Duration().Milliseconds(), Reason: sp.Reason}
		if open[key+"\x00"+sp.Start.String()] && !sp.End.Before(to) {
			a.Open, a.End = true, time.Time{}
		}
		out = append(out, a)
	}
	slices.SortStableFunc(out, func(a, b Ask) int { return b.Start.Compare(a.Start) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, err
}

// Export returns q's records of one type (spans, usage or alerts), oldest
// first.
func (s *Service) Export(ctx context.Context, q Query, what string) ([]Record, error) {
	if what != RecordSpan && what != RecordUsage && what != RecordAlert {
		return nil, invalid("what %q (want span, usage or alert)", what)
	}
	recs, _, _, err := s.records(ctx, q)
	out := []Record{}
	for _, r := range recs {
		if r.Type == what {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int { return recordTime(a).Compare(recordTime(b)) })
	return out, err
}
