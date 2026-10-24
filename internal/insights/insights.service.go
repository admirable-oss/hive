package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/task"
)

// Agents is what the service reads about agents.
type Agents interface {
	List(ctx context.Context, all bool) ([]agent.Agent, error)
	Get(ctx context.Context, id string) (agent.Agent, error)
}

// Tasks says which plan and task an agent works on, for agents whose
// task.updated event this daemon did not see (after a restart).
type Tasks interface {
	TaskOf(ctx context.Context, agentID string) (plan, task string)
}

// Events publishes insights.* events.
type Events interface {
	Publish(typ string, data any)
}

// Ports are what the service needs from the rest of the daemon.
type Ports struct {
	Agents Agents
	Tasks  Tasks
	Events Events
}

// Config is the service's policy. Zero values are the defaults.
type Config struct {
	Logger *slog.Logger
	Now    func() time.Time
	// Location is the time zone days are counted in (default Local).
	Location *time.Location
	// Disabled records nothing; queries return what was recorded.
	Disabled bool
	// KeepDays is how many days of records are kept (DefaultKeepDays).
	KeepDays int
	Budgets  []Budget
	// Interval is how often budgets are checked and the state saved
	// besides after changes (default 15s).
	Interval time.Duration
}

func (c Config) withDefaults() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Location == nil {
		c.Location = time.Local
	}
	if c.KeepDays <= 0 {
		c.KeepDays = DefaultKeepDays
	}
	if c.Interval <= 0 {
		c.Interval = 15 * time.Second
	}
	for i := range c.Budgets {
		b := &c.Budgets[i]
		if b.WarnAt <= 0 || b.WarnAt > 1 {
			b.WarnAt = DefaultWarnAt
		}
		if b.Action == "" {
			b.Action = ActionWarn
		}
	}
	return c
}

// Service records spans and usage, answers queries, and keeps budgets.
type Service struct {
	cfg   Config
	log   *slog.Logger
	store *Store
	ports Ports

	loaded   chan struct{}
	loadOnce sync.Once
	loadErr  error

	mu    sync.Mutex
	state State
	// open are the spans going on, by agent ID ("approval:plan/task" for
	// a task waiting for approval).
	open map[string]*Span
	// tasks says which plan/task each agent works on; models the model
	// each session last reported.
	tasks  map[string][2]string
	models map[string]string
	// today holds today's closed spans and usage, for budgets; day is its
	// date.
	today []Record
	day   string
	dirty bool

	// holds is what Hold reads: the scheduler calls it under its own lock,
	// so it never takes mu.
	holds atomic.Pointer[[]hold]
}

// hold is a budget holding new work in its scope.
type hold struct {
	budget Budget
	key    string
	reason string
}

// NewService returns a service over store; Load reads what a previous
// daemon left.
func NewService(cfg Config, store *Store, ports Ports) *Service {
	cfg = cfg.withDefaults()
	s := &Service{
		cfg: cfg, log: logging.OrDiscard(cfg.Logger), store: store, ports: ports,
		loaded: make(chan struct{}), open: map[string]*Span{}, tasks: map[string][2]string{}, models: map[string]string{},
	}
	s.state.init()
	empty := []hold{}
	s.holds.Store(&empty)
	if s.ports.Events == nil {
		s.ports.Events = nopEvents{}
	}
	return s
}

type nopEvents struct{}

func (nopEvents) Publish(string, any) {}

func (s *Service) now() time.Time { return s.cfg.Now().In(s.cfg.Location) }

// Load reads the state and today's records, closes the spans a previous
// daemon left open at the moment it stopped, and opens spans for the
// agents running now. It runs once; Run calls it first.
func (s *Service) Load(ctx context.Context) error {
	s.loadOnce.Do(func() {
		s.loadErr = s.load(ctx)
		close(s.loaded)
	})
	return s.loadErr
}

