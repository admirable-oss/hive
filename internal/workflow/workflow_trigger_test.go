package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/workflow"
)

func runsOf(e *env, name string) []workflow.RunSummary {
	return e.svc.List(context.Background(), name, 0)
}

func notify(e *env, typ string, data any) {
	raw, _ := json.Marshal(data)
	e.svc.Notify(context.Background(), typ, 7, raw)
}

func TestEventTriggers(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	e.write(e.own, "blocked.toml", `
version = 1
concurrency = "parallel"
[inputs.agent]
[[on]]
event = "agent.blocked"
[on.inputs]
agent = "${{ trigger.data.id }}"
[[step]]
id = "say"
run = ["echo", "${{ inputs.agent }} in ${{ run.dir }} after ${{ trigger.event }} #${{ trigger.seq }}"]
`)
	e.write(e.own, "branches.toml", `
version = 1
[[on]]
event = "branch."
[on.match]
state = "rea*"
[[step]]
id = "s"
run = ["ok"]
`)
	notify(e, "agent.state", map[string]any{"id": "p3", "state": "working", "environment_id": "api"})
	notify(e, "agent.state", map[string]any{"id": "p3", "state": "blocked", "environment_id": "api"})
	notify(e, "process.exited", map[string]any{"id": "p3"})
	runs := runsOf(e, "blocked")
	if len(runs) != 1 {
		t.Fatalf("one run, for the blocked state: %+v", runs)
	}
	r := e.wait(runs[0].ID)
	if out := r.Step("say").Last().Output[0]; out != "p3 in /src/api after agent.state #7" {
		t.Fatalf("its inputs, from the event, in its environment's directory: %q", out)
	}
	if r.Trigger.Type != workflow.TriggerEvent || r.Inputs["agent"] != "p3" {
		t.Fatalf("the trigger recorded: %+v", r.Trigger)
	}

	notify(e, "branch.updated", map[string]any{"state": "working"})
	notify(e, "branch.updated", map[string]any{"state": "ready"})
	if n := len(runsOf(e, "branches")); n != 1 {
		t.Fatalf("a prefix and a glob: %d runs", n)
	}
}

func TestARunNeverTriggersItsOwnWorkflow(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	e.write(e.own, "loop.toml", "version = 1\nconcurrency = \"parallel\"\n[[on]]\nevent = \"workflow.run\"\n[[step]]\nid = \"s\"\nrun = [\"ok\"]\n")
	e.write(e.own, "other.toml", "version = 1\n[[step]]\nid = \"s\"\nrun = [\"ok\"]\n")
	notify(e, workflow.EventRun, map[string]any{"workflow": "loop", "state": "running"})
	if n := len(runsOf(e, "loop")); n != 0 {
		t.Fatalf("its own run: %d", n)
	}
	notify(e, workflow.EventRun, map[string]any{"workflow": "other", "state": "running"})
	if n := len(runsOf(e, "loop")); n != 1 {
		t.Fatalf("another workflow's run: %d", n)
	}
}

func TestWebhooksAndConcurrency(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	ctx := context.Background()
	e.write(e.own, "hook.toml", `
version = 1
concurrency = "skip"
environment = "api"
[inputs.ref]
[[on]]
webhook = "github"
[on.inputs]
ref = "${{ trigger.data.ref }}"
[[step]]
id = "s"
run = ["sleep"]
`)
	runs, err := e.svc.Fire(ctx, "github", map[string]any{"ref": "refs/heads/main"})
	if err != nil || len(runs) != 1 {
		t.Fatalf("fired: %+v %v", runs, err)
	}
	if again, _ := e.svc.Fire(ctx, "github", nil); len(again) != 0 {
		t.Fatalf("skip while a run goes on: %+v", again)
	}
	r, _ := e.svc.Get(ctx, runs[0].ID)
	if r.Inputs["ref"] != "refs/heads/main" || r.Trigger.Data["name"] != "github" {
		t.Fatalf("the webhook's payload: %+v", r)
	}
	if none, _ := e.svc.Fire(ctx, "gitlab", nil); len(none) != 0 {
		t.Fatal("another webhook fires nothing")
	}
	if _, err := e.svc.Fire(ctx, "bad name", nil); !errors.Is(err, workflow.ErrInvalid) {
		t.Fatalf("a bad name: %v", err)
	}
	_, _ = e.svc.Cancel(ctx, r.ID)
	e.wait(r.ID)

	e.write(e.own, "queue.toml", "version = 1\nenvironment = \"api\"\n[[on]]\nwebhook = \"q\"\n[[step]]\nid = \"s\"\nrun = [\"sleep\"]\n")
	first, _ := e.svc.Fire(ctx, "q", nil)
	second, _ := e.svc.Fire(ctx, "q", nil)
	waitFor(t, "the first to run", func() bool { r, _ := e.svc.Get(ctx, first[0].ID); return r.State == workflow.StateRunning })
	time.Sleep(30 * time.Millisecond)
	if r, _ := e.svc.Get(ctx, second[0].ID); r.State != workflow.StateQueued {
		t.Fatalf("queue: the second waits: %s", r.State)
	}
	_, _ = e.svc.Cancel(ctx, first[0].ID)
	waitFor(t, "the second to run", func() bool { r, _ := e.svc.Get(ctx, second[0].ID); return r.State == workflow.StateRunning })
	_, _ = e.svc.Cancel(ctx, second[0].ID)
	e.wait(second[0].ID)
}

