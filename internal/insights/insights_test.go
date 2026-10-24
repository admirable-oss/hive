package insights

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/task"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// clock is a time tests move.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fakeAgents struct {
	mu   sync.Mutex
	list []agent.Agent
}

func (f *fakeAgents) List(context.Context, bool) ([]agent.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Agent(nil), f.list...), nil
}

func (f *fakeAgents) Get(_ context.Context, id string) (agent.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.list {
		if a.ID == id || a.PaneID == id {
			return a, nil
		}
	}
	return agent.Agent{}, errors.New("no agent " + id)
}

type fakeTasks map[string][2]string

func (f fakeTasks) TaskOf(_ context.Context, id string) (string, string) {
	pt := f[id]
	return pt[0], pt[1]
}

type recorder struct {
	mu     sync.Mutex
	alerts []Alert
	other  []string
}

func (r *recorder) Publish(typ string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := data.(Alert); ok {
		r.alerts = append(r.alerts, a)
		return
	}
	r.other = append(r.other, typ)
}

func (r *recorder) levels() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, a := range r.alerts {
		out = append(out, a.Budget+":"+a.Key+":"+a.Level)
	}
	return out
}

type env struct {
	t      *testing.T
	dir    string
	clock  *clock
	agents *fakeAgents
	tasks  fakeTasks
	events *recorder
	cfg    Config
	svc    *Service
}

// start is 2026-10-24 09:00 UTC, a Saturday.
func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	e := &env{
		t: t, dir: filepath.Join(t.TempDir(), "insights"),
		clock:  &clock{t: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)},
		agents: &fakeAgents{list: []agent.Agent{
			{ID: "x1", Kind: "claude", Name: "Claude Code", EnvironmentID: "api", PaneID: "p1", Status: agent.Status{State: agent.StateIdle}},
			{ID: "x2", Kind: "codex", Name: "Codex", EnvironmentID: "web", PaneID: "p2", Status: agent.Status{State: agent.StateIdle}},
			{ID: "x3", Kind: "gemini", Name: "Gemini", EnvironmentID: "api", PaneID: "p3", Status: agent.Status{State: agent.StateIdle}},
		}},
		tasks: fakeTasks{}, events: &recorder{},
	}
	cfg.Now, cfg.Location = e.clock.now, time.UTC
	e.cfg = cfg
	e.start()
	return e
}

