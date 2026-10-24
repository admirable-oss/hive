package workflow_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/workflow"
)

func TestStepsRunInOrderAndInParallel(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	r := e.run(`
version = 1
[[step]]
id = "first"
run = ["echo", "one"]
[[step]]
id = "second"
run = ["echo", "two"]
[[step]]
id = "aside"
needs = []
run = ["echo", "aside"]
[[step]]
id = "last"
needs = ["second", "aside"]
run = ["echo", "${{ steps.first.output }}+${{ steps.aside.output }}"]
`, nil)
	if r.State != workflow.StateSucceeded || states(r) != "first=succeeded second=succeeded aside=succeeded last=succeeded" {
		t.Fatalf("the run: %s %s (%s)", r.State, states(r), r.Reason)
	}
	cmds := e.cmds.commands()
	if slices.Index(cmds, "/src/api: echo one") > slices.Index(cmds, "/src/api: echo two") {
		t.Fatalf("second after first: %v", cmds)
	}
	if got := r.Step("last").Last().Resolved.Run; !slices.Equal(got, []string{"echo", "one+aside"}) {
		t.Fatalf("results read by later steps, recorded resolved: %v", got)
	}
	if r.Trigger.Type != workflow.TriggerManual || r.Digest == "" || !strings.Contains(r.Source, `id = "aside"`) {
		t.Fatalf("the run records what it ran: %+v", r.Trigger)
	}
}

// The roadmap's case: an agent review, the tests, a fix-up agent when they
// fail, and the tests again.
func TestReviewTestFix(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	r := e.run(`
version = 1
environment = "api"
[inputs.branch]
default = "main"
[[step]]
id = "review"
agent = ["claude", "codex"]
prompt = "Review ${{ inputs.branch }}."
isolate = true
[[step]]
id = "test"
run = ["fail"]
on_failure = "continue"
[[step]]
id = "fix"
if = "steps.test.outcome == 'failure'"
agent = "any"
prompt = "The tests fail: ${{ steps.test.output }}"
[[step]]
id = "retest"
if = "steps.fix.ok"
run = ["ok"]
[[step]]
id = "celebrate"
needs = ["test"]
if = "steps.test.ok"
run = ["ok"]
[outputs]
review = "${{ steps.review.summary }}"
fixed = "${{ steps.retest.ok }}"
`, map[string]string{"branch": "feature"})
	if r.State != workflow.StateSucceeded {
		t.Fatalf("a failed test step that continues does not fail the run: %s %s (%s)", r.State, states(r), r.Reason)
	}
	if states(r) != "review=succeeded test=failed fix=succeeded retest=succeeded celebrate=skipped" {
		t.Fatalf("steps: %s", states(r))
	}
	if p := e.tasks.submitted(); !slices.Equal(p, []string{"claude: Review feature.", "codex: The tests fail: FAIL: TestSomething"}) {
		t.Fatalf("claude was preferred, any took the first installed, prompts resolved: %q", p)
	}
	review := r.Step("review").Last()
	if review.Kind != "claude" || review.Plan == "" || review.Resolved.EnvironmentID != "api" || review.Summary != "Did: Review feature." {
		t.Fatalf("the review's result: %+v", review)
	}
	if r.Outputs["review"] != "Did: Review feature." || r.Outputs["fixed"] != "true" {
		t.Fatalf("outputs: %v", r.Outputs)
	}
}

func TestFailureFailsTheRunAndSkipsDependents(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	r := e.run(`
version = 1
[[step]]
id = "build"
run = ["fail", "2"]
[[step]]
id = "deploy"
run = ["ok"]
[[step]]
id = "report"
needs = ["build"]
if = "failure()"
run = ["ok"]
[[step]]
id = "always"
needs = ["deploy"]
if = "always()"
run = ["ok"]
`, nil)
	if r.State != workflow.StateFailed || !strings.Contains(r.Reason, "step build failed: exit status 2") {
		t.Fatalf("the run fails with its reason: %s (%s)", r.State, r.Reason)
	}
	if states(r) != "build=failed deploy=skipped report=succeeded always=succeeded" {
		t.Fatalf("steps: %s", states(r))
	}
	if code := r.Step("build").Last().ExitCode; code == nil || *code != 2 {
		t.Fatalf("its exit status: %v", code)
	}
}

