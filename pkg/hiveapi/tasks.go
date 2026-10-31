package hiveapi

import (
	"context"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/task"
)

// Plans: many agents working through tasks with dependencies, limits,
// results and approvals (`hive task`).
//
//	plan, err := c.RunPlan(ctx, hiveapi.PlanSpec{
//		Defaults: &hiveapi.TaskSpec{Kind: "claude", EnvironmentID: "api", Isolate: true},
//		Tasks: []hiveapi.TaskSpec{
//			{ID: "schema", Prompt: "Design the session schema in docs/session.md."},
//			{ID: "api", Prompt: "Implement docs/session.md.", Needs: []string{"schema"}},
//		},
//	})
//	res, err := c.WaitPlan(ctx, plan.ID, hiveapi.PlanWaitOptions{})
//	report := hiveapi.NewReport(res.Plan)
type (
	Plan            = task.Plan
	PlanSpec        = task.PlanSpec
	PlanSummary     = task.Summary
	PlanState       = task.PlanState
	Task            = task.Task
	TaskSpec        = task.Spec
	TaskState       = task.State
	TaskView        = task.TaskView
	TaskResult      = task.Result
	Report          = task.Report
	TaskReport      = task.TaskReport
	PlanWaitResult  = task.WaitResult
	PlanWaitOptions = client.TaskWaitOptions
	ApproveOptions  = client.ApproveOptions
)

// Task states.
const (
	TaskWaiting   = task.StateWaiting
	TaskApproval  = task.StateApproval
	TaskQueued    = task.StateQueued
	TaskRunning   = task.StateRunning
	TaskBlocked   = task.StateBlocked
	TaskSucceeded = task.StateSucceeded
	TaskFailed    = task.StateFailed
	TaskCancelled = task.StateCancelled
	TaskSkipped   = task.StateSkipped
)

// Plan states.
const (
	PlanRunning   = task.PlanRunning
	PlanSucceeded = task.PlanSucceeded
	PlanFailed    = task.PlanFailed
	PlanCancelled = task.PlanCancelled
)

// Completion conditions (TaskSpec.Until).
const (
	TaskUntilDone    = task.UntilDone
	TaskUntilIdle    = task.UntilIdle
	TaskUntilMatch   = task.UntilMatch
	TaskUntilCommand = task.UntilCommand
)

// What WaitPlan waits for, and how it ended.
const (
	PlanWaitFinished  = task.WaitFinished
	PlanWaitAttention = task.WaitAttention
	PlanWaitChange    = task.WaitChange

	PlanOutcomeFinished  = task.OutcomeFinished
	PlanOutcomeAttention = task.OutcomeAttention
	PlanOutcomeChanged   = task.OutcomeChanged
	PlanOutcomeTimeout   = task.OutcomeTimeout
)

// ParsePlan reads a plan file (TOML), as `hive task run` does.
func ParsePlan(data []byte, source string) (PlanSpec, error) { return task.ParsePlan(data, source) }

// NewReport is what a plan did, task by task (so far, while it runs);
// WriteText prints it.
func NewReport(p Plan) Report { return task.NewReport(p, time.Now()) }

// RunPlan starts a plan.
func (c *Client) RunPlan(ctx context.Context, spec PlanSpec) (Plan, error) {
	return c.tasks.Run(ctx, spec)
}

// RunPlanFile starts the plan in a plan file's text; source names it in
// errors.
func (c *Client) RunPlanFile(ctx context.Context, toml, source string) (Plan, error) {
	return c.tasks.RunFile(ctx, toml, source)
}

// Plans lists every plan's summary, newest first.
func (c *Client) Plans(ctx context.Context) ([]PlanSummary, error) { return c.tasks.List(ctx) }

// Plan returns a plan, by ID or a unique prefix of one.
func (c *Client) Plan(ctx context.Context, id string) (Plan, error) { return c.tasks.Get(ctx, id) }

// WaitPlan waits for a plan (or one task) to finish, need a person, or
// change.
func (c *Client) WaitPlan(ctx context.Context, id string, opts PlanWaitOptions) (PlanWaitResult, error) {
	return c.tasks.Wait(ctx, id, opts)
}

// CancelPlan cancels a plan, or (with taskID) one of its tasks.
func (c *Client) CancelPlan(ctx context.Context, id, taskID string) (Plan, error) {
	return c.tasks.Cancel(ctx, id, taskID)
}

// RetryPlan runs a plan's failed, cancelled and skipped tasks again, or
// (with taskID) one of them.
func (c *Client) RetryPlan(ctx context.Context, id, taskID string) (Plan, error) {
	return c.tasks.Retry(ctx, id, taskID)
}

// ApproveTask lets a task waiting for approval start, or with Deny cancels
// it. taskID may be empty when one task waits.
func (c *Client) ApproveTask(ctx context.Context, id, taskID string, opts ApproveOptions) (Plan, error) {
	return c.tasks.Approve(ctx, id, taskID, opts)
}

// AssignTask gives a task, or (taskID "") a plan and its unfinished tasks,
// to another member (by name or ID), or to "owner", the runtime's own
// user. The agents working on them go with them.
func (c *Client) AssignTask(ctx context.Context, id, taskID, to string) (Plan, error) {
	return c.tasks.Assign(ctx, id, taskID, to)
}