func TestGitTriggers(t *testing.T) {
	home := filepath.FromSlash("/home/me")
	e := newEnv(t, workflow.Config{Home: home})
	repo := filepath.Join(home, "src", "api") // as "~/src/api" expands here
	e.ws.setRef(repo, "refs/heads/main", "c1")
	e.ws.setRef(repo, "refs/remotes/origin/main", "c1")
	e.ws.roots[repo] = repo
	e.write(e.own, "push.toml", `
version = 1
concurrency = "parallel"
[inputs.branch]
[[on]]
git = "push"
repo = "~/src/api"
branches = ["main", "release/*"]
[on.inputs]
branch = "${{ trigger.branch }}"
[[step]]
id = "s"
run = ["echo", "${{ trigger.before }}..${{ trigger.commit }} in ${{ run.dir }}"]
`)
	e.write(e.own, "branch.toml", "version = 1\nconcurrency = \"parallel\"\n[[on]]\ngit = \"branch\"\n[[step]]\nid = \"s\"\nrun = [\"ok\"]\n")
	waitFor(t, "the baseline", func() bool {
		st, _ := workflow.NewFilesystemStore(e.dir).LoadState()
		return st.Refs[repo]["refs/heads/main"] == "c1"
	})

	// Committed and pushed: the remote-tracking branch follows the local one.
	e.ws.setRef(repo, "refs/heads/main", "c2")
	e.svc.GitChanged()
	time.Sleep(700 * time.Millisecond)
	if n := len(runsOf(e, "push")); n != 0 {
		t.Fatalf("a commit is not a push: %d", n)
	}
	e.ws.setRef(repo, "refs/remotes/origin/main", "c2")
	e.svc.GitChanged()
	waitFor(t, "the push's run", func() bool { return len(runsOf(e, "push")) == 1 })
	r := e.wait(runsOf(e, "push")[0].ID)
	if out := r.Step("s").Last().Output[0]; out != "c1..c2 in "+repo || r.Inputs["branch"] != "main" {
		t.Fatalf("the push: %q %+v", out, r.Inputs)
	}

	// Fetched someone else's work: not a push. A branch the triggers do
	// not name: not one of theirs. A new branch fires the branch trigger.
	e.ws.setRef(repo, "refs/remotes/origin/main", "c9")
	e.ws.setRef(repo, "refs/heads/topic", "t1")
	e.ws.setRef(repo, "refs/remotes/origin/topic", "t1")
	e.svc.GitChanged()
	waitFor(t, "the new branch's run", func() bool { return len(runsOf(e, "branch")) == 1 })
	time.Sleep(100 * time.Millisecond)
	if n := len(runsOf(e, "push")); n != 1 {
		t.Fatalf("a fetch, and a push to another branch, start nothing: %d", n)
	}

	// A push while no daemon ran fires once the next daemon looks.
	e.stop()
	e.ws.setRef(repo, "refs/heads/release/2", "r2")
	e.ws.setRef(repo, "refs/remotes/origin/release/2", "r2")
	e.start()
	waitFor(t, "the missed push", func() bool { return len(runsOf(e, "push")) == 2 })
	if r := runsOf(e, "push")[0]; r.Trigger != workflow.TriggerGit {
		t.Fatalf("%+v", r)
	}
}