func TestRetriesWithBackoff(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	start := time.Now()
	r := e.run(`
version = 1
[[step]]
id = "flaky"
run = ["flaky", "f", "2"]
retries = 2
backoff = "50ms"
[[step]]
id = "never"
run = ["flaky", "g", "5"]
retries = 1
backoff = "10ms"
needs = []
`, nil)
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Fatalf("backoff doubles: 50ms then 100ms, but it took %s", took)
	}
	flaky, never := r.Step("flaky"), r.Step("never")
	if flaky.State != workflow.StepSucceeded || flaky.Attempts != 3 || e.cmds.count("f") != 3 {
		t.Fatalf("succeeds on its third attempt: %+v", flaky)
	}
	if never.State != workflow.StepFailed || never.Attempts != 2 {
		t.Fatalf("fails once its retries are spent: %+v", never)
	}
	if !slices.ContainsFunc(flaky.Log, func(l workflow.LogEntry) bool { return strings.Contains(l.Message, "retrying in 100ms") }) {
		t.Fatalf("its log: %+v", flaky.Log)
	}
}

func TestTimeoutsAndCancellation(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	r := e.run(`
version = 1
[[step]]
id = "slow"
run = ["sleep"]
timeout = "50ms"
`, nil)
	if st := r.Step("slow"); st.State != workflow.StepFailed || !strings.Contains(st.Reason, "timed out after 50ms") {
		t.Fatalf("a step's timeout: %+v", st)
	}

	r = e.run(`
version = 1
timeout = "100ms"
[[step]]
id = "slow"
run = ["sleep"]
[[step]]
id = "after"
run = ["ok"]
`, nil)
	if r.State != workflow.StateCancelled || !strings.Contains(r.Reason, "budget of 100ms ran out") || states(r) != "slow=cancelled after=cancelled" {
		t.Fatalf("the run's budget: %s %s (%s)", r.State, states(r), r.Reason)
	}

	ctx := context.Background()
	run, err := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: `
version = 1
environment = "api"
[[step]]
id = "agent"
agent = "codex"
prompt = "HANG"
[[step]]
id = "cmd"
needs = []
run = ["sleep"]
[[step]]
id = "later"
needs = ["agent"]
run = ["ok"]
`, Source: "c.toml"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both steps to run", func() bool {
		r, _ := e.svc.Get(ctx, run.ID)
		return r.Summary().Counts[workflow.StepRunning] == 2 && r.Step("agent").Current.Plan != ""
	})
	if _, err := e.svc.Cancel(ctx, run.ID[:4]); err != nil {
		t.Fatal(err)
	}
	r = e.wait(run.ID)
	if r.State != workflow.StateCancelled || states(r) != "agent=cancelled cmd=cancelled later=cancelled" {
		t.Fatalf("cancelled: %s %s", r.State, states(r))
	}
	plan, _ := e.tasks.Wait(ctx, task.WaitRequest{ID: r.Step("agent").Last().Plan})
	if plan.Plan.State != task.PlanCancelled {
		t.Fatalf("the agent's task is cancelled too: %+v", plan.Plan)
	}
	if _, err := e.svc.Cancel(ctx, run.ID); !errors.Is(err, workflow.ErrState) {
		t.Fatalf("cancelling a finished run: %v", err)
	}
}

