package task_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/task"
)

// tenTasks is a plan of 10 tasks with dependencies, as in M22's
// acceptance: a diamond, a chain and loose tasks.
func tenTasks() []task.Spec {
	return []task.Spec{
		spec("schema"),
		spec("api", "schema"),
		spec("db", "schema"),
		spec("web", "api"),
		spec("cli", "api"),
		spec("tests", "api", "db"),
		spec("docs", "web", "cli"),
		spec("lint"),
		spec("bench", "db"),
		spec("release", "docs", "tests", "bench", "lint"),
	}
}

func TestAPlanRunsWithinItsLimitsAcrossMachines(t *testing.T) {
	ms := newMachines(work(30*time.Millisecond),
		task.Machine{Name: task.LocalMachine, Capacity: 2, Local: true},
		task.Machine{Name: "box", Capacity: 2, Labels: []string{"linux"}},
	)
	h := run(t, task.Config{}, newMemStore(), ms)
	tasks := tenTasks()
	tasks[7].Labels = []string{"linux"} // lint only runs on box
	p, err := h.svc.Submit(t.Context(), task.PlanSpec{Name: "big", MaxParallel: 3, Tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.State != task.PlanRunning || len(p.Tasks) != 10 {
		t.Fatalf("submitted: %+v", p)
	}
	res := h.wait(p.ID, task.WaitFinished)
	if res.Outcome != task.OutcomeFinished || res.Plan.State != task.PlanSucceeded {
		t.Fatalf("plan: %s %+v", res.Plan.State, states(res.Plan))
	}
	local, la := ms.execs[task.LocalMachine].snapshot()
	box, ba := ms.execs["box"].snapshot()
	if local > 2 || box > 2 {
		t.Fatalf("machine capacity: local ran %d at once, box %d", local, box)
	}
	if len(la)+len(ba) != 10 || len(ba) == 0 {
		t.Fatalf("attempts: %d local, %d on box", len(la), len(ba))
	}
	ran := map[string]time.Time{}
	for _, tk := range res.Plan.Tasks {
		ran[tk.Spec.ID] = tk.StartedAt
		if tk.Attempt != 1 || tk.Result == nil || tk.Result.Summary != "did "+tk.Spec.ID || tk.AgentID != "agent-"+tk.Spec.ID {
			t.Fatalf("task %s: %+v", tk.Spec.ID, tk)
		}
		if tk.Spec.ID == "lint" && tk.Machine != "box" {
			t.Fatalf("lint ran on %s, not on the machine with its label", tk.Machine)
		}
	}
	for _, tk := range res.Plan.Tasks {
		for _, n := range tk.Spec.Needs {
			need := res.Plan.Task(n)
			if tk.StartedAt.Before(need.EndedAt) {
				t.Fatalf("%s started before %s, which it needs, ended", tk.Spec.ID, n)
			}
		}
	}
	// The report has every task's result.
	report := task.NewReport(res.Plan, time.Now())
	var text bytes.Buffer
	if err := report.WriteText(&text, 0); err != nil {
		t.Fatal(err)
	}
	for _, tk := range tasks {
		if !strings.Contains(text.String(), "✓ "+tk.ID) || !strings.Contains(text.String(), "did "+tk.ID) {
			t.Fatalf("report lacks %s:\n%s", tk.ID, text.String())
		}
	}
	if report.Counts[task.StateSucceeded] != 10 {
		t.Fatalf("report counts: %v", report.Counts)
	}
}

func TestPlanAndKindLimitsHoldAcrossPlans(t *testing.T) {
	ms := newMachines(work(20*time.Millisecond), task.Machine{Name: task.LocalMachine, Capacity: 10, Local: true})
	h := run(t, task.Config{Kinds: map[string]int{"codex": 1}}, newMemStore(), ms)
	var ids []string
	for range 2 {
		var tasks []task.Spec
		for _, id := range []string{"a", "b", "c", "d"} {
			s := spec(id)
			s.Kind = "codex"
			tasks = append(tasks, s)
		}
		p, err := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: tasks})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	for _, id := range ids {
		h.wait(id, task.WaitFinished)
	}
	ex := ms.execs[task.LocalMachine]
	ex.mu.Lock()
	peak := ex.kindPeak["codex"]
	ex.mu.Unlock()
	if peak != 1 {
		t.Fatalf("codex ran %d at once, over its cap of 1", peak)
	}
}

func TestApprovalStopsOnlyItsTask(t *testing.T) {
	ms := newMachines(work(5*time.Millisecond), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	gated := spec("deploy", "build")
	gated.Approve = true
	p, err := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{spec("build"), gated, spec("after", "deploy"), spec("free")}})
	if err != nil {
		t.Fatal(err)
	}
	res := h.wait(p.ID, task.WaitAttention)
	st := states(res.Plan)
	if res.Outcome != task.OutcomeAttention || st["deploy"] != task.StateApproval || st["after"] != task.StateWaiting {
		t.Fatalf("attention: %s %v", res.Outcome, st)
	}
	waitFor(t, "free to finish while deploy waits", func() bool {
		p, _ := h.svc.Get(t.Context(), p.ID)
		return p.Task("free").State == task.StateSucceeded
	})
	if h.events.count(task.EventApproval) != 1 {
		t.Fatalf("approval events: %d", h.events.count(task.EventApproval))
	}
	if _, err := h.svc.Approve(t.Context(), task.ApproveRequest{ID: p.ID, Task: "build", Approve: true}); !errors.Is(err, task.ErrState) {
		t.Fatalf("approving a task that does not wait: %v", err)
	}
	// One task waits, so it need not be named; a prefix names the plan.
	if _, err := h.svc.Approve(t.Context(), task.ApproveRequest{ID: p.ID[:4], Approve: true, By: "ada"}); err != nil {
		t.Fatal(err)
	}
	res = h.wait(p.ID, task.WaitFinished)
	if res.Plan.State != task.PlanSucceeded {
		t.Fatalf("after approval: %v", states(res.Plan))
	}
	if a := res.Plan.Task("deploy").Approval; a == nil || !a.Approved || a.By != "ada" {
		t.Fatalf("approval record: %+v", a)
	}
}

