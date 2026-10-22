//go:build !windows

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/distribution"
	"github.com/admirable-oss/hive/internal/task"
)

// fakeTaskAgent draws Codex's screens and takes multi-line prompts (a
// bracketed paste, then Enter). Each prompt is worked on for a second,
// appended to result.txt, summarised to $HIVE_TASK_SUMMARY and answered
// with "DONE: <first line>". A prompt saying ASK FIRST asks for permission
// first and goes on once it gets a line (Enter).
const fakeTaskAgent = `#!/bin/sh
bottom='\033[999;1H'
idle() {
	printf "\033[H\033[2J  >_ OpenAI Codex (fake)\r\n%s${bottom}\033[2A› Ask Codex to do anything${bottom}  ? for shortcuts" "$1"
}
printf '\033[?2004h'
idle ""
task=""
while IFS= read -r line; do
	clean=$(printf '%s' "$line" | tr -d '\033' | sed -e 's/\[200~//' -e 's/\[201~//')
	[ -z "$task" ] && task="$clean"
	case "$line" in *"201~"*) ;; *) continue ;; esac
	case "$task" in *"ASK FIRST"*)
		printf "\033[H\033[2J  >_ OpenAI Codex (fake)\r\n› %s\r\n\r\n  Would you like to run the tests?\r\n  Press enter to confirm or esc to cancel" "$task"
		IFS= read -r answer ;;
	esac
	printf "\033[H\033[2J  >_ OpenAI Codex (fake)\r\n› %s${bottom}\033[3A• Working (0s • esc to interrupt)" "$task"
	sleep 1
	printf '%s\n' "$task" >> result.txt
	[ -n "$HIVE_TASK_SUMMARY" ] && printf 'Did: %s\n' "$task" > "$HIVE_TASK_SUMMARY"
	idle "› $task\r\n• DONE: $task in $(basename "$PWD")"
	task=""
done
`

// m22Plan is M22's acceptance plan: 10 tasks with dependencies, some on
// the other machine, one whose agent asks for permission, one that needs
// approval and one whose check fails until fixed.
const m22Plan = `
name = "m22 acceptance"
max_parallel = 3

[defaults]
kind = "codex"
environment = "proj"
isolate = true
timeout = "2m"

[[task]]
id = "schema"
prompt = "design the schema"

[[task]]
id = "api"
needs = ["schema"]
prompt = "build the api"

[[task]]
id = "db"
needs = ["schema"]
labels = ["remote"]
prompt = "build the db"

[[task]]
id = "web"
needs = ["api"]
labels = ["remote"]
prompt = "build the web"

[[task]]
id = "cli"
needs = ["api"]
prompt = "build the cli ASK FIRST"

[[task]]
id = "tests"
needs = ["api", "db"]
prompt = "write the tests"
until = "command"
command = ["sh", "-c", "test -f fixed"]

[[task]]
id = "lint"
labels = ["remote"]
prompt = "lint everything"

[[task]]
id = "bench"
needs = ["db"]
prompt = "benchmark it"

[[task]]
id = "deploy"
needs = ["web", "cli"]
approve = true
prompt = "deploy it"

[[task]]
id = "release"
needs = ["deploy", "tests", "bench", "lint"]
prompt = "cut the release"
`

