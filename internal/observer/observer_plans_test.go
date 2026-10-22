package observer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/task"
)

func TestPlansAreRedactedAndScoped(t *testing.T) {
	plan := task.Plan{ID: "p1", Name: "ship", State: task.PlanRunning, Rev: 7, CreatedAt: time.Now(), Tasks: []task.Task{
		{
			Spec: task.Spec{ID: "api", Prompt: "SECRET prompt", Kind: "claude", EnvironmentID: "api"}, State: task.StateRunning, PaneID: "pa",
			Reason: "SECRET reason", Result: &task.Result{Summary: "SECRET summary", Screen: []string{"SECRET screen"}},
		},
		{Spec: task.Spec{ID: "web", Prompt: "x", Kind: "codex", EnvironmentID: "web"}, State: task.StateQueued},
	}}
	all, ok := PlanView(plan, Scope{All: true})
	if !ok || all.Total != 2 || all.Counts[task.StateRunning] != 1 || all.Tasks[0].PaneID != "pa" || all.Tasks[0].Rev != 7 {
		t.Fatalf("all: %+v", all)
	}
	data, _ := json.Marshal(all)
	if strings.Contains(string(data), "SECRET") {
		t.Fatalf("a plan view leaks prompts, reasons or results: %s", data)
	}
	// A scope sees its environment's tasks, or a pane's, and counts only them.
	api, ok := PlanView(plan, Scope{Environments: []string{"api"}})
	if !ok || api.Total != 1 || api.Tasks[0].ID != "api" || api.Counts[task.StateQueued] != 0 {
		t.Fatalf("api scope: %+v", api)
	}
	if v, ok := PlanView(plan, Scope{Panes: []string{"pa"}}); !ok || v.Total != 1 {
		t.Fatalf("pane scope: %+v", v)
	}
	if _, ok := PlanView(plan, Scope{Environments: []string{"other"}}); ok {
		t.Fatal("a plan with no task in scope is seen")
	}

	// Events: a task's is seen where the task is; a plan's only with all.
	taskEv, _ := json.Marshal(plan.Tasks[0].View(&plan))
	ev, ok := Translate(event.Event{Seq: 1, Type: task.EventTask, Data: taskEv}, fakeLocator{})
	if !ok || !ev.Visible(Scope{Environments: []string{"api"}}) || ev.Visible(Scope{Environments: []string{"web"}}) || strings.Contains(string(ev.Data), "SECRET") {
		t.Fatalf("task event: %+v %s", ev, ev.Data)
	}
	planEv, _ := json.Marshal(plan.Summary())
	ev, ok = Translate(event.Event{Seq: 2, Type: task.EventPlan, Data: planEv}, fakeLocator{})
	if !ok || !ev.Visible(Scope{All: true}) || ev.Visible(Scope{Environments: []string{"api"}}) {
		t.Fatalf("plan event: %+v", ev)
	}
	var v Plan
	if json.Unmarshal(ev.Data, &v) != nil || v.Total != 2 || v.Tasks != nil {
		t.Fatalf("plan event data: %s", ev.Data)
	}
}