func TestSharedWorkflowsFireOnlyWhenTrusted(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	ctx := context.Background()
	path := e.write(e.shared, "ci.toml", "version = 1\nconcurrency = \"parallel\"\n[[on]]\nwebhook = \"ci\"\n[[step]]\nid = \"s\"\nrun = [\"pwd\"]\n")
	w, err := e.svc.Workflow(ctx, "ci")
	if err != nil || w.Own || w.Trusted || w.Fires() {
		t.Fatalf("a shared workflow starts untrusted: %+v %v", w, err)
	}
	if runs, _ := e.svc.Fire(ctx, "ci", nil); len(runs) != 0 {
		t.Fatal("an untrusted workflow fired")
	}
	// Running it by hand is the person's own command: no trust needed.
	r, err := e.svc.Submit(ctx, workflow.SubmitRequest{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir != filepath.Dir(filepath.Dir(e.shared)) {
		t.Fatalf("a repository's workflow runs in the repository: %q", r.Dir)
	}
	if _, err := e.svc.Trust(ctx, "ci", false); err != nil {
		t.Fatal(err)
	}
	if runs, _ := e.svc.Fire(ctx, "ci", nil); len(runs) != 1 {
		t.Fatal("a trusted workflow fires")
	}
	// A change to the file (a commit to the repository) needs trust again.
	if err := os.WriteFile(path, []byte("version = 1\nconcurrency = \"parallel\"\n[[on]]\nwebhook = \"ci\"\n[[step]]\nid = \"s\"\nrun = [\"rm\", \"-rf\", \"/\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc.Reload(ctx)
	if w, _ := e.svc.Workflow(ctx, "ci"); w.Trusted {
		t.Fatal("a changed file is still trusted")
	}
	if runs, _ := e.svc.Fire(ctx, "ci", nil); len(runs) != 0 {
		t.Fatal("a changed file fired")
	}
	if _, err := e.svc.Trust(ctx, "ci", false); err != nil {
		t.Fatal(err)
	}
	e.restart()
	if w, _ := e.svc.Workflow(ctx, "ci"); !w.Trusted {
		t.Fatal("trust is kept across restarts")
	}
	if _, err := e.svc.Trust(ctx, "ci", true); err != nil {
		t.Fatal(err)
	}
	if w, _ := e.svc.Workflow(ctx, "ci"); w.Trusted {
		t.Fatal("revoked")
	}
	for _, s := range e.svc.List(ctx, "ci", 0) {
		if !s.State.Finished() {
			_, _ = e.svc.Cancel(ctx, s.ID)
		}
	}

	e.write(e.shared, "broken.toml", "version = 1\n[[step]]\n")
	list := e.svc.Workflows(ctx)
	var broken *workflow.Workflow
	for i := range list {
		if list[i].Path == filepath.Join(e.shared, "broken.toml") {
			broken = &list[i]
		}
	}
	if broken == nil || broken.Error == "" {
		t.Fatalf("a file that does not parse is listed with its error: %+v", list)
	}
	if _, err := e.svc.Trust(ctx, broken.Name, false); !errors.Is(err, workflow.ErrState) {
		t.Fatalf("trusting it: %v", err)
	}
	if _, err := e.svc.Submit(ctx, workflow.SubmitRequest{Name: broken.Name}); !errors.Is(err, workflow.ErrInvalid) {
		t.Fatalf("running it: %v", err)
	}
}

func TestSchedulesAreListed(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	ctx := context.Background()
	e.write(e.own, "nightly.toml", "version = 1\n[[on]]\nschedule = \"0 3 * * *\"\n[[on]]\nevery = \"6h\"\n[[step]]\nid = \"s\"\nrun = [\"ok\"]\n")
	waitFor(t, "its next time", func() bool { w, _ := e.svc.Workflow(ctx, "nightly"); return !w.Next.IsZero() })
	w, _ := e.svc.Workflow(ctx, "nightly")
	if until := time.Until(w.Next); until <= 0 || until > 6*time.Hour+time.Minute {
		t.Fatalf("the next of 03:00 and every 6h: %s", w.Next)
	}
	if len(w.Triggers) != 2 || w.Triggers[0].Description != "0 3 * * *" || w.Triggers[1].Description != "every 6h0m0s" {
		t.Fatalf("its triggers described: %+v", w.Triggers)
	}
}

func TestAnEditedScheduleIsComputedAgain(t *testing.T) {
	e := newEnv(t, workflow.Config{})
	ctx := context.Background()
	e.write(e.own, "s.toml", "version = 1\n[[on]]\nevery = \"6h\"\n[[step]]\nid = \"s\"\nrun = [\"ok\"]\n")
	waitFor(t, "its next time", func() bool { w, _ := e.svc.Workflow(ctx, "s"); return !w.Next.IsZero() })
	time.Sleep(20 * time.Millisecond) // a later modification time
	e.write(e.own, "s.toml", "version = 1\n[[on]]\nevery = \"1h\"\n[[step]]\nid = \"s\"\nrun = [\"ok\"]\n")
	waitFor(t, "the edited interval", func() bool {
		w, _ := e.svc.Workflow(ctx, "s")
		return !w.Next.IsZero() && time.Until(w.Next) <= time.Hour
	})
}

func TestACancelBeforeAResumeCancelsTheTask(t *testing.T) {
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
	e.restart()
	if _, err := e.svc.Cancel(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	final := e.wait(r.ID)
	if final.State != workflow.StateCancelled || final.Step("a").State != workflow.StepCancelled {
		t.Fatalf("cancelled: %s %s", final.State, states(final))
	}
}
