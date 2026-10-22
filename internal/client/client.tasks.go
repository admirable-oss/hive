package client

import (
	"context"
	"time"

	"github.com/admirable-oss/hive/internal/task"
)

// Tasks is the typed plan API (task.*) over any Client.
type Tasks struct {
	c Client
}

// NewTasks wraps c.
func NewTasks(c Client) Tasks { return Tasks{c: c} }

func taskCall[R any](ctx context.Context, t Tasks, method string, params any) (R, error) {
	var r R
	err := t.c.Call(ctx, method, params, &r)
	return r, err
}

// Run starts a plan.
func (t Tasks) Run(ctx context.Context, spec task.PlanSpec) (task.Plan, error) {
	return taskCall[task.Plan](ctx, t, "task.run", task.RunParams{Plan: &spec})
}

// RunFile starts the plan in a plan file's text; source names it in
// errors. The daemon parses it, so every client reads plans alike.
func (t Tasks) RunFile(ctx context.Context, toml, source string) (task.Plan, error) {
	return taskCall[task.Plan](ctx, t, "task.run", task.RunParams{TOML: toml, Source: source})
}

// List returns every plan's summary, newest first.
func (t Tasks) List(ctx context.Context) ([]task.Summary, error) {
	return taskCall[[]task.Summary](ctx, t, "task.list", struct{}{})
}

// Get returns a plan (by ID, or a unique prefix of one).
func (t Tasks) Get(ctx context.Context, id string) (task.Plan, error) {
	return taskCall[task.Plan](ctx, t, "task.get", task.Ref{ID: id})
}

// TaskWaitOptions say what Wait waits for; see task.WaitRequest.
type TaskWaitOptions struct {
	// Task narrows the wait to one task.
	Task  string
	Until task.WaitUntil
	// After is the plan revision already seen, for task.WaitChange.
	After   uint64
	Timeout time.Duration
}

// Wait waits for a plan to finish, need a person, or change.
func (t Tasks) Wait(ctx context.Context, id string, o TaskWaitOptions) (task.WaitResult, error) {
	return taskCall[task.WaitResult](ctx, t, "task.wait", task.WaitParams{
		ID: id, Task: o.Task, Until: o.Until, After: o.After, TimeoutMS: o.Timeout.Milliseconds(),
	})
}

// Cancel cancels a plan, or (with taskID) one of its tasks.
func (t Tasks) Cancel(ctx context.Context, id, taskID string) (task.Plan, error) {
	return taskCall[task.Plan](ctx, t, "task.cancel", task.Ref{ID: id, Task: taskID})
}

// Retry runs a plan's failed, cancelled and skipped tasks again, or (with
// taskID) one of them.
func (t Tasks) Retry(ctx context.Context, id, taskID string) (task.Plan, error) {
	return taskCall[task.Plan](ctx, t, "task.retry", task.Ref{ID: id, Task: taskID})
}

// ApproveOptions qualify an approval.
type ApproveOptions struct {
	// Deny refuses: the task is cancelled.
	Deny bool
	By   string
	Note string
}

// Approve decides on a task waiting for approval (taskID may be empty when
// one task waits).
func (t Tasks) Approve(ctx context.Context, id, taskID string, o ApproveOptions) (task.Plan, error) {
	return taskCall[task.Plan](ctx, t, "task.approve", task.ApproveParams{ID: id, Task: taskID, Deny: o.Deny, By: o.By, Note: o.Note})
}
