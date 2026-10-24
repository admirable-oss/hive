package core

import (
	"testing"

	"github.com/admirable-oss/hive/internal/workflow"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func TestWorkflowRunsAndTheirFailuresNotifyOnce(t *testing.T) {
	list := []hiveapi.WorkflowRunSummary{
		{
			ID: "r1", Workflow: "review", State: workflow.StateRunning, Trigger: "git", Total: 4,
			Counts: map[workflow.StepState]int{workflow.StepSucceeded: 1, workflow.StepRunning: 1, workflow.StepWaiting: 2}, Current: []string{"test"},
		},
		{
			ID: "r0", Workflow: "nightly", State: workflow.StateFailed, Trigger: "schedule", Total: 2, Reason: "step test failed",
			Counts: map[workflow.StepState]int{workflow.StepSucceeded: 1, workflow.StepFailed: 1},
		},
	}
	got := workflowRunsOf(list)
	if len(got) != 2 || got[0].Done != 1 || got[0].Current[0] != "test" || got[1].Done != 2 || got[1].Failed != 1 || got[1].Current == nil {
		t.Fatalf("view model: %+v", got)
	}

	h := &Hub{settings: DefaultSettings()}
	ws := Workspace{Workflows: got}
	if n := h.failedRunsLocked(ws); len(n) != 0 {
		t.Fatalf("the first snapshot announces nothing: %+v", n)
	}
	got[0].State, got[0].Reason = "failed", "step test failed: exit status 1"
	n := h.failedRunsLocked(ws)
	if len(n) != 1 || n[0].Title != "Workflow review" || n[0].Body != "step test failed: exit status 1" {
		t.Fatalf("a run that failed: %+v", n)
	}
	if n := h.failedRunsLocked(ws); len(n) != 0 {
		t.Fatalf("announced once: %+v", n)
	}
}