func TestRefusingATaskSkipsWhatNeedsIt(t *testing.T) {
	ms := newMachines(work(time.Millisecond), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	gated := spec("deploy")
	gated.Approve = true
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{gated, spec("announce", "deploy")}})
	h.wait(p.ID, task.WaitAttention)
	if _, err := h.svc.Approve(t.Context(), task.ApproveRequest{ID: p.ID, Task: "deploy", Note: "not today"}); err != nil {
		t.Fatal(err)
	}
	res := h.wait(p.ID, task.WaitFinished)
	st := states(res.Plan)
	if res.Plan.State != task.PlanFailed || st["deploy"] != task.StateCancelled || st["announce"] != task.StateSkipped {
		t.Fatalf("refused: %s %v", res.Plan.State, st)
	}
	if r := res.Plan.Task("deploy").Reason; r != "not approved: not today" {
		t.Fatalf("reason %q", r)
	}
	// Retrying asks again.
	if _, err := h.svc.Retry(t.Context(), p.ID, "deploy"); err != nil {
		t.Fatal(err)
	}
	res = h.wait(p.ID, task.WaitAttention)
	if st := states(res.Plan); st["deploy"] != task.StateApproval || st["announce"] != task.StateWaiting {
		t.Fatalf("after retry: %v", st)
	}
}

func TestFailedTasksRetryThenSkipWhatNeedsThem(t *testing.T) {
	ms := newMachines(work(time.Millisecond, "flaky"), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	flaky := spec("flaky")
	flaky.Retries = 2
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{flaky, spec("next", "flaky"), spec("other")}})
	res := h.wait(p.ID, task.WaitFinished)
	st := states(res.Plan)
	if res.Plan.State != task.PlanFailed || st["flaky"] != task.StateFailed || st["next"] != task.StateSkipped || st["other"] != task.StateSucceeded {
		t.Fatalf("plan: %v", st)
	}
	if n := res.Plan.Task("flaky").Attempt; n != 3 {
		t.Fatalf("attempts: %d, want 1 + 2 retries", n)
	}
	if r := res.Plan.Task("next").Reason; r != "it needs flaky, which failed" {
		t.Fatalf("skip reason %q", r)
	}
	// A person's retry gets the automatic retries again, and brings back
	// what was skipped. It is retried in the worktree it had.
	ms.execs[task.LocalMachine].act = work(time.Millisecond)
	if _, err := h.svc.Retry(t.Context(), p.ID, "flaky"); err != nil {
		t.Fatal(err)
	}
	res = h.wait(p.ID, task.WaitFinished)
	if res.Plan.State != task.PlanSucceeded || res.Plan.Task("flaky").Attempt != 4 {
		t.Fatalf("after retry: %s %v", res.Plan.State, states(res.Plan))
	}
	if _, err := h.svc.Retry(t.Context(), p.ID, "flaky"); !errors.Is(err, task.ErrState) {
		t.Fatalf("retrying a task that succeeded: %v", err)
	}
}

