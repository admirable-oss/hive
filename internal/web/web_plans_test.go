package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/task"
)

func planEvent(seq uint64, typ string, v any) observer.Update {
	raw, _ := json.Marshal(v)
	return observer.Update{Event: &observer.Event{Seq: seq, Type: typ, Time: time.Now(), Data: raw}}
}

func TestModelFollowsPlans(t *testing.T) {
	m := NewModel()
	m.Apply(observer.Update{Snapshot: &observer.Snapshot{Seq: 10, Plans: []observer.Plan{{
		ID: "p1", Name: "ship", State: task.PlanRunning, Rev: 3, Total: 2, Counts: map[task.State]int{task.StateRunning: 1, task.StateWaiting: 1},
		Tasks: []observer.PlanTask{
			{PlanID: "p1", Rev: 3, ID: "api", EnvironmentID: "dev", State: task.StateRunning},
			{PlanID: "p1", Rev: 3, ID: "web", EnvironmentID: "dev", State: task.StateWaiting},
		},
	}}}}, time.Now())

	// An older task revision is ignored; a newer one counts.
	m.Apply(planEvent(11, task.EventTask, observer.PlanTask{PlanID: "p1", Rev: 2, ID: "api", EnvironmentID: "dev", State: task.StateFailed}), time.Now())
	m.Apply(planEvent(12, task.EventTask, observer.PlanTask{PlanID: "p1", Rev: 4, ID: "api", EnvironmentID: "dev", State: task.StateSucceeded}), time.Now())
	v := m.View()
	p := v.Plans["p1"]
	if p.Counts[task.StateSucceeded] != 1 || p.Counts[task.StateRunning] != 0 || p.Total != 2 || p.Tasks[0].State != task.StateSucceeded {
		t.Fatalf("plan: %+v", p)
	}
	if len(v.Activity) != 1 || !strings.Contains(v.Activity[0].Text, "task api of plan p1 is succeeded") {
		t.Fatalf("activity: %+v", v.Activity)
	}
	// The plan as a whole (observers that see all) keeps its tasks.
	m.Apply(planEvent(13, task.EventPlan, observer.Plan{ID: "p1", Name: "ship", State: task.PlanSucceeded, Rev: 6, Total: 2, Counts: map[task.State]int{task.StateSucceeded: 2}}), time.Now())
	if p := m.View().Plans["p1"]; p.State != task.PlanSucceeded || len(p.Tasks) != 2 || p.Counts[task.StateSucceeded] != 2 {
		t.Fatalf("after the plan event: %+v", p)
	}
	m.Apply(planEvent(14, task.EventRemoved, map[string][]string{"ids": {"p1"}}), time.Now())
	if _, ok := m.View().Plans["p1"]; ok {
		t.Fatal("a removed plan is still shown")
	}
}

func TestPlansShowOnlyWhatThePageSees(t *testing.T) {
	r := &shown{v: RuntimeView{Plans: map[string]observer.Plan{
		"p1": {ID: "p1", State: task.PlanRunning, Total: 2, Counts: map[task.State]int{task.StateRunning: 2}, Tasks: []observer.PlanTask{
			{PlanID: "p1", ID: "api", EnvironmentID: "dev", State: task.StateRunning},
			{PlanID: "p1", ID: "ops", EnvironmentID: "secret", State: task.StateRunning},
		}},
		"p2": {ID: "p2", State: task.PlanRunning, Total: 1}, // known only as a whole
	}}}
	scoped := &page{grant: Grant{Scope: observer.Scope{Environments: []string{"dev"}}}}
	plans := scoped.plansOf(r)
	if len(plans) != 1 || len(plans[0].Tasks) != 1 || plans[0].Tasks[0].ID != "api" {
		t.Fatalf("a scoped page sees %+v", plans)
	}
	all := &page{grant: Grant{Scope: observer.Scope{All: true}}}
	if plans := all.plansOf(r); len(plans) != 2 {
		t.Fatalf("a page that sees all sees %+v", plans)
	}
}
