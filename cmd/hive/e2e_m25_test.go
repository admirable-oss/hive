//go:build !windows

package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/task"
)

// fakeHookClaude stands in for Claude Code with Hive's hooks installed:
// they report its state, and on SessionStart and Stop (state idle) the
// path of its transcript, to which each turn appends an assistant message
// with its usage. A prompt is a bracketed paste of any number of lines.
const fakeHookClaude = `#!/bin/sh
t="$PWD/transcript-$HIVE_PANE_ID.jsonl"
hook() {
	printf '{"session_id":"claude-%s","transcript_path":"%s","hook_event_name":"%s"}' "$HIVE_PANE_ID" "$t" "$2" |
		HIVE_CLI_TEST_EXEC=1 "$HIVE_BIN" pane report-agent --hook --state "$1" --ttl 10m
}
n=0
msg() {
	n=$((n+1))
	printf '{"type":"assistant","message":{"id":"msg_%s","model":"claude-fake-1","usage":{"input_tokens":%s,"output_tokens":%s,"cache_read_input_tokens":%s,"cache_creation_input_tokens":0}}}\n' "$n" "$1" "$2" "$3" >> "$t"
}
printf '\033[?2004h\033[H\033[2J  Claude Code (fake)\r\n> '
msg 1000 100 0
hook idle SessionStart
while IFS= read -r line; do
	case "$line" in *"201~"*) ;; *) continue ;; esac
	hook working UserPromptSubmit
	printf '\r\n  working\r\n'
	sleep 1
	msg 3000 500 2000
	printf '  done\r\n> '
	hook idle Stop
done
`

// fakeSilentGemini reports its state through hooks, and no usage at all.
const fakeSilentGemini = `#!/bin/sh
hook() { printf '{"session_id":"gemini-%s"}' "$HIVE_PANE_ID" | HIVE_CLI_TEST_EXEC=1 "$HIVE_BIN" pane report-agent --hook --state "$1" --ttl 10m; }
printf '\033[?2004h\033[H\033[2J  Gemini (fake)\r\n> '
hook idle
while IFS= read -r line; do
	case "$line" in *"201~"*) ;; *) continue ;; esac
	hook working
	sleep 1
	printf '\r\n  done\r\n> '
	hook idle
done
`

// fakeReportingCodex is M22's fake Codex that reports what each prompt
// cost with `hive insights report`, as a script or plugin of a user's
// would.
var fakeReportingCodex = strings.Replace(fakeTaskAgent, `	printf '%s\n' "$task" >> result.txt`,
	`	printf '%s\n' "$task" >> result.txt
	HIVE_CLI_TEST_EXEC=1 "$HIVE_BIN" insights report --input 1200 --output 300 --cost 0.3 --model gpt-fake >/dev/null 2>&1`, 1)

const m25Plan = `
name = "m25 work"
max_parallel = 3

[defaults]
environment = "proj"
timeout = "2m"

[[task]]
id = "api"
kind = "codex"
prompt = "build the api"

[[task]]
id = "tests"
kind = "codex"
needs = ["api"]
keep_agent = true
prompt = "run the tests ASK FIRST"

[[task]]
id = "docs"
kind = "claude"
keep_agent = true
prompt = "write the docs"

[[task]]
id = "lint"
kind = "gemini"
prompt = "lint it"
`

const m25Next = `
name = "m25 next"

[[task]]
id = "next"
kind = "codex"
environment = "proj"
timeout = "2m"
prompt = "the next thing"
`

