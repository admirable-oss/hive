//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/workflow"
)

// fakeSlowAgent is fakeTaskAgent, working six seconds on a prompt that
// says SLOW: long enough to restart the daemon under it.
var fakeSlowAgent = strings.Replace(fakeTaskAgent, "\tsleep 1\n", "\tcase \"$task\" in *SLOW*) sleep 6 ;; *) sleep 1 ;; esac\n", 1)

// m24Workflow is M24's acceptance workflow: on a push to main, an agent
// reviews, the tests run, and a fix-up agent works on them when they fail
// (unless the fix input says no), then they run again.
const m24Workflow = `
version = 1
description = "Review a push, test it, and fix the tests when they fail."

[inputs.branch]
default = "main"

[inputs.fix]
default = "yes"

[[on]]
git = "push"
branches = ["main"]
[on.inputs]
branch = "${{ trigger.branch }}"

[[step]]
id = "review"
agent = ["claude", "codex"]
prompt = "SLOW review of ${{ inputs.branch }}"

[[step]]
id = "test"
run = ["sh", "-c", "echo 'FAIL: TestLogin'; test -s fixed.txt"]
on_failure = "continue"

[[step]]
id = "fix"
if = "steps.test.outcome == 'failure' && inputs.fix == 'yes'"
agent = "any"
prompt = "fix the tests: ${{ steps.test.output }}"

[[step]]
id = "retest"
if = "steps.test.outcome == 'failure'"
run = ["sh", "-c", "grep -q 'fix the tests' result.txt && echo verdict=fixed >> \"$HIVE_OUTPUT\""]

[outputs]
review = "${{ steps.review.summary }}"
verdict = "${{ steps.retest.outputs.verdict }}"
`

