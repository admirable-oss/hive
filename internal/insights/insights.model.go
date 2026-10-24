package insights

import (
	"time"
)

// schema is the state file format this build writes (and the newest it
// reads); lineVersion is the day files' record format.
const (
	schema      = 1
	lineVersion = 1
)

// Defaults and bounds.
const (
	DefaultKeepDays = 90
	DefaultWarnAt   = 0.8
	// MaxReasonBytes bounds what a blocked agent was asked, as kept.
	MaxReasonBytes = 512
	// MaxRows bounds a summary's rows and MaxAsks a list of asks.
	MaxRows = 1000
	MaxAsks = 1000
	// MaxRecords bounds an export.
	MaxRecords = 200000
	// MaxTranscriptBytes bounds the transcript read for a session's usage.
	MaxTranscriptBytes = 256 << 20
)

// States spans are recorded in: an agent's (package agent's) and a task
// waiting for approval.
const (
	StateWorking  = "working"
	StateBlocked  = "blocked"
	StateIdle     = "idle"
	StateDone     = "done"
	StateUnknown  = "unknown"
	StateApproval = "approval"
)

// Sources of usage numbers.
const (
	SourceTranscript = "transcript"
	SourceStatusline = "statusline"
	SourceReport     = "report"
)

// Who an agent is, as records keep it.
type Who struct {
	Agent string `json:"agent"` // its process ID; for approvals, plan/task
	Pane  string `json:"pane,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Name  string `json:"name,omitempty"`
	Env   string `json:"environment,omitempty"`
	Plan  string `json:"plan,omitempty"`
	Task  string `json:"task,omitempty"`
}

// Span is a time an agent (or a task waiting for approval) spent in one
// state, within one local day.
type Span struct {
	Who
	State string    `json:"state"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Reason is what a blocked agent asked, or why it is in its state.
	Reason string `json:"reason,omitempty"`
}

// Duration is how long the span lasted.
func (s Span) Duration() time.Duration { return s.End.Sub(s.Start) }