func (s *Service) ready(ctx context.Context) error {
	select {
	case <-s.loaded:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) load(ctx context.Context) error {
	st, err := s.store.LoadState()
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	now := s.now()
	day := now.Format(dayLayout)
	recs, skipped, rerr := s.store.Read(now, now, s.cfg.Location)
	if rerr != nil {
		errs = append(errs, rerr)
	}
	if skipped > 0 {
		s.log.Warn("insights lines skipped (broken, or from a newer hive)", "count", skipped)
	}
	var list []agent.Agent
	if s.ports.Agents != nil && !s.cfg.Disabled {
		list, _ = s.ports.Agents.List(ctx, false)
	}
	taskOf := map[string][2]string{}
	if s.ports.Tasks != nil {
		for _, a := range list {
			if p, t := s.ports.Tasks.TaskOf(ctx, a.ID); p != "" {
				taskOf[a.ID] = [2]string{p, t}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.day = st, day
	for _, r := range recs {
		if r.Type != RecordAlert {
			s.today = append(s.today, r)
		}
	}
	// Spans a previous daemon left open end when it stopped: nobody saw
	// what happened after.
	for _, sp := range st.Open {
		if !st.SavedAt.IsZero() && st.SavedAt.After(sp.Start) {
			sp.End = st.SavedAt
			s.closeLocked(sp)
		}
	}
	s.state.Open = nil
	s.tasks = taskOf
	for _, a := range list {
		if a.State != agent.StateExited {
			s.openLocked(a.ID, s.whoOf(a), string(a.State), reasonOf(a), now)
		}
	}
	s.dirty = true
	s.refreshHoldsLocked(now)
	return errors.Join(errs...)
}

// Run checks budgets, splits spans at midnight, prunes old days and saves
// the state until ctx ends, then saves the open spans for the next daemon.
func (s *Service) Run(ctx context.Context) {
	if err := s.Load(ctx); err != nil {
		s.log.Warn("reading the insights a previous daemon left", "err", err) // the rest runs
	}
	s.prune()
	tick := time.NewTicker(s.cfg.Interval)
	defer tick.Stop()
	lastPrune := s.now()
	for {
		select {
		case <-ctx.Done():
			s.save(true)
			return
		case <-tick.C:
		}
		now := s.now()
		s.mu.Lock()
		s.rolloverLocked(now)
		alerts := s.checkLocked(now)
		s.mu.Unlock()
		s.publish(alerts)
		s.save(false)
		if now.Sub(lastPrune) > time.Hour {
			s.prune()
			lastPrune = now
		}
	}
}

func (s *Service) prune() {
	if gone := s.store.Prune(s.cfg.KeepDays, s.now()); len(gone) > 0 {
		s.log.Info("insights forgotten", "days", strings.Join(gone, ", "))
	}
}

// save writes the state when it changed; final also records the open
// spans and the time, so the next daemon closes them there.
func (s *Service) save(final bool) {
	s.mu.Lock()
	if !s.dirty && !final {
		s.mu.Unlock()
		return
	}
	st := s.state
	st.SavedAt = s.now()
	st.Open = nil
	for _, sp := range s.open {
		st.Open = append(st.Open, *sp)
	}
	s.dirty = false
	s.mu.Unlock()
	if err := s.store.SaveState(st); err != nil {
		s.log.Warn("saving insights state", "err", err)
	}
}

// --- Spans ---

func (s *Service) whoOf(a agent.Agent) Who {
	w := Who{Agent: a.ID, Pane: a.PaneID, Kind: a.Kind, Name: a.Name, Env: a.EnvironmentID}
	if pt, ok := s.tasks[a.ID]; ok {
		w.Plan, w.Task = pt[0], pt[1]
	}
	return w
}

func reasonOf(a agent.Agent) string {
	if a.State != agent.StateBlocked {
		return ""
	}
	return truncate(a.Reason, MaxReasonBytes)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8Valid(s) {
		s = s[:len(s)-1]
	}
	return s
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }

// openLocked starts a span for key.
func (s *Service) openLocked(key string, who Who, state, reason string, now time.Time) {
	if s.cfg.Disabled {
		return
	}
	s.open[key] = &Span{Who: who, State: state, Start: now, Reason: reason}
	s.dirty = true
}

// endLocked closes key's open span at now.
func (s *Service) endLocked(key string, now time.Time) {
	sp := s.open[key]
	if sp == nil {
		return
	}
	delete(s.open, key)
	sp.End = now
	s.closeLocked(*sp)
}

// closeLocked records a finished span, split at local midnights so each
// part belongs to one day.
func (s *Service) closeLocked(sp Span) {
	if !sp.End.After(sp.Start) {
		return
	}
	var recs []Record
	for {
		midnight := startOfDay(sp.Start.In(s.cfg.Location)).AddDate(0, 0, 1)
		part := sp
		if sp.End.After(midnight) {
			part.End = midnight
		}
		p := part
		recs = append(recs, Record{Type: RecordSpan, Span: &p})
		if !sp.End.After(midnight) {
			break
		}
		sp.Start = midnight
	}
	s.recordLocked(recs...)
}

// recordLocked appends records, and counts them for budgets.
func (s *Service) recordLocked(recs ...Record) {
	if err := s.store.Append(recs, s.cfg.Location); err != nil {
		s.log.Warn("recording insights", "err", err)
	}
	for _, r := range recs {
		if s.dayOf(r) == s.day && r.Type != RecordAlert {
			s.today = append(s.today, r)
		}
		s.countPlanLocked(r)
	}
	s.dirty = true
}

func (s *Service) dayOf(r Record) string { return recordTime(r).In(s.cfg.Location).Format(dayLayout) }

// countPlanLocked adds a record to its plan's lifetime amounts.
func (s *Service) countPlanLocked(r Record) {
	var who Who
	var a Amounts
	switch {
	case r.Span != nil && r.Span.State == StateWorking:
		who, a.WorkingMS = r.Span.Who, r.Span.Duration().Milliseconds()
	case r.Usage != nil:
		who = r.Usage.Who
		if r.Usage.Tokens != nil {
			a.Tokens, a.TokensKnown = r.Usage.Tokens.Total(), true
		}
		if r.Usage.CostUSD != nil {
			a.CostUSD, a.CostKnown = *r.Usage.CostUSD, true
		}
	default:
		return
	}
	if who.Plan == "" {
		return
	}
	byScope := s.state.Plans[who.Plan]
	if byScope == nil {
		byScope = map[string]Amounts{}
		s.state.Plans[who.Plan] = byScope
	}
	k := who.Env + "\x00" + who.Kind
	byScope[k] = addAmounts(byScope[k], a)
}

func addAmounts(a, b Amounts) Amounts {
	return Amounts{
		CostUSD: a.CostUSD + b.CostUSD, Tokens: a.Tokens + b.Tokens, WorkingMS: a.WorkingMS + b.WorkingMS,
		CostKnown: a.CostKnown || b.CostKnown, TokensKnown: a.TokensKnown || b.TokensKnown,
	}
}

// rolloverLocked splits open spans at midnight and starts a new day.
func (s *Service) rolloverLocked(now time.Time) {
	day := now.Format(dayLayout)
	if day == s.day {
		return
	}
	midnight := startOfDay(now)
	for key, sp := range s.open {
		if sp.Start.Before(midnight) {
			prev := *sp
			prev.End = midnight
			s.closeLocked(prev)
			sp.Start = midnight
			s.open[key] = sp
		}
	}
	s.day, s.today = day, nil
	// Holds and levels of finished windows are forgotten.
	for _, m := range []map[string]bool{s.state.Held, s.state.Resumed} {
		for k := range m {
			if !strings.Contains(k, "\x00"+day+"\x00") && !strings.Contains(k, "\x00life\x00") {
				delete(m, k)
			}
		}
	}
	for k := range s.state.Raised {
		if !strings.Contains(k, "\x00"+day+"\x00") && !strings.Contains(k, "\x00life\x00") {
			delete(s.state.Raised, k)
		}
	}
	s.refreshHoldsLocked(now)
	s.dirty = true
}

// Notify is a Hive event: agents' states and tasks' become spans.
func (s *Service) Notify(ctx context.Context, typ string, data json.RawMessage) {
	if s.cfg.Disabled || s.ready(ctx) != nil {
		return
	}
	switch typ {
	case agent.EventState:
		var a agent.Agent
		if json.Unmarshal(data, &a) != nil || a.ID == "" {
			return
		}
		s.mu.Lock()
		_, known := s.tasks[a.ID]
		s.mu.Unlock()
		var plan, tsk string
		if !known && s.ports.Tasks != nil {
			plan, tsk = s.ports.Tasks.TaskOf(ctx, a.ID) // outside the lock: it takes the task service's
		}
		s.mu.Lock()
		if plan != "" {
			s.tasks[a.ID] = [2]string{plan, tsk}
		}
		s.agentStateLocked(a, s.now())
		alerts := s.checkLocked(s.now())
		s.mu.Unlock()
		s.publish(alerts)
	case task.EventTask, task.EventApproval:
		var v task.TaskView
		if json.Unmarshal(data, &v) != nil || v.PlanID == "" {
			return
		}
		s.mu.Lock()
		s.taskLocked(v, s.now())
		s.mu.Unlock()
	}
}

func (s *Service) agentStateLocked(a agent.Agent, now time.Time) {
	s.rolloverLocked(now)
	state := string(a.State)
	if a.State == agent.StateExited {
		s.endLocked(a.ID, now)
		delete(s.tasks, a.ID)
		return
	}
	who := s.whoOf(a)
	reason := reasonOf(a)
	if sp := s.open[a.ID]; sp != nil {
		if sp.State == state && sp.Who == who && sp.Reason == reason {
			return
		}
		s.endLocked(a.ID, now)
	}
	s.openLocked(a.ID, who, state, reason, now)
}

func (s *Service) taskLocked(v task.TaskView, now time.Time) {
	s.rolloverLocked(now)
	if v.AgentID != "" {
		pt := [2]string{v.PlanID, v.ID}
		if s.tasks[v.AgentID] != pt {
			s.tasks[v.AgentID] = pt
			// The agent's span is now its task's.
			if sp := s.open[v.AgentID]; sp != nil && sp.Plan != v.PlanID {
				w := sp.Who
				w.Plan, w.Task = v.PlanID, v.ID
				state, reason := sp.State, sp.Reason
				s.endLocked(v.AgentID, now)
				s.openLocked(v.AgentID, w, state, reason, now)
			}
		}
	}
	key := "approval:" + v.PlanID + "/" + v.ID
	waiting := v.State == task.StateApproval
	switch {
	case waiting && s.open[key] == nil:
		s.openLocked(key, Who{Agent: v.PlanID + "/" + v.ID, Kind: v.Kind, Env: v.Environment, Plan: v.PlanID, Task: v.ID},
			StateApproval, truncate("task "+v.ID+" of plan "+v.PlanID+" waits for approval", MaxReasonBytes), now)
	case !waiting && s.open[key] != nil:
		s.endLocked(key, now)
	}
}

// --- Usage ---

// UsageResult is what a report recorded.
type UsageResult struct {
	// Recorded is the usage recorded: what grew, or nil when nothing did.
	Recorded *Usage `json:"recorded,omitempty"`
	// Agent is who it was counted for.
	Agent Who `json:"agent"`
}

// ReportUsage records an agent's usage: what grew of its session's totals
// (Total), or the increment it gives.
func (s *Service) ReportUsage(ctx context.Context, r UsageReport) (UsageResult, error) {
	if err := s.ready(ctx); err != nil {
		return UsageResult{}, err
	}
	if r.ID == "" {
		return UsageResult{}, invalid("give the agent's pane or process ID")
	}
	if r.Tokens == nil && r.CostUSD == nil {
		return UsageResult{}, invalid("report tokens or a cost")
	}
	if r.Tokens != nil && (r.Tokens.Input < 0 || r.Tokens.Output < 0 || r.Tokens.CacheRead < 0 || r.Tokens.CacheWrite < 0) ||
		r.CostUSD != nil && (*r.CostUSD < 0 || *r.CostUSD != *r.CostUSD) {
		return UsageResult{}, invalid("negative numbers")
	}
	if s.ports.Agents == nil {
		return UsageResult{}, fmt.Errorf("%w: no agents here", ErrNotFound)
	}
	a, err := s.ports.Agents.Get(ctx, r.ID)
	if err != nil {
		return UsageResult{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if r.Source == "" {
		r.Source = SourceReport
	}
	r.Model = truncate(r.Model, 128)
	r.Session = truncate(r.Session, 256)
	session := r.Session
	if session == "" {
		session = a.ID
	}
	var plan, tsk string
	s.mu.Lock()
	_, known := s.tasks[a.ID]
	s.mu.Unlock()
	if !known && s.ports.Tasks != nil {
		plan, tsk = s.ports.Tasks.TaskOf(ctx, a.ID)
	}
	now := s.now()
	s.mu.Lock()
	defer func() {
		alerts := s.checkLocked(now)
		s.mu.Unlock()
		s.publish(alerts)
	}()
	if plan != "" {
		s.tasks[a.ID] = [2]string{plan, tsk}
	}
	s.rolloverLocked(now)
	who := s.whoOf(a)
	res := UsageResult{Agent: who}
	if s.cfg.Disabled {
		return res, nil
	}
	// A session's reports name its model once; later ones without one
	// are for the same model.
	if mk := a.ID + "\x00" + session; r.Model != "" {
		s.models[mk] = r.Model
	} else {
		r.Model = s.models[mk]
	}
	u := Usage{Who: who, Time: now, Session: r.Session, Model: r.Model, Source: r.Source}
	grew := false
	field := func(name string, v float64) float64 {
		if !r.Total {
			if v > 0 {
				grew = true
			}
			return v
		}
		key := a.ID + "\x00" + session + "\x00" + name
		prev, seen := s.state.Totals[key]
		s.state.Totals[key] = v
		d := v - prev
		switch {
		case v < prev:
			d = v // a new session under the same name
		case !seen:
			d = v
		}
		if d > 0 || !seen {
			grew = true
		}
		return max(d, 0)
	}
	if t := r.Tokens; t != nil {
		u.Tokens = &Tokens{
			Input: int64(field("input", float64(t.Input))), Output: int64(field("output", float64(t.Output))),
			CacheRead: int64(field("cache_read", float64(t.CacheRead))), CacheWrite: int64(field("cache_write", float64(t.CacheWrite))),
		}
	}
	if r.CostUSD != nil {
		c := field("cost", *r.CostUSD)
		u.CostUSD = &c
	}
	if !grew {
		return res, nil
	}
	s.recordLocked(Record{Type: RecordUsage, Usage: &u})
	res.Recorded = &u
	return res, nil
}

func (s *Service) publish(alerts []Alert) {
	for _, a := range alerts {
		s.log.Warn("budget "+a.Level, "budget", a.Budget, "key", a.Key, "message", a.Message)
		s.ports.Events.Publish(EventAlert, a)
	}
}