// TestM24WorkflowOnPush is M24's acceptance: a workflow triggered by a
// push runs an agent review, the tests and a fix-up agent on failure. It
// survives a daemon restart in the middle, records every run with its
// inputs and outputs, and runs the same way headless in CI.
func TestM24WorkflowOnPush(t *testing.T) {
	c := newCLI(t)
	repo := newRepo(t, filepath.Join(t.TempDir(), "proj"))
	shared := filepath.Join(repo, ".hive", "workflows")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "review.toml"), []byte(m24Workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := "[terminal]\nshell = \"/bin/sh\"\n\n[workflows]\ngit_interval = \"1s\"\npaths = [\"" + shared + "\"]\n"
	if err := os.WriteFile(c.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeSlowAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only the fake Codex and the system's tools: the workflow prefers
	// Claude, which is not installed here, so it runs with Codex.
	c.env = append(c.env, "PATH="+strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	commit := func(msg string) {
		gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", msg)
	}
	bare := filepath.Join(t.TempDir(), "proj.git")
	gitRun(t, "", "init", "-q", "--bare", bare)
	gitRun(t, repo, "remote", "add", "origin", bare)
	gitRun(t, repo, "push", "-q", "-u", "origin", "main")
	c.must("env", "create", "proj", "--cwd", repo)

	// A shared workflow does not fire until it is trusted.
	var listed []workflow.Workflow
	decode(t, c.must("--json", "workflow", "list"), &listed)
	if len(listed) != 1 || listed[0].Name != "review" || listed[0].Trusted || listed[0].Own {
		t.Fatalf("the repository's workflow, untrusted: %+v", listed)
	}
	commit("untrusted")
	gitRun(t, repo, "push", "-q", "origin", "main")
	time.Sleep(2500 * time.Millisecond)
	runs := func() []workflow.RunSummary {
		var out []workflow.RunSummary
		decode(t, c.must("--json", "workflow", "runs"), &out)
		return out
	}
	if r := runs(); len(r) != 0 {
		t.Fatalf("an untrusted workflow ran: %+v", r)
	}
	c.must("workflow", "trust", "review")
	statePath := filepath.Join(c.home, "workflows", "state.json")
	c.waitFor("the repository's references", func() bool {
		data, _ := os.ReadFile(statePath)
		return strings.Contains(string(data), "refs/remotes/origin/main")
	})

	// A push runs it.
	commit("the work")
	gitRun(t, repo, "push", "-q", "origin", "main")
	c.waitForUpTo(20*time.Second, "the push's run", func() bool { return len(runs()) == 1 })
	id := runs()[0].ID
	status := func() workflow.Run {
		var r workflow.Run
		decode(t, c.must("--json", "workflow", "status", id), &r)
		return r
	}
	c.waitForUpTo(30*time.Second, "the review agent at work", func() bool {
		r := status()
		st := r.Step("review")
		return st.State == workflow.StepRunning && st.Current != nil && st.Current.Plan != ""
	})

	// The daemon restarts in the middle of the review; the agent keeps
	// working, and the run goes on.
	c.must("daemon", "restart")
	out, errOut, code := c.run("workflow", "wait", id, "--timeout", "2m")
	if code != 0 {
		t.Fatalf("the run: exit %d\n%s\n%s\ndaemon log:\n%s", code, out, errOut, c.daemonLog())
	}
	r := status()
	if r.State != workflow.StateSucceeded || r.Trigger.Type != workflow.TriggerGit || r.Trigger.Data["branch"] != "main" ||
		r.Inputs["branch"] != "main" || r.Inputs["fix"] != "yes" || r.Digest == "" || !strings.Contains(r.Source, "SLOW review") {
		t.Fatalf("recorded with its trigger and inputs: %+v", r)
	}
	if got := states(r); got != "review=succeeded test=failed fix=succeeded retest=succeeded" {
		t.Fatalf("review, tests, fix-up, tests: %s", got)
	}
	review, test, fix := r.Step("review").Last(), r.Step("test").Last(), r.Step("fix").Last()
	if review.Kind != "codex" || review.Summary != "Did: SLOW review of main" || r.Outputs["review"] != review.Summary {
		t.Fatalf("the review, from the only installed kind, with its summary: %+v %v", review, r.Outputs)
	}
	if len(test.Output) == 0 || test.Output[0] != "FAIL: TestLogin" || *test.ExitCode != 1 {
		t.Fatalf("the tests' output: %+v", test)
	}
	if !strings.Contains(fix.Resolved.Prompt, "FAIL: TestLogin") || r.Outputs["verdict"] != "fixed" {
		t.Fatalf("the fix-up agent got the failure: %q, %v", fix.Resolved.Prompt, r.Outputs)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "result.txt"))
	if n := strings.Count(string(data), "SLOW review of main"); n != 1 {
		t.Fatalf("the review was prompted once across the restart, not %d times:\n%s", n, data)
	}
	if report := c.must("workflow", "report", id); !strings.Contains(report, "✓ review (succeeded) codex") || !strings.Contains(report, "output verdict = \"fixed\"") {
		t.Fatalf("the report:\n%s", report)
	}

	// The same file, headless as CI runs it: a daemon of its own, a JSON
	// report, and exit 1 since the tests fail and nothing fixes them.
	if err := os.Remove(filepath.Join(repo, "result.txt")); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = c.run("--json", "workflow", "run", filepath.Join(shared, "review.toml"), "--headless", "--dir", repo, "-i", "fix=no", "--timeout", "2m")
	if code != exitError {
		t.Fatalf("headless: exit %d\n%s\n%s", code, out, errOut)
	}
	var rep workflow.Report
	decode(t, out, &rep)
	if rep.State != workflow.StateFailed || rep.Inputs["fix"] != "no" || rep.Trigger.Type != workflow.TriggerManual || len(rep.Steps) != 4 {
		t.Fatalf("the headless report: %+v", rep)
	}
	if rep.Steps[0].State != workflow.StepSucceeded || rep.Steps[0].Summary != "Did: SLOW review of main" ||
		rep.Steps[2].State != workflow.StepSkipped || rep.Steps[3].State != workflow.StepFailed {
		t.Fatalf("headless, the same steps: %+v", rep.Steps)
	}
	// The daemon here never saw it.
	if n := len(runs()); n != 1 {
		t.Fatalf("the headless run is not this daemon's: %d runs", n)
	}
}

func states(r workflow.Run) string {
	var parts []string
	for _, st := range r.Steps {
		parts = append(parts, st.ID+"="+string(st.State))
	}
	return strings.Join(parts, " ")
}