func TestOutputsActionsAndLoops(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	r := e.run(`
version = 1
[inputs.services]
default = '["api", "web"]'
[[step]]
id = "meta"
run = ["out", "version=1.2.3", "ok=yes"]
[[step]]
id = "each"
foreach = "fromJSON(inputs.services)"
run = ["echo", "deploy ${{ item }} ${{ index }} at ${{ steps.meta.outputs.version }}"]
[[step]]
id = "fixed"
foreach = ["a", "b", "c"]
run = ["count", "${{ item }}"]
[[step]]
id = "none"
foreach = "fromJSON('[]')"
run = ["ok"]
[[step]]
id = "rounds"
run = ["flaky", "r", "2"]
repeat = { max = 5, until = "steps.rounds.ok" }
[[step]]
id = "notify"
action = "slack/post"
with = { text = "deployed ${{ steps.meta.outputs.version }}" }
[outputs]
posted = "${{ steps.notify.outputs.posted }}"
items = "${{ len(steps.each.results) }}"
`, nil)
	if r.State != workflow.StateSucceeded {
		t.Fatalf("%s %s (%s)", r.State, states(r), r.Reason)
	}
	if out := r.Step("meta").Last().Outputs; out["version"] != "1.2.3" || out["ok"] != "yes" {
		t.Fatalf("$HIVE_OUTPUT: %v", out)
	}
	each := r.Step("each")
	if !slices.Equal(each.Items, []string{"api", "web"}) || len(each.Results) != 2 ||
		each.Results[1].Output[0] != "deploy web 1 at 1.2.3" || each.Results[1].Item != "web" {
		t.Fatalf("foreach: %+v", each)
	}
	if e.cmds.count("a")+e.cmds.count("b")+e.cmds.count("c") != 3 {
		t.Fatal("a listed foreach runs each item")
	}
	if st := r.Step("none"); st.State != workflow.StepSucceeded || st.Reason != "no items" {
		t.Fatalf("no items: %+v", st)
	}
	if st := r.Step("rounds"); st.State != workflow.StepSucceeded || len(st.Results) != 3 {
		t.Fatalf("repeat until it holds: %+v", st)
	}
	if r.Outputs["posted"] != "deployed 1.2.3" || r.Outputs["items"] != "2" {
		t.Fatalf("outputs: %v", r.Outputs)
	}
	if len(e.acts.reqs) != 1 || e.acts.reqs[0].Plugin != "slack" || e.acts.reqs[0].Action != "post" || e.acts.reqs[0].Run != r.ID {
		t.Fatalf("the action: %+v", e.acts.reqs)
	}

	r = e.run(`
version = 1
[[step]]
id = "rounds"
run = ["ok"]
repeat = { max = 2, until = "false" }
`, nil)
	if st := r.Step("rounds"); st.State != workflow.StepFailed || !strings.Contains(st.Reason, "never held in 2 rounds") {
		t.Fatalf("a repeat whose until never holds: %+v", st)
	}
}

func TestInputsAndAgents(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	ctx := context.Background()
	text := `
version = 1
[inputs.who]
required = true
[[step]]
id = "a"
agent = ["gemini", "claude"]
prompt = "hi ${{ inputs.who }}"
`
	if _, err := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: text}); !errors.Is(err, workflow.ErrInvalid) || !strings.Contains(err.Error(), "needs input who") {
		t.Fatalf("a required input: %v", err)
	}
	if _, err := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: text, Inputs: map[string]string{"who": "x", "extra": "y"}}); !errors.Is(err, workflow.ErrInvalid) {
		t.Fatalf("an undeclared input: %v", err)
	}
	// Run from a directory with no environment: one is made for it.
	r, err := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: text, Inputs: map[string]string{"who": "you"}, Dir: "/src/web"})
	if err != nil {
		t.Fatal(err)
	}
	r = e.wait(r.ID)
	if r.State != workflow.StateSucceeded || r.Step("a").Last().Kind != "claude" || r.Step("a").Last().Resolved.EnvironmentID != "web" {
		t.Fatalf("the first installed kind of its list, in an environment for its directory: %+v", r.Step("a").Last())
	}

	e.agents = fakeAgents{"codex"}
	r = e.run(text, map[string]string{"who": "x"})
	if st := r.Step("a"); st.State != workflow.StepFailed || !strings.Contains(st.Reason, "no agent of kind gemini, claude is installed here (installed: codex)") {
		t.Fatalf("no kind available: %+v", st)
	}

	r = e.run(`
version = 1
[[step]]
id = "a"
agent = "codex"
prompt = "x"
`, nil)
	if r.State != workflow.StateSucceeded {
		t.Fatalf("the run's directory gives agents an environment: %s %s", r.State, r.Reason)
	}
	r, err = e.svc.Submit(ctx, workflow.SubmitRequest{TOML: "version = 1\n[[step]]\nid = \"a\"\nagent = \"codex\"\nprompt = \"x\""})
	if err != nil {
		t.Fatal(err)
	}
	if r = e.wait(r.ID); !strings.Contains(r.Step("a").Reason, "no environment to run in") {
		t.Fatalf("nowhere to run: %+v", r.Step("a"))
	}
}