// TestM22PlanAcrossMachines is M22's acceptance: a plan of 10 tasks with
// dependencies runs across 2 machines within its concurrency limits,
// survives a daemon restart, stops for one approval (and one blocked
// agent, pausing only its task), and produces a report with every task's
// result. A failed task is retried without disturbing the others.
func TestM22PlanAcrossMachines(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	if err := os.WriteFile(c.cfg, []byte("[terminal]\nshell = \"/bin/sh\"\n[tasks]\ncapacity = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This machine: the fake agent on PATH, and a repository.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeTaskAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := newRepo(t, filepath.Join(t.TempDir(), "proj"))
	c.must("env", "create", "proj", "--cwd", repo)

	// The other machine, reached over (fake) ssh, with its own agent
	// command (its PATH is the system's) and its own repository.
	root := c.fakeHosts()
	beta := c.host(root, "beta", true)
	agentBin := filepath.Join(beta, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(agentBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentBin, []byte(fakeTaskAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := fs.ReadFile(distribution.AgentDetection, "agent-detection/codex.toml")
	if err != nil {
		t.Fatal(err)
	}
	manifest = []byte(strings.Replace(string(manifest), `command = ["codex"]`, fmt.Sprintf("command = [%q]", agentBin), 1))
	if err := os.MkdirAll(filepath.Join(beta, "agent-detection"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beta, "agent-detection", "codex.toml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beta, "config.toml"), []byte("[terminal]\nshell = \"/bin/sh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	betaRepo := newRepo(t, filepath.Join(beta, "proj"))
	c.must("machine", "add", "beta", "beta")
	c.must("machine", "set", "beta", "--label", "remote", "--capacity", "2")
	c.must("--machine", "beta", "env", "create", "proj", "--cwd", betaRepo)

	planFile := filepath.Join(t.TempDir(), "plan.toml")
	if err := os.WriteFile(planFile, []byte(m22Plan), 0o600); err != nil {
		t.Fatal(err)
	}
	var started task.Plan
	decode(t, c.must("--json", "task", "run", planFile), &started)
	id := started.ID
	if id == "" || len(started.Tasks) != 10 || started.Tasks[0].Spec.ID != "schema" {
		t.Fatalf("started: %+v", started)
	}

	get := func() task.Plan {
		var p task.Plan
		decode(t, c.must("--json", "task", "get", id), &p)
		return p
	}
	state := func(p task.Plan, tid string) task.State { return p.Task(tid).State }

	// The daemon restarts while the first task runs: its agent works on
	// under its shim, and the plan goes on.
	c.waitForUpTo(time.Minute, "schema to be prompted", func() bool {
		p := get()
		x := p.Task("schema")
		return x.State == task.StateRunning && x.Phase == task.PhasePrompted
	})
	c.must("daemon", "restart")
	c.waitForUpTo(time.Minute, "api to start after the restart", func() bool { return state(get(), "api") != task.StateWaiting })
	if p := get(); state(p, "schema") != task.StateSucceeded || p.Task("schema").Attempt != 1 {
		t.Fatalf("schema across the restart: %+v", p.Task("schema"))
	}

	// cli's agent asks for permission: only cli pauses.
	c.waitForUpTo(time.Minute, "cli to block", func() bool { return state(get(), "cli") == task.StateBlocked })
	if _, _, code := c.run("task", "wait", id, "--until", "attention", "--timeout", "10s"); code != exitAgent {
		t.Fatalf("waiting for attention exits %d, want %d", code, exitAgent)
	}
	blocked := get()
	c.must("agent", "send-keys", blocked.Task("cli").PaneID, "Enter")

	// tests fails its check; the rest carries on, and deploy waits for a yes.
	c.waitForUpTo(time.Minute, "deploy to wait for approval", func() bool { return state(get(), "deploy") == task.StateApproval })
	c.waitForUpTo(time.Minute, "tests to fail its check", func() bool { return state(get(), "tests") == task.StateFailed })
	p := get()
	if r := p.Task("tests").Result; r == nil || r.ExitCode == nil || *r.ExitCode != 1 || state(p, "release") != task.StateSkipped {
		t.Fatalf("after tests failed: %+v, release %s", r, state(p, "release"))
	}
	c.must("task", "approve", id[:5], "deploy")

	// Fixed by hand, it is retried in its own worktree; release comes back.
	if err := os.WriteFile(filepath.Join(p.Task("tests").Path, "fixed"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c.must("task", "retry", id, "tests")
	out, errOut, code := c.run("task", "wait", id, "--timeout", "2m")
	if code != 0 {
		t.Fatalf("plan: exit %d: %s %s\n%s", code, out, errOut, c.must("task", "report", id, "--screen", "5"))
	}

	// The report has every task's result.
	var report task.Report
	decode(t, c.must("--json", "task", "report", id), &report)
	if report.State != task.PlanSucceeded || len(report.Tasks) != 10 || report.Counts[task.StateSucceeded] != 10 {
		t.Fatalf("report: %+v", report)
	}
	machines := map[string]int{}
	for _, tr := range report.Tasks {
		r := tr.Result
		if r == nil || r.Outcome != "met" || len(r.Screen) == 0 || !slices.Contains(r.Changed, "result.txt") || !strings.HasPrefix(r.Summary, "Did: ") {
			t.Fatalf("task %s's result: %+v", tr.ID, r)
		}
		// Labelled tasks run on the machine with the label; the rest run
		// wherever there is room.
		labelled := tr.ID == "db" || tr.ID == "web" || tr.ID == "lint"
		if labelled && tr.Machine != "beta" || tr.Machine != "beta" && tr.Machine != task.LocalMachine {
			t.Fatalf("task %s ran on %q", tr.ID, tr.Machine)
		}
		machines[tr.Machine]++
		if tr.Branch != "hive/"+id+"/"+tr.ID {
			t.Fatalf("task %s's branch: %q", tr.ID, tr.Branch)
		}
	}
	if machines[task.LocalMachine] == 0 || machines["beta"] < 3 {
		t.Fatalf("tasks per machine: %v", machines)
	}
	if a := report.Tasks[slices.IndexFunc(report.Tasks, func(t task.TaskReport) bool { return t.ID == "deploy" })].Approval; a == nil || !a.Approved {
		t.Fatalf("deploy's approval: %+v", a)
	}
	// Within the limits: 3 at once in the plan, 2 per machine.
	final := get()
	for machine, limit := range map[string]int{"": 3, task.LocalMachine: 2, "beta": 2} {
		if n := peak(final, machine); n > limit {
			t.Fatalf("%d tasks ran at once on %q, over %d", n, cmpStr(machine, "the plan"), limit)
		}
	}
	text := c.must("task", "report", id)
	for _, want := range []string{"Plan " + id, "✓ release", "Did: cut the release", "result.txt"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the report lacks %q:\n%s", want, text)
		}
	}
	if list := c.must("task", "list"); !strings.Contains(list, id) || !strings.Contains(list, "10/10 done") {
		t.Fatalf("task list:\n%s", list)
	}
}

// peak is how many of the plan's tasks ran at once on machine ("" for
// any), from their last attempts' times.
func peak(p task.Plan, machine string) int {
	type edge struct {
		at   time.Time
		step int
	}
	var edges []edge
	for _, t := range p.Tasks {
		if machine != "" && t.Machine != machine || t.StartedAt.IsZero() {
			continue
		}
		edges = append(edges, edge{t.StartedAt, 1}, edge{t.EndedAt, -1})
	}
	slices.SortFunc(edges, func(a, b edge) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return a.step - b.step // an end before a start at the same moment
	})
	n, top := 0, 0
	for _, e := range edges {
		n += e.step
		top = max(top, n)
	}
	return top
}

// newRepo makes a repository with one commit at dir.
func newRepo(t *testing.T, dir string) string {
	t.Helper()
	gitRun(t, "", "init", "-q", "-b", "main", dir)
	gitRun(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func TestTaskRunOneTaskFromFlags(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeTaskAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := newRepo(t, filepath.Join(t.TempDir(), "solo"))
	c.must("env", "create", "solo", "--cwd", repo)

	if _, errOut, code := c.run("task", "run"); code != exitUsage || !strings.Contains(errOut, "--prompt") {
		t.Fatalf("no plan: exit %d %s", code, errOut)
	}
	if _, errOut, code := c.run("task", "run", "-p", "x", "--kind", "codex", "--env", "solo", "--until", "match"); code == 0 || !strings.Contains(errOut, "needs match") {
		t.Fatalf("an invalid task: exit %d %s", code, errOut)
	}
	out := c.must("task", "run", "-p", "fix the bug", "--kind", "codex", "--env", "solo", "--isolate",
		"--check", "test", "--check", "-s", "--check", "result.txt", "--wait")
	if !strings.Contains(out, "✓ task") || !strings.Contains(out, "Did: fix the bug") {
		t.Fatalf("report:\n%s", out)
	}
	// A failing check exits 1 and says why.
	out, errOut, code := c.run("task", "run", "-p", "break it", "--kind", "codex", "--env", "solo", "--check", "false", "--wait")
	if code != exitError || !strings.Contains(out, "false exited 1") || !strings.Contains(errOut, "did not succeed") {
		t.Fatalf("a failing check: exit %d\n%s\n%s", code, out, errOut)
	}
}
