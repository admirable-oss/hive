package core

import (
	"context"
	"fmt"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// WorkflowRun is a run of a workflow, as the app shows it (hive workflow).
type WorkflowRun struct {
	ID       string `json:"id"`
	Workflow string `json:"workflow"`
	State    string `json:"state"`
	// Trigger is what started it: manual, git, event, schedule, webhook.
	Trigger string `json:"trigger"`
	Total   int    `json:"total"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
	// Current are the steps running.
	Current   []string  `json:"current"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	// Machine is the saved machine it runs on ("" for this computer).
	Machine string `json:"machine,omitempty"`
}

// workflowRunsRead is how many runs the app reads from each daemon.
const workflowRunsRead = 20

func workflowRunsOf(list []hiveapi.WorkflowRunSummary) []WorkflowRun {
	out := make([]WorkflowRun, 0, len(list))
	for _, r := range list {
		out = append(out, WorkflowRun{
			ID: r.ID, Workflow: r.Workflow, State: string(r.State), Trigger: r.Trigger, Total: r.Total,
			Done:   r.Counts["succeeded"] + r.Counts["failed"] + r.Counts["skipped"] + r.Counts["cancelled"],
			Failed: r.Counts["failed"], Current: nonNil(r.Current), Reason: r.Reason,
			CreatedAt: r.CreatedAt, EndedAt: r.EndedAt,
		})
	}
	return out
}

// failedRunsLocked announces runs that failed since the last refresh:
// once each, and not on the first snapshot.
func (h *Hub) failedRunsLocked(ws Workspace) []Notification {
	next := map[string]bool{}
	var out []Notification
	for _, r := range ws.Workflows {
		if r.State != string(hiveapi.WorkflowFailed) {
			continue
		}
		next[r.ID] = true
		if h.failedRuns == nil || h.failedRuns[r.ID] || !h.settings.notifies("blocked") || h.settings.SystemNotifications {
			continue
		}
		out = append(out, Notification{ID: "workflow:" + r.ID, Title: "Workflow " + r.Workflow, Body: cmp(r.Reason, "a step failed")})
	}
	h.failedRuns = next
	return out
}

// CancelWorkflowRun cancels a run: its agents' tasks and its commands stop.
func (h *Hub) CancelWorkflowRun(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		if _, err := c.CancelWorkflowRun(ctx, id); err != nil {
			return fmt.Errorf("cancel run %s: %w", id, err)
		}
		return nil
	})
}

// RerunWorkflow runs a run again, with its recorded text, inputs and
// trigger, and returns the new run's ID.
func (h *Hub) RerunWorkflow(id string) (string, error) {
	if err := validID(id); err != nil {
		return "", err
	}
	var run string
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		r, err := c.RerunWorkflow(ctx, id)
		if err != nil {
			return fmt.Errorf("run %s again: %w", id, err)
		}
		run = r.ID
		return nil
	})
	return run, err
}