func TestRerunRepeatsTheRecordedRun(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	path := e.write(e.own, "greet.toml", "version = 1\n[inputs.who]\ndefault = \"a\"\n[[step]]\nid = \"s\"\nrun = [\"echo\", \"hi ${{ inputs.who }}\"]\n")
	ctx := context.Background()
	r, err := e.svc.Submit(ctx, workflow.SubmitRequest{Name: "greet", Inputs: map[string]string{"who": "b"}, Dir: "/src/api"})
	if err != nil {
		t.Fatal(err)
	}
	first := e.wait(r.ID)
	// The file changes; a rerun still runs what the first run ran.
	if err := os.WriteFile(path, []byte("version = 1\n[[step]]\nid = \"s\"\nrun = [\"fail\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc.Reload(ctx)
	again, err := e.svc.Rerun(ctx, first.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	second := e.wait(again.ID)
	if second.State != workflow.StateSucceeded || second.Digest != first.Digest || second.Inputs["who"] != "b" ||
		second.Trigger.Type != workflow.TriggerRerun || second.Trigger.From != first.ID ||
		second.Step("s").Last().Output[0] != "hi b" {
		t.Fatalf("the rerun: %+v", second)
	}
	if list := e.svc.List(ctx, "greet", 0); len(list) != 2 || list[0].ID != second.ID {
		t.Fatalf("runs, newest first: %+v", list)
	}
}

// A daemon restart: the agent step waits on its plan again without
// starting another, and the interrupted command runs again without
// spending a retry.
func TestRunsResumeAfterARestart(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	e.tasks.hold = make(chan struct{})
	ctx := context.Background()
	r, err := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: `
version = 1
environment = "api"
[[step]]
id = "review"
agent = "codex"
prompt = "review it"
[[step]]
id = "long"
needs = []
run = ["sleep"]
[[step]]
id = "after"
needs = ["review", "long"]
if = "always()"
run = ["ok"]
`, Source: "r.toml"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent's plan and the command", func() bool {
		r, _ := e.svc.Get(ctx, r.ID)
		return r.Step("review").Current != nil && r.Step("review").Current.Plan != "" && len(e.cmds.commands()) == 1
	})
	e.restart()
	waitFor(t, "the command to run again", func() bool { return len(e.cmds.commands()) == 2 })
	got, err := e.svc.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	long := got.Step("long")
	if long.Attempts != 1 || long.Attempt != 1 || !slices.ContainsFunc(long.Log, func(l workflow.LogEntry) bool { return strings.Contains(l.Message, "interrupted by a daemon restart") }) {
		t.Fatalf("the command runs again, its attempt not spent: %+v", long)
	}
	close(e.tasks.hold) // the agent finishes its work
	_, _ = e.svc.Cancel(ctx, r.ID)
	final := e.wait(r.ID)
	if n := len(e.tasks.submitted()); n != 1 {
		t.Fatalf("one plan for the agent, across the restart: %d", n)
	}
	if final.Step("review").Current.Plan == "" {
		t.Fatalf("the review kept its plan: %+v", final.Step("review"))
	}
}

func TestResumeWaitsOnAPlanStartedBefore(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	ctx := context.Background()
	r, err := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: "version = 1\nenvironment = \"api\"\n[[step]]\nid = \"a\"\nagent = \"codex\"\nprompt = \"HANG\"", Source: "x.toml"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "its plan", func() bool {
		r, _ := e.svc.Get(ctx, r.ID)
		return r.Step("a").Current != nil && r.Step("a").Current.Plan != ""
	})
	got, _ := e.svc.Get(ctx, r.ID)
	plan := got.Step("a").Current.Plan
	e.stop()
	// The next daemon's task service resumed the plan; it ends now.
	e.tasks = newFakeTasks()
	e.tasks.adopt(plan, task.Spec{ID: "a", Prompt: "HANG"})
	e.start()
	e.tasks.mu.Lock()
	e.tasks.endLocked(plan, task.StateSucceeded, &task.Result{Summary: "done after the restart"})
	e.tasks.mu.Unlock()
	final := e.wait(r.ID)
	if final.State != workflow.StateSucceeded || final.Step("a").Last().Summary != "done after the restart" || len(e.tasks.submitted()) != 0 {
		t.Fatalf("waited on the same plan: %+v", final.Step("a"))
	}
}