// Tokens are counts of tokens.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// Total is every token counted.
func (t Tokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

func (t Tokens) add(o Tokens) Tokens {
	return Tokens{Input: t.Input + o.Input, Output: t.Output + o.Output, CacheRead: t.CacheRead + o.CacheRead, CacheWrite: t.CacheWrite + o.CacheWrite}
}

// Usage is usage an agent reported, as recorded: what grew since the last
// report (or the increment it gave).
type Usage struct {
	Who
	Time    time.Time `json:"time"`
	Session string    `json:"session,omitempty"`
	Model   string    `json:"model,omitempty"`
	Source  string    `json:"source"`
	// Tokens is nil when the report gave none; CostUSD likewise.
	Tokens  *Tokens  `json:"tokens,omitempty"`
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

// Amounts is what a budget counts.
type Amounts struct {
	CostUSD   float64 `json:"cost_usd"`
	Tokens    int64   `json:"tokens"`
	WorkingMS int64   `json:"working_ms"`
	// CostKnown and TokensKnown say something reported them.
	CostKnown   bool `json:"cost_known"`
	TokensKnown bool `json:"tokens_known"`
}

// Alert is a budget that reached a level.
type Alert struct {
	Time   time.Time `json:"time"`
	Budget string    `json:"budget"`
	Per    string    `json:"per"`
	Key    string    `json:"key"`
	Window string    `json:"window"`
	Level  string    `json:"level"` // warning | exceeded | unenforceable
	Used   Amounts   `json:"used"`
	Limit  Limits    `json:"limit"`
	Action string    `json:"action"`
	// Held says new work in its scope is held.
	Held    bool   `json:"held,omitempty"`
	Message string `json:"message"`
}

// Alert levels.
const (
	LevelWarning       = "warning"
	LevelExceeded      = "exceeded"
	LevelUnenforceable = "unenforceable"
)

// Record is one line of a day file: one of a span, usage or an alert.
type Record struct {
	V     int    `json:"v"`
	Type  string `json:"type"` // span | usage | alert
	Span  *Span  `json:"span,omitempty"`
	Usage *Usage `json:"usage,omitempty"`
	Alert *Alert `json:"alert,omitempty"`
}

// Record types.
const (
	RecordSpan  = "span"
	RecordUsage = "usage"
	RecordAlert = "alert"
)

// --- Budgets ---

// Per is what a budget applies to, each separately.
const (
	PerDay         = "day"
	PerPlan        = "plan"
	PerEnvironment = "environment"
	PerKind        = "kind"
)

// Pers lists what a budget can apply to.
var Pers = []string{PerDay, PerPlan, PerEnvironment, PerKind}

// Actions a budget takes once exceeded.
const (
	ActionWarn  = "warn"
	ActionPause = "pause"
)

// Limits are a budget's limits; zero is none.
type Limits struct {
	CostUSD   float64 `json:"cost_usd,omitempty"`
	Tokens    int64   `json:"tokens,omitempty"`
	WorkingMS int64   `json:"working_ms,omitempty"`
}

// Budget is one [[insights.budget]].
type Budget struct {
	Name        string `json:"name"`
	Per         string `json:"per"`
	Environment string `json:"environment,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Limits
	WarnAt float64 `json:"warn_at,omitempty"`
	Action string  `json:"action,omitempty"`
}

// BudgetStatus is a budget's use for one key of its window.
type BudgetStatus struct {
	Budget   Budget  `json:"budget"`
	Key      string  `json:"key"`
	Window   string  `json:"window"`
	Used     Amounts `json:"used"`
	Fraction float64 `json:"fraction"`
	Level    string  `json:"level,omitempty"`
	Held     bool    `json:"held,omitempty"`
	Resumed  bool    `json:"resumed,omitempty"`
}

// --- Views ---

// Query selects records: a range, a grouping, and filters.
type Query struct {
	From, To    time.Time
	By          string
	Kind        string
	Environment string
	// Sees, when set, keeps only what it accepts (an observer's scope):
	// environment and pane.
	Sees func(env, pane string) bool
}

// Groupings a summary's rows can have.
const (
	ByKind        = "kind"
	ByAgent       = "agent"
	ByEnvironment = "environment"
	ByTask        = "task"
	ByDay         = "day"
	ByModel       = "model"
)

// Bys lists the groupings.
var Bys = []string{ByKind, ByAgent, ByEnvironment, ByTask, ByDay, ByModel}

// Summary is time and usage over a range, by a grouping.
type Summary struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	By    string    `json:"by"`
	Rows  []Row     `json:"rows"`
	Total Row       `json:"total"`
}

// Row is one group's time and usage.
type Row struct {
	Key string `json:"key"`
	// StatesMS is the time spent in each state, in milliseconds.
	StatesMS map[string]int64 `json:"states_ms"`
	// WaitingMS is the time waiting on a person: blocked, and tasks
	// waiting for approval; Asks counts them.
	WaitingMS int64 `json:"waiting_ms"`
	Asks      int   `json:"asks"`
	// Tokens and CostUSD are nil when nothing reported them: unknown,
	// not zero.
	Tokens  *Tokens  `json:"tokens,omitempty"`
	CostUSD *float64 `json:"cost_usd,omitempty"`
	// Sources are where its usage numbers came from.
	Sources []string `json:"sources,omitempty"`
	// Models are the models reported.
	Models []string `json:"models,omitempty"`
	Agents int      `json:"agents"`
}

// Ask is a time an agent waited on a person: what it asked, and for how
// long.
type Ask struct {
	Who
	State    string    `json:"state"` // blocked | approval
	Start    time.Time `json:"start"`
	End      time.Time `json:"end,omitzero"`
	WaitedMS int64     `json:"waited_ms"`
	Reason   string    `json:"reason,omitempty"`
	// Open says it still waits.
	Open bool `json:"open,omitempty"`
}

// UsageReport is a report of usage (insights.usage).
type UsageReport struct {
	// ID is the agent's pane or process.
	ID      string
	Session string
	Model   string
	Source  string
	Tokens  *Tokens
	CostUSD *float64
	// Total says the numbers are the session's totals so far (what
	// agents report); otherwise they are an increment.
	Total bool
}

// Events, published on the bus as insights.*.
const (
	// EventAlert says a budget reached a level: an Alert.
	EventAlert = "insights.alert"
	// EventResumed says a held budget was resumed: {"budget", "key"}.
	EventResumed = "insights.resumed"
)