func TestAFailedNeedSkipsATaskWhileOtherNeedsRun(t *testing.T) {
	started := make(chan string, 4)
	ms := newMachines(func(ctx context.Context, a task.Attempt, progress func(task.Progress)) task.Outcome {
		if a.Task.Spec.ID == "bad" {
			return task.Outcome{State: task.StateFailed, Reason: "broken"}
		}
		return hold(started)(ctx, a, progress)
	}, task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{spec("slow"), spec("bad"), spec("both", "slow", "bad")}})
	<-started
	waitFor(t, "both to be skipped while slow runs", func() bool {
		p, _ := h.svc.Get(t.Context(), p.ID)
		return p.Task("both").State == task.StateSkipped && p.Task("slow").State == task.StateRunning
	})
	_, _ = h.svc.Cancel(t.Context(), p.ID, "")
	h.wait(p.ID, task.WaitFinished)
}

func TestCancellingStopsRunningAttempts(t *testing.T) {
	started := make(chan string, 4)
	ms := newMachines(hold(started), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{spec("a"), spec("b"), spec("c", "a")}})
	<-started
	<-started
	// One task alone first: the others go on.
	if _, err := h.svc.Cancel(t.Context(), p.ID, "b"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b to be cancelled", func() bool {
		p, _ := h.svc.Get(t.Context(), p.ID)
		return p.Task("b").State == task.StateCancelled
	})
	if p, _ := h.svc.Get(t.Context(), p.ID); p.Task("a").State != task.StateRunning || p.State != task.PlanRunning {
		t.Fatalf("cancelling b touched the rest: %v", states(p))
	}
	if _, err := h.svc.Cancel(t.Context(), p.ID, ""); err != nil {
		t.Fatal(err)
	}
	res := h.wait(p.ID, task.WaitFinished)
	if st := states(res.Plan); res.Plan.State != task.PlanCancelled || st["a"] != task.StateCancelled || st["c"] != task.StateCancelled {
		t.Fatalf("cancelled plan: %s %v", res.Plan.State, st)
	}
	if _, err := h.svc.Cancel(t.Context(), p.ID, ""); !errors.Is(err, task.ErrState) {
		t.Fatalf("cancelling a finished plan: %v", err)
	}
}

func TestAPlanSurvivesADaemonRestart(t *testing.T) {
	store := newMemStore()
	started := make(chan string, 4)
	first := newMachines(hold(started), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h := run(t, task.Config{SaveDelay: time.Millisecond}, store, first)
	gated := spec("gate")
	gated.Approve = true
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{spec("a"), spec("b", "a"), gated}})
	<-started
	h.wait(p.ID, task.WaitAttention)
	h.stop() // the daemon stops: the attempt is interrupted, its agent works on

	saved, _ := store.get(p.ID)
	if st := states(saved); st["a"] != task.StateRunning || st["gate"] != task.StateApproval {
		t.Fatalf("saved: %v", st)
	}
	if a := saved.Task("a"); a.AgentID != "agent-a" || a.Phase != task.PhasePrompted {
		t.Fatalf("saved attempt: %+v", a)
	}

	second := newMachines(work(time.Millisecond), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h2 := run(t, task.Config{}, store, second)
	waitFor(t, "a to be resumed", func() bool {
		_, attempts := second.execs[task.LocalMachine].snapshot()
		return len(attempts) > 0
	})
	_, attempts := second.execs[task.LocalMachine].snapshot()
	if len(attempts) == 0 || !attempts[0].Resume || attempts[0].Task.Spec.ID != "a" || attempts[0].Task.AgentID != "agent-a" || attempts[0].Task.Attempt != 1 {
		t.Fatalf("the restarted daemon picks a up where it was: %+v", attempts)
	}
	if _, err := h2.svc.Approve(t.Context(), task.ApproveRequest{ID: p.ID, Approve: true}); err != nil {
		t.Fatal(err)
	}
	res := h2.wait(p.ID, task.WaitFinished)
	if res.Plan.State != task.PlanSucceeded {
		t.Fatalf("after restart: %v", states(res.Plan))
	}
}