func TestMaxRunsAndConcurrency(t *testing.T) {
	e := newEnv(t, workflow.Config{MaxRuns: 1})
	ctx := context.Background()
	text := "version = 1\nconcurrency = \"parallel\"\n[[step]]\nid = \"s\"\nrun = [\"sleep\"]\n"
	a, _ := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: text, Source: "a.toml", Dir: "/x"})
	b, _ := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: text, Source: "b.toml", Dir: "/x"})
	waitFor(t, "a to run", func() bool { r, _ := e.svc.Get(ctx, a.ID); return r.State == workflow.StateRunning })
	time.Sleep(50 * time.Millisecond)
	if r, _ := e.svc.Get(ctx, b.ID); r.State != workflow.StateQueued {
		t.Fatalf("max_runs 1 keeps b queued: %s", r.State)
	}
	_, _ = e.svc.Cancel(ctx, a.ID)
	waitFor(t, "b to run", func() bool { r, _ := e.svc.Get(ctx, b.ID); return r.State == workflow.StateRunning })
	_, _ = e.svc.Cancel(ctx, b.ID)
	e.wait(b.ID)
}

func TestStoreKeepsAndForgets(t *testing.T) {
	e := newEnv(t, workflow.Config{Keep: 2})
	var ids []string
	for range 3 {
		ids = append(ids, e.run("version = 1\n[[step]]\nid = \"s\"\nrun = [\"ok\"]\n", nil).ID)
	}
	ctx := context.Background()
	waitFor(t, "the oldest run forgotten", func() bool { _, err := e.svc.Get(ctx, ids[0]); return errors.Is(err, workflow.ErrNotFound) })
	waitFor(t, "forgetting published", func() bool { return e.events.count(workflow.EventRemoved) > 0 })
	e.restart()
	if list := e.svc.List(ctx, "", 0); len(list) != 2 || list[0].ID != ids[2] {
		t.Fatalf("what the next daemon reads: %+v", list)
	}

	// A newer hive's run is left alone; a broken one is moved aside.
	runs := filepath.Join(e.dir, "runs")
	if err := os.WriteFile(filepath.Join(runs, "aaaaaaaa.json"), []byte(`{"schema": 99, "id": "aaaaaaaa"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "bbbbbbbb.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	e.restart()
	if _, err := os.Stat(filepath.Join(runs, "aaaaaaaa.json")); err != nil {
		t.Fatalf("a newer run is kept: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(runs, "bbbbbbbb.json.corrupt-*")); len(m) != 1 {
		t.Fatal("a broken run is moved aside")
	}
	if len(e.svc.List(ctx, "", 0)) != 2 {
		t.Fatal("neither is loaded")
	}
}

func TestReport(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	r := e.run(`
version = 1
environment = "api"
[[step]]
id = "review"
agent = "codex"
prompt = "look"
[[step]]
id = "test"
run = ["fail"]
`, nil)
	rep := workflow.NewReport(r, time.Now())
	if rep.State != workflow.StateFailed || len(rep.Steps) != 2 || rep.Steps[0].Summary != "Did: look" || rep.Steps[0].Agent != "codex" || *rep.Steps[1].ExitCode != 1 {
		t.Fatalf("the report: %+v", rep)
	}
	var b strings.Builder
	if err := rep.WriteText(&b, 0); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"of test: failed", "✓ review (succeeded) codex", "│ Did: look", "✗ test (failed) exit 1", "> FAIL: TestSomething"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the text lacks %q:\n%s", want, b.String())
		}
	}
}