// TestM25Insights is M25's acceptance: after agents work, block on a
// question and are answered in tasks of a plan, Hive shows time in each
// state and the reported usage of each agent kind with its source
// (unknown for the one that reports nothing), raises a budget's warning
// and exceeded alerts, holds the next plan's task until the budget is
// resumed, and exports the data. Nothing is written outside HIVE_HOME.
func TestM25Insights(t *testing.T) {
	c := newCLI(t)
	// A home of its own, and nothing else: nothing may be written there.
	home := t.TempDir()
	c.env = append(c.env, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"), "XDG_STATE_HOME="+filepath.Join(home, ".local", "state"))
	cfg := `[terminal]
shell = "/bin/sh"
[tasks]
capacity = 3

[[insights.budget]]
name = "daily"
per = "day"
cost = 1.0
warn_at = 0.5
action = "pause"
`
	if err := os.WriteFile(c.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(c.home, "fake-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"codex": fakeReportingCodex, "claude": fakeHookClaude, "gemini": fakeSilentGemini} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c.must("env", "create", "proj", "--managed")

	runPlan := func(text string) task.Plan {
		t.Helper()
		f := filepath.Join(t.TempDir(), "plan.toml")
		if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		var p task.Plan
		decode(t, c.must("--json", "task", "run", f), &p)
		return p
	}
	get := func(id string) task.Plan {
		t.Helper()
		var p task.Plan
		decode(t, c.must("--json", "task", "get", id), &p)
		return p
	}
	budget := func() insights.BudgetStatus {
		t.Helper()
		var st []insights.BudgetStatus
		decode(t, c.must("--json", "insights", "budgets"), &st)
		if len(st) != 1 || st[0].Budget.Name != "daily" {
			t.Fatalf("budgets: %+v", st)
		}
		return st[0]
	}

	// The plan: Codex builds, then asks before running the tests and is
	// answered; Claude and Gemini work beside it.
	work := runPlan(m25Plan)
	c.waitForUpTo(2*time.Minute, "the tests' agent to ask", func() bool { p := get(work.ID); return p.Task("tests").State == task.StateBlocked })
	time.Sleep(time.Second) // it waits on us a while
	asked := get(work.ID)
	c.must("agent", "send-keys", asked.Task("tests").PaneID, "Enter")
	if out, errOut, code := c.run("task", "wait", work.ID, "--timeout", "2m"); code != 0 {
		t.Fatalf("plan: exit %d: %s %s\n%s", code, out, errOut, c.must("task", "report", work.ID, "--screen", "5"))
	}
	p := get(work.ID)
	codexPane, claudePane := p.Task("tests").PaneID, p.Task("docs").PaneID

	// Two Codex prompts at $0.30 each: over half the daily budget, a
	// warning. A warning holds nothing.
	c.waitFor("the budget's warning", func() bool { return budget().Level == insights.LevelWarning })
	if b := budget(); b.Held || !b.Used.CostKnown || b.Used.CostUSD < 0.59 || b.Used.CostUSD > 0.61 {
		t.Fatalf("after the plan: %+v", b)
	}

	// Claude's status line reports the session's cost, and shows the budget.
	line := c.statusline(claudePane, `{"session_id":"claude-s","model":{"id":"claude-fake-1","display_name":"Fake Opus"},"cost":{"total_cost_usd":0.25}}`)
	if line != "Fake Opus · $0.25 · budget daily 85%" {
		t.Fatalf("status line: %q", line)
	}
	// It reports the same total again on every redraw: counted once.
	c.statusline(claudePane, `{"session_id":"claude-s","model":{"id":"claude-fake-1"},"cost":{"total_cost_usd":0.25}}`)

	// One more report takes it over: exceeded, and new tasks are held.
	c.must("insights", "report", "--pane", codexPane, "--cost", "0.5")
	c.waitFor("the budget to hold", func() bool { b := budget(); return b.Level == insights.LevelExceeded && b.Held })
	next := runPlan(m25Next)
	c.waitFor("the next task to be held", func() bool {
		p := get(next.ID)
		tk := p.Task("next")
		return tk.State == task.StateQueued && strings.HasPrefix(tk.Reason, "held by budget daily")
	})
	time.Sleep(time.Second)
	held := get(next.ID)
	if tk := held.Task("next"); tk.State != task.StateQueued || tk.Attempt != 0 {
		t.Fatalf("a held task stays queued: %+v", tk)
	}
	out := c.must("insights", "resume", "daily")
	if !strings.Contains(out, "resumed until the window ends") {
		t.Fatalf("resume: %s", out)
	}
	if out, errOut, code := c.run("task", "wait", next.ID, "--timeout", "2m"); code != 0 {
		t.Fatalf("the resumed plan: exit %d: %s %s", code, out, errOut)
	}

	// The summary by kind: time in each state, usage with its source, and
	// unknown for the agent that reported nothing.
	var sum insights.Summary
	decode(t, c.must("--json", "insights"), &sum)
	rows := map[string]insights.Row{}
	for _, r := range sum.Rows {
		rows[r.Key] = r
	}
	codex, claude, gemini := rows["codex"], rows["claude"], rows["gemini"]
	for kind, r := range map[string]insights.Row{"codex": codex, "claude": claude, "gemini": gemini} {
		if r.StatesMS[insights.StateWorking] <= 0 {
			t.Errorf("%s worked: %+v", kind, r)
		}
	}
	if codex.StatesMS[insights.StateBlocked] < 500 || codex.WaitingMS < 500 || codex.Asks < 1 {
		t.Errorf("codex waited on us: %+v", codex)
	}
	if codex.CostUSD == nil || *codex.CostUSD < 1.39 || *codex.CostUSD > 1.41 || codex.Tokens == nil || codex.Tokens.Input != 3*1200 ||
		!slices.Equal(codex.Sources, []string{insights.SourceReport}) || !slices.Equal(codex.Models, []string{"gpt-fake"}) {
		t.Errorf("codex's reported usage: cost %v tokens %+v sources %v models %v", codex.CostUSD, codex.Tokens, codex.Sources, codex.Models)
	}
	// Claude's transcript: the session start's message and the turn's, each
	// counted once however often the hooks pass it.
	if claude.Tokens == nil || *claude.Tokens != (insights.Tokens{Input: 4000, Output: 600, CacheRead: 2000}) || claude.CostUSD == nil || *claude.CostUSD != 0.25 ||
		!slices.Equal(claude.Sources, []string{insights.SourceStatusline, insights.SourceTranscript}) {
		t.Errorf("claude's usage: tokens %+v cost %v sources %v", claude.Tokens, claude.CostUSD, claude.Sources)
	}
	if gemini.Tokens != nil || gemini.CostUSD != nil || len(gemini.Sources) != 0 {
		t.Errorf("gemini reported nothing: its usage is unknown, not zero: %+v", gemini)
	}

	// As a person reads it.
	human := c.must("insights")
	for _, want := range []string{
		"Today (", "by kind", "WORKING", "BLOCKED", "WAITING ON YOU", "codex", "claude", "gemini", "unknown", "report",
		"statusline, transcript", "$1.40", "Waiting on you", "waited", "Budgets:", "daily", "exceeded", "resumed until the window ends",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("hive insights lacks %q:\n%s", want, human)
		}
	}
	var asks []insights.Ask
	decode(t, c.must("--json", "insights", "asks"), &asks)
	if i := slices.IndexFunc(asks, func(a insights.Ask) bool {
		return a.Kind == "codex" && a.State == insights.StateBlocked && a.Task == "tests" && a.Reason != "" && a.WaitedMS >= 500
	}); i < 0 {
		t.Errorf("the question codex asked: %+v", asks)
	}

	// Exports: spans as CSV, usage and alerts as JSON.
	dir := t.TempDir()
	spansCSV := filepath.Join(dir, "spans.csv")
	c.must("insights", "export", "--what", "spans", "--format", "csv", "-o", spansCSV)
	f, err := os.Open(spansCSV)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(f).ReadAll()
	_ = f.Close()
	if err != nil || len(recs) < 5 || strings.Join(recs[0], ",") != "agent,pane,kind,name,environment,plan,task,state,start,end,duration_ms,reason" {
		t.Fatalf("spans CSV: %v %v", err, recs)
	}
	var usage []insights.Usage
	decode(t, c.must("insights", "export", "--what", "usage", "--format", "json"), &usage)
	sources := map[string]bool{}
	for _, u := range usage {
		sources[u.Source] = true
	}
	if !sources[insights.SourceTranscript] || !sources[insights.SourceStatusline] || !sources[insights.SourceReport] {
		t.Errorf("usage export's sources: %v", sources)
	}
	var alerts []insights.Alert
	decode(t, c.must("insights", "export", "--what", "alerts", "--format", "json"), &alerts)
	// Before any cost was reported (Claude's transcript has tokens only),
	// the budget could not be enforced, and said so.
	var levels []string
	pausing := false
	for _, a := range alerts {
		levels = append(levels, a.Level)
		pausing = pausing || a.Level == insights.LevelExceeded && a.Held
	}
	if !slices.Equal(levels, []string{insights.LevelUnenforceable, insights.LevelWarning, insights.LevelExceeded}) || !pausing {
		t.Errorf("each level alerted once, in order: %+v", alerts)
	}
	summaryCSV := c.must("insights", "export", "--what", "summary", "--format", "csv")
	if !strings.HasPrefix(summaryCSV, "kind,from,to,agents,working_ms,") || !strings.Contains(summaryCSV, "\ngemini,") {
		t.Errorf("summary CSV:\n%s", summaryCSV)
	}

	// Kept in HIVE_HOME; nothing written anywhere else.
	if days, _ := filepath.Glob(filepath.Join(c.home, "insights", "*.jsonl")); len(days) == 0 {
		t.Error("no day file in HIVE_HOME/insights")
	}
	var outside []string
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && path != home {
			outside = append(outside, path)
		}
		return nil
	})
	if len(outside) > 0 {
		t.Errorf("written outside HIVE_HOME: %v", outside)
	}
}