func TestAPlanBudgetCancelsWhatIsLeft(t *testing.T) {
	ms := newMachines(hold(nil), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{TimeoutMS: 50, Tasks: []task.Spec{spec("slow"), spec("after", "slow")}})
	res := h.wait(p.ID, task.WaitFinished)
	if res.Plan.State != task.PlanCancelled || !strings.Contains(res.Plan.Reason, "budget ran out") {
		t.Fatalf("plan: %s %q %v", res.Plan.State, res.Plan.Reason, states(res.Plan))
	}
	if r := res.Plan.Task("slow").Reason; !strings.Contains(r, "budget ran out") {
		t.Fatalf("task reason %q", r)
	}
}

func TestNoMachineFitsFailsTheTask(t *testing.T) {
	ms := newMachines(work(time.Millisecond), task.Machine{Name: task.LocalMachine, Capacity: 1, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	gpu := spec("train")
	gpu.Labels = []string{"gpu"}
	named := spec("far")
	named.Machine = "nowhere"
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{gpu, named, spec("near")}})
	res := h.wait(p.ID, task.WaitFinished)
	if r := res.Plan.Task("train").Reason; r != "no machine has the labels gpu" {
		t.Fatalf("train: %q", r)
	}
	if r := res.Plan.Task("far").Reason; !strings.Contains(r, `no machine called "nowhere"`) {
		t.Fatalf("far: %q", r)
	}
	if res.Plan.Task("near").State != task.StateSucceeded {
		t.Fatalf("near: %v", states(res.Plan))
	}
}

func TestFinishedPlansBeyondKeepAreForgotten(t *testing.T) {
	ms := newMachines(work(0), task.Machine{Name: task.LocalMachine, Capacity: 4, Local: true})
	store := newMemStore()
	h := run(t, task.Config{Keep: 2, SaveDelay: time.Millisecond}, store, ms)
	var ids []string
	for range 4 {
		p, err := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{spec("a")}})
		if err != nil {
			t.Fatal(err)
		}
		h.wait(p.ID, task.WaitFinished)
		ids = append(ids, p.ID)
	}
	list := h.svc.List(t.Context())
	if len(list) != 2 || list[0].ID != ids[3] || list[1].ID != ids[2] {
		t.Fatalf("kept: %+v", list)
	}
	waitFor(t, "the forgotten plans' files to go", func() bool {
		_, a := store.get(ids[0])
		_, b := store.get(ids[1])
		return !a && !b
	})
	if _, err := h.svc.Get(t.Context(), ids[0]); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("a forgotten plan: %v", err)
	}
}

func TestWaitForAChange(t *testing.T) {
	started := make(chan string, 1)
	ms := newMachines(hold(started), task.Machine{Name: task.LocalMachine, Capacity: 1, Local: true})
	h := run(t, task.Config{}, newMemStore(), ms)
	p, _ := h.svc.Submit(t.Context(), task.PlanSpec{Tasks: []task.Spec{spec("a")}})
	<-started
	cur, _ := h.svc.Get(t.Context(), p.ID)
	res, err := h.svc.Wait(t.Context(), task.WaitRequest{ID: p.ID, Until: task.WaitChange, After: cur.Rev, Timeout: 20 * time.Millisecond})
	if err != nil || res.Outcome != task.OutcomeTimeout {
		t.Fatalf("no change: %v %s", err, res.Outcome)
	}
	go func() { _, _ = h.svc.Cancel(context.Background(), p.ID, "") }()
	res, err = h.svc.Wait(t.Context(), task.WaitRequest{ID: p.ID, Until: task.WaitChange, After: cur.Rev, Timeout: 20 * time.Second})
	if err != nil || res.Outcome != task.OutcomeChanged || res.Plan.Rev <= cur.Rev {
		t.Fatalf("a change: %v %s %d", err, res.Outcome, res.Plan.Rev)
	}
	if _, err := h.svc.Wait(t.Context(), task.WaitRequest{ID: "nope"}); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("an unknown plan: %v", err)
	}
	h.wait(p.ID, task.WaitFinished)
}