func (e *env) start() {
	e.t.Helper()
	e.svc = NewService(e.cfg, NewStore(e.dir), Ports{Agents: e.agents, Tasks: e.tasks, Events: e.events})
	if err := e.svc.Load(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

// state sends an agent's state change.
func (e *env) state(id string, st agent.State, reason string) {
	e.t.Helper()
	e.agents.mu.Lock()
	var a agent.Agent
	for i := range e.agents.list {
		if e.agents.list[i].ID == id {
			e.agents.list[i].State, e.agents.list[i].Reason = st, reason
			a = e.agents.list[i]
		}
	}
	e.agents.mu.Unlock()
	raw, _ := json.Marshal(a)
	e.svc.Notify(context.Background(), agent.EventState, raw)
}

func (e *env) taskEvent(typ string, v task.TaskView) {
	raw, _ := json.Marshal(v)
	e.svc.Notify(context.Background(), typ, raw)
}

func (e *env) summary(by string) Summary {
	e.t.Helper()
	s, err := e.svc.Summary(context.Background(), Query{By: by})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func row(s Summary, key string) Row {
	for _, r := range s.Rows {
		if r.Key == key {
			return r
		}
	}
	return Row{}
}

func i64(v int64) *int64 { return &v }

func f64(v float64) *float64 { return &v }

func TestTimeInState(t *testing.T) {
	e := newEnv(t, Config{})
	e.clock.add(10 * time.Minute) // idle since 09:00
	e.state("x1", agent.StateWorking, "")
	e.clock.add(30 * time.Minute)
	e.state("x1", agent.StateBlocked, "asks to run go test ./...")
	e.clock.add(5 * time.Minute)
	e.state("x1", agent.StateWorking, "")
	e.clock.add(15 * time.Minute)
	e.state("x1", agent.StateDone, "")
	e.clock.add(time.Minute)
	e.state("x2", agent.StateExited, "")

	s := e.summary(ByKind)
	c := row(s, "claude")
	if c.StatesMS[StateIdle] != (10 * time.Minute).Milliseconds() || c.StatesMS[StateWorking] != (45*time.Minute).Milliseconds() ||
		c.StatesMS[StateBlocked] != (5*time.Minute).Milliseconds() || c.StatesMS[StateDone] != time.Minute.Milliseconds() {
		t.Fatalf("claude's time in state: %+v", c.StatesMS)
	}
	if c.WaitingMS != (5*time.Minute).Milliseconds() || c.Asks != 1 || c.Tokens != nil || c.CostUSD != nil || c.Agents != 1 {
		t.Fatalf("waiting, asks, and unknown usage: %+v", c)
	}
	if x := row(s, "codex"); x.StatesMS[StateIdle] != (61 * time.Minute).Milliseconds() {
		t.Fatalf("codex idle until it exited: %+v", x.StatesMS)
	}
	asks, err := e.svc.Asks(context.Background(), Query{}, 0)
	if err != nil || len(asks) != 1 || asks[0].Reason != "asks to run go test ./..." || asks[0].WaitedMS != (5*time.Minute).Milliseconds() || asks[0].Open {
		t.Fatalf("what it was asked: %+v %v", asks, err)
	}
	if byEnv := e.summary(ByEnvironment); row(byEnv, "api").Agents != 2 || row(byEnv, "web").Agents != 1 {
		t.Fatalf("by environment: %+v", byEnv.Rows)
	}
	e.state("x3", agent.StateBlocked, "Allow edits?")
	e.clock.add(time.Minute)
	if asks, _ := e.svc.Asks(context.Background(), Query{}, 0); len(asks) != 2 || !asks[0].Open || asks[0].Reason != "Allow edits?" {
		t.Fatalf("an open ask: %+v", asks)
	}
}

func TestSpansSplitAtMidnight(t *testing.T) {
	e := newEnv(t, Config{})
	e.clock.add(14*time.Hour + 30*time.Minute) // 23:30
	e.state("x1", agent.StateWorking, "")
	e.clock.add(time.Hour) // 00:30 the next day
	e.state("x1", agent.StateIdle, "")
	sat := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	byDay, err := e.svc.Summary(context.Background(), Query{By: ByDay, From: sat})
	if err != nil {
		t.Fatal(err)
	}
	if d := row(byDay, "2026-10-24").StatesMS[StateWorking]; d != (30 * time.Minute).Milliseconds() {
		t.Fatalf("Saturday's half: %d", d)
	}
	if d := row(byDay, "2026-10-25").StatesMS[StateWorking]; d != (30 * time.Minute).Milliseconds() {
		t.Fatalf("Sunday's half: %d", d)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "2026-10-25.jsonl")); err != nil {
		t.Fatalf("a file per day: %v", err)
	}
	if today := e.summary(ByKind); row(today, "claude").StatesMS[StateWorking] != (30 * time.Minute).Milliseconds() {
		t.Fatalf("today is Sunday: %+v", today.Rows)
	}
}

func TestTasksAndApprovals(t *testing.T) {
	e := newEnv(t, Config{})
	e.taskEvent(task.EventApproval, task.TaskView{PlanID: "3f2a", ID: "deploy", Kind: "codex", Environment: "web", State: task.StateApproval})
	e.clock.add(4 * time.Minute)
	e.taskEvent(task.EventTask, task.TaskView{PlanID: "3f2a", ID: "deploy", Kind: "codex", Environment: "web", State: task.StateRunning, AgentID: "x2"})
	e.state("x2", agent.StateWorking, "")
	e.clock.add(10 * time.Minute)
	e.state("x2", agent.StateIdle, "")
	s := e.summary(ByTask)
	r := row(s, "3f2a/deploy")
	if r.StatesMS[StateApproval] != (4*time.Minute).Milliseconds() || r.StatesMS[StateWorking] != (10*time.Minute).Milliseconds() || r.WaitingMS != (4*time.Minute).Milliseconds() {
		t.Fatalf("the task's approval and work: %+v", r)
	}
	asks, _ := e.svc.Asks(context.Background(), Query{}, 0)
	if len(asks) != 1 || asks[0].State != StateApproval || !strings.Contains(asks[0].Reason, "waits for approval") {
		t.Fatalf("the approval is an ask: %+v", asks)
	}
}

func TestUsageTotalsAndSources(t *testing.T) {
	e := newEnv(t, Config{})
	ctx := context.Background()
	report := func(r UsageReport) *Usage {
		t.Helper()
		res, err := e.svc.ReportUsage(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return res.Recorded
	}
	// The transcript's totals grow; only what grew is recorded.
	report(UsageReport{ID: "p1", Session: "s1", Source: SourceTranscript, Model: "claude-opus", Total: true, Tokens: &Tokens{Input: 100, Output: 50}})
	u := report(UsageReport{ID: "p1", Session: "s1", Source: SourceTranscript, Total: true, Tokens: &Tokens{Input: 150, Output: 50, CacheRead: 10}})
	if u == nil || u.Tokens.Input != 50 || u.Tokens.Output != 0 || u.Tokens.CacheRead != 10 || u.Agent != "x1" || u.Kind != "claude" {
		t.Fatalf("what grew: %+v", u)
	}
	if u := report(UsageReport{ID: "p1", Session: "s1", Source: SourceTranscript, Total: true, Tokens: &Tokens{Input: 150, Output: 50, CacheRead: 10}}); u != nil {
		t.Fatalf("the same totals record nothing: %+v", u)
	}
	// The status line gives the cost of the same session.
	report(UsageReport{ID: "p1", Session: "s1", Source: SourceStatusline, Total: true, CostUSD: f64(0.5)})
	report(UsageReport{ID: "p1", Session: "s1", Source: SourceStatusline, Total: true, CostUSD: f64(1.25)})
	// A new session's totals start again.
	report(UsageReport{ID: "p1", Session: "s2", Source: SourceTranscript, Total: true, Tokens: &Tokens{Input: 20}})
	// Codex reports increments.
	report(UsageReport{ID: "x2", Tokens: &Tokens{Output: 7}})
	report(UsageReport{ID: "x2", Tokens: &Tokens{Output: 3}, CostUSD: f64(0.1)})

	s := e.summary(ByKind)
	c, x, g := row(s, "claude"), row(s, "codex"), row(s, "gemini")
	if c.Tokens == nil || c.Tokens.Input != 170 || c.Tokens.Output != 50 || c.Tokens.CacheRead != 10 || *c.CostUSD != 1.25 {
		t.Fatalf("claude: %+v %v", c.Tokens, c.CostUSD)
	}
	if strings.Join(c.Sources, ",") != "statusline,transcript" || strings.Join(c.Models, ",") != "claude-opus" {
		t.Fatalf("claude's sources and models: %v %v", c.Sources, c.Models)
	}
	if x.Tokens.Output != 10 || *x.CostUSD != 0.1 || x.Sources[0] != SourceReport {
		t.Fatalf("codex: %+v", x)
	}
	if g.Tokens != nil || g.CostUSD != nil {
		t.Fatalf("gemini reported nothing: unknown, not zero: %+v", g)
	}
	if byModel := e.summary(ByModel); row(byModel, "claude-opus").Tokens.Input != 150 || row(byModel, "(none)").Tokens == nil {
		t.Fatalf("by model: %+v", byModel.Rows)
	}

	// A first report of zero is known usage.
	report(UsageReport{ID: "p3", Total: true, Tokens: &Tokens{}})
	if g := row(e.summary(ByKind), "gemini"); g.Tokens == nil || g.Tokens.Total() != 0 {
		t.Fatalf("known zero: %+v", g)
	}

	for _, bad := range []UsageReport{{ID: ""}, {ID: "p1"}, {ID: "p1", Tokens: &Tokens{Input: -1}}, {ID: "nobody", Tokens: &Tokens{}}} {
		if _, err := e.svc.ReportUsage(ctx, bad); err == nil {
			t.Errorf("%+v: accepted", bad)
		}
	}
}

func TestBudgetsWarnHoldAndResume(t *testing.T) {
	e := newEnv(t, Config{Budgets: []Budget{
		{Name: "daily", Per: PerDay, Limits: Limits{CostUSD: 10}, Action: ActionPause},
		{Name: "web", Per: PerEnvironment, Environment: "web", Limits: Limits{WorkingMS: time.Hour.Milliseconds()}},
		{Name: "plans", Per: PerPlan, Limits: Limits{Tokens: 1000}, Action: ActionPause},
	}})
	ctx := context.Background()
	cost := func(id string, v float64) {
		t.Helper()
		if _, err := e.svc.ReportUsage(ctx, UsageReport{ID: id, CostUSD: f64(v)}); err != nil {
			t.Fatal(err)
		}
	}
	cost("p1", 7)
	if l := e.events.levels(); len(l) != 0 {
		t.Fatalf("under its warning: %v", l)
	}
	cost("p1", 1.5)
	if l := e.events.levels(); strings.Join(l, " ") != "daily:today:warning" {
		t.Fatalf("a warning at 85%%: %v", l)
	}
	cost("p2", 0.1)
	if l := e.events.levels(); len(l) != 1 {
		t.Fatalf("a warning once: %v", l)
	}
	plan := &task.Plan{ID: "3f2a"}
	tk := &task.Task{Spec: task.Spec{ID: "api", Kind: "claude", EnvironmentID: "api"}}
	if h := e.svc.Hold(plan, tk); h != "" {
		t.Fatalf("not held yet: %q", h)
	}
	cost("p2", 2)
	l := e.events.levels()
	if len(l) != 2 || l[1] != "daily:today:exceeded" || !e.events.alerts[1].Held {
		t.Fatalf("exceeded, and held: %v", l)
	}
	if h := e.svc.Hold(plan, tk); !strings.Contains(h, "held by budget daily") {
		t.Fatalf("new tasks are held: %q", h)
	}

	// Working time in web counts open spans as they go.
	e.state("x2", agent.StateWorking, "")
	e.clock.add(50 * time.Minute)
	e.svc.mu.Lock()
	alerts := e.svc.checkLocked(e.clock.now())
	e.svc.mu.Unlock()
	if len(alerts) != 1 || alerts[0].Budget != "web" || alerts[0].Key != "web" || alerts[0].Level != LevelWarning {
		t.Fatalf("web's working time: %+v", alerts)
	}

	st, err := e.svc.Resume(ctx, "daily", "")
	if err != nil || len(st) != 1 || st[0].Held || !st[0].Resumed {
		t.Fatalf("resumed: %+v %v", st, err)
	}
	if h := e.svc.Hold(plan, tk); h != "" {
		t.Fatalf("resumed, nothing is held: %q", h)
	}
	cost("p1", 5)
	if h := e.svc.Hold(plan, tk); h != "" {
		t.Fatalf("a resumed budget holds no more this window: %q", h)
	}
	if _, err := e.svc.Resume(ctx, "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown budget: %v", err)
	}

	// A plan's budget holds only its own tasks.
	e.tasks["x3"] = [2]string{"77aa", "lint"}
	if _, err := e.svc.ReportUsage(ctx, UsageReport{ID: "p3", Tokens: &Tokens{Output: 1200}}); err != nil {
		t.Fatal(err)
	}
	if h := e.svc.Hold(&task.Plan{ID: "77aa"}, &task.Task{Spec: task.Spec{Kind: "gemini"}}); !strings.Contains(h, "plan 77aa") {
		t.Fatalf("the plan over its budget is held: %q", h)
	}
	if h := e.svc.Hold(&task.Plan{ID: "other"}, &task.Task{Spec: task.Spec{Kind: "gemini"}}); h != "" {
		t.Fatalf("another plan is not: %q", h)
	}

	// The next day, the daily budget starts again.
	e.clock.add(24 * time.Hour)
	e.svc.Notify(ctx, "agent.state", json.RawMessage(`{}`))
	e.state("x1", agent.StateIdle, "")
	for _, b := range e.svc.Budgets(ctx) {
		if b.Budget.Name == "daily" && (b.Used.CostUSD != 0 || b.Held || b.Level == LevelWarning || b.Level == LevelExceeded) {
			t.Fatalf("a new day: %+v", b)
		}
	}
}

func TestUnenforceableBudget(t *testing.T) {
	e := newEnv(t, Config{Budgets: []Budget{{Name: "spend", Per: PerKind, Kind: "gemini", Limits: Limits{CostUSD: 1}, Action: ActionPause}}})
	e.state("x3", agent.StateWorking, "")
	e.clock.add(time.Minute)
	e.state("x3", agent.StateIdle, "")
	l := e.events.levels()
	if len(l) != 1 || l[0] != "spend:gemini:unenforceable" {
		t.Fatalf("a cost limit nothing reports: %v", l)
	}
	if h := e.svc.Hold(&task.Plan{ID: "p"}, &task.Task{Spec: task.Spec{Kind: "gemini"}}); h != "" {
		t.Fatalf("unknown usage holds nothing: %q", h)
	}
}

func TestRestartClosesOpenSpansWhenItStopped(t *testing.T) {
	e := newEnv(t, Config{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	e.state("x1", agent.StateWorking, "")
	e.clock.add(20 * time.Minute)
	cancel()
	<-done // saves the open spans at 09:20

	e.clock.add(40 * time.Minute) // no daemon for 40 minutes
	e.agents.list[0].State = agent.StateWorking
	e.start()
	e.clock.add(10 * time.Minute)
	c := row(e.summary(ByKind), "claude")
	if c.StatesMS[StateWorking] != (30 * time.Minute).Milliseconds() {
		t.Fatalf("20 minutes before the stop and 10 after, not the time between: %+v", c.StatesMS)
	}
}

func TestStoreAndExport(t *testing.T) {
	e := newEnv(t, Config{KeepDays: 2})
	ctx := context.Background()
	e.state("x1", agent.StateWorking, "")
	e.clock.add(time.Minute)
	e.state("x1", agent.StateIdle, "")
	if _, err := e.svc.ReportUsage(ctx, UsageReport{ID: "p1", Tokens: &Tokens{Input: 5}}); err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(e.dir, "2026-10-24.jsonl")
	f, err := os.OpenFile(day, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{broken\n{\"v\": 9, \"type\": \"span\", \"span\": {}}\n")
	_ = f.Close()
	spans, err := e.svc.Export(ctx, Query{}, What("spans"))
	if err != nil || len(spans) < 2 {
		t.Fatalf("spans, the broken and newer lines skipped: %+v %v", spans, err)
	}
	usage, _ := e.svc.Export(ctx, Query{}, What("usage"))
	if len(usage) != 1 || usage[0].Usage.Tokens.Input != 5 {
		t.Fatalf("usage: %+v", usage)
	}
	if _, err := e.svc.Export(ctx, Query{}, "screens"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown what: %v", err)
	}

	// Old days are forgotten.
	old := filepath.Join(e.dir, "2026-10-01.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if gone := e.svc.store.Prune(2, e.clock.now()); len(gone) != 1 || gone[0] != "2026-10-01" {
		t.Fatalf("pruned: %v", gone)
	}

	// A newer hive's state is left alone; a broken one moved aside.
	state := filepath.Join(e.dir, "state.json")
	if err := os.WriteFile(state, []byte(`{"schema": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewStore(e.dir)
	if _, err := st.LoadState(); err == nil {
		t.Fatal("a newer state loaded")
	}
	_ = st.SaveState(State{})
	if data, _ := os.ReadFile(state); !strings.Contains(string(data), "99") {
		t.Fatal("a newer state was written over")
	}
	if err := os.WriteFile(state, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(e.dir).LoadState(); err == nil {
		t.Fatal("a broken state loaded")
	}
	if m, _ := filepath.Glob(state + ".corrupt-*"); len(m) != 1 {
		t.Fatal("a broken state is moved aside")
	}
}

func TestObserverScope(t *testing.T) {
	e := newEnv(t, Config{})
	e.state("x1", agent.StateWorking, "")
	e.state("x2", agent.StateWorking, "")
	e.clock.add(time.Minute)
	s, err := e.svc.Summary(context.Background(), Query{Sees: func(env, _ string) bool { return env == "web" }})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rows) != 1 || s.Rows[0].Key != "codex" {
		t.Fatalf("only what the scope sees: %+v", s.Rows)
	}
}

func TestDisabledRecordsNothing(t *testing.T) {
	e := newEnv(t, Config{Disabled: true})
	e.state("x1", agent.StateWorking, "")
	e.clock.add(time.Minute)
	if s := e.summary(ByKind); len(s.Rows) != 0 {
		t.Fatalf("disabled: %+v", s.Rows)
	}
}

const claudeTranscript = `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"id":"m1","model":"claude-opus-4","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}
{"type":"assistant","message":{"id":"m1","model":"claude-opus-4","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}
{"type":"assistant","message":{"id":"m2","model":"claude-opus-4","usage":{"input_tokens":3,"output_tokens":40,"cache_read_input_tokens":120,"cache_creation_input_tokens":0}}}
not json
`

const codexTranscript = `{"type":"session_meta","payload":{"id":"abc"}}
{"type":"turn_context","payload":{"model":"gpt-5-codex"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":200}}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1500,"cached_input_tokens":900,"output_tokens":350}}}}
`

func TestTranscripts(t *testing.T) {
	c, ok := ReadTranscript(strings.NewReader(claudeTranscript))
	if !ok || c.Format != "claude" || c.Model != "claude-opus-4" || c.Tokens != (Tokens{Input: 13, Output: 45, CacheRead: 220, CacheWrite: 20}) {
		t.Fatalf("claude: %+v %v", c, ok)
	}
	x, ok := ReadTranscript(strings.NewReader(codexTranscript))
	if !ok || x.Format != "codex" || x.Model != "gpt-5-codex" || x.Tokens != (Tokens{Input: 600, CacheRead: 900, Output: 350}) {
		t.Fatalf("codex: %+v %v", x, ok)
	}
	if _, ok := ReadTranscript(strings.NewReader("hello\n{\"type\":\"x\"}\n")); ok {
		t.Fatal("an unknown format has unknown usage")
	}
}

func FuzzReadTranscript(f *testing.F) {
	f.Add([]byte(claudeTranscript))
	f.Add([]byte(codexTranscript))
	f.Fuzz(func(t *testing.T, data []byte) {
		tr, ok := ReadTranscript(strings.NewReader(string(data)))
		if !ok && tr != (Transcript{}) {
			t.Fatalf("unknown usage carries numbers: %+v", tr)
		}
		if tr.Tokens.Input < 0 || tr.Tokens.CacheRead < 0 {
			t.Fatalf("negative tokens: %+v", tr)
		}
	})
}