// statusline runs `hive insights statusline` in pane, with what Claude
// Code passes it, and returns the line it prints.
func (c *cli) statusline(pane, input string) string {
	c.t.Helper()
	cmd := exec.Command(os.Args[0], "insights", "statusline")
	cmd.Env = append(os.Environ(), "HIVE_CLI_TEST_EXEC=1", "HIVE_HOME="+c.home, "HIVE_CONFIG="+c.cfg, "GORACE=atexit_sleep_ms=0")
	cmd.Env = append(cmd.Env, c.env...)
	cmd.Env = append(cmd.Env, "HIVE_PANE_ID="+pane, "HIVE_DEBUG_HOOKS=1")
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stderr.Len() > 0 {
		c.t.Fatalf("hive insights statusline: %v\n%s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// TestInsightsExportsEmpty: with nothing recorded, exports are empty JSON
// lists and the summary says so.
func TestInsightsExportsEmpty(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	for _, what := range []string{"spans", "usage", "alerts"} {
		var v []json.RawMessage
		decode(t, c.must("insights", "export", "--what", what, "--format", "json"), &v)
		if len(v) != 0 {
			t.Fatalf("%s: %v", what, v)
		}
	}
	if out := c.must("insights"); !strings.Contains(out, "nothing recorded in this range") {
		t.Fatalf("an empty summary: %s", out)
	}
}
