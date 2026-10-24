package hiveapi

import (
	"context"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/workflow"
)

// Workflows: reusable steps (agents' tasks, commands, plugin actions) run
// by triggers or on request, each run recorded with its inputs
// (`hive workflow`).
//
//	run, err := c.RunWorkflow(ctx, "review", hiveapi.WorkflowRunOptions{Inputs: map[string]string{"branch": "main"}})
//	res, err := c.WaitWorkflowRun(ctx, run.ID, 0)
//	report := hiveapi.NewWorkflowReport(res.Run)
type (
	Workflow           = workflow.Workflow
	WorkflowSpec       = workflow.Spec
	WorkflowStep       = workflow.StepSpec
	WorkflowTrigger    = workflow.TriggerSpec
	WorkflowInput      = workflow.InputSpec
	WorkflowRun        = workflow.Run
	WorkflowRunSummary = workflow.RunSummary
	WorkflowRunState   = workflow.State
	WorkflowStepRun    = workflow.StepRun
	WorkflowStepState  = workflow.StepState
	WorkflowResult     = workflow.Result
	WorkflowReport     = workflow.Report
	WorkflowWaitResult = workflow.WaitResult
	WorkflowRunOptions = client.RunOptions
	WorkflowStepView   = workflow.StepView
)

// Workflow run states.
const (
	WorkflowQueued    = workflow.StateQueued
	WorkflowRunning   = workflow.StateRunning
	WorkflowSucceeded = workflow.StateSucceeded
	WorkflowFailed    = workflow.StateFailed
	WorkflowCancelled = workflow.StateCancelled
)

// Workflow events (see Events).
const (
	EventWorkflowRun     = workflow.EventRun
	EventWorkflowStep    = workflow.EventStep
	EventWorkflowRemoved = workflow.EventRemoved
	EventWorkflowChanged = workflow.EventChanged
)

// ParseWorkflow reads a workflow file; source names it in errors.
func ParseWorkflow(data []byte, source string) (WorkflowSpec, error) {
	return workflow.Parse(data, source)
}

// NewWorkflowReport is what a run did, step by step (so far, while it
// runs); WriteText prints it.
func NewWorkflowReport(r WorkflowRun) WorkflowReport { return workflow.NewReport(r, time.Now()) }

// Workflows lists the workflow files the daemon knows, by name.
func (c *Client) Workflows(ctx context.Context) ([]Workflow, error) { return c.wfs.List(ctx) }

// Workflow returns one workflow, with its spec and text.
func (c *Client) Workflow(ctx context.Context, name string) (Workflow, error) {
	return c.wfs.Get(ctx, name)
}

// ValidateWorkflow has the daemon check the text of a workflow file.
func (c *Client) ValidateWorkflow(ctx context.Context, text, source string) (WorkflowSpec, error) {
	return c.wfs.Validate(ctx, text, source)
}

// RunWorkflow starts a run of a known workflow.
func (c *Client) RunWorkflow(ctx context.Context, name string, o WorkflowRunOptions) (WorkflowRun, error) {
	return c.wfs.Run(ctx, name, o)
}

// RunWorkflowFile starts a run of the text of a workflow file.
func (c *Client) RunWorkflowFile(ctx context.Context, text, source string, o WorkflowRunOptions) (WorkflowRun, error) {
	return c.wfs.RunFile(ctx, text, source, o)
}

// WorkflowRuns lists runs, newest first: of one workflow when named, at
// most limit when positive.
func (c *Client) WorkflowRuns(ctx context.Context, name string, limit int) ([]WorkflowRunSummary, error) {
	return c.wfs.Runs(ctx, name, limit)
}

// WorkflowRun returns a run by its ID or a unique prefix.
func (c *Client) WorkflowRun(ctx context.Context, id string) (WorkflowRun, error) {
	return c.wfs.Status(ctx, id)
}

// WaitWorkflowRun waits for a run to finish, or for timeout (0: no bound).
func (c *Client) WaitWorkflowRun(ctx context.Context, id string, timeout time.Duration) (WorkflowWaitResult, error) {
	return c.wfs.Wait(ctx, id, timeout)
}

// CancelWorkflowRun cancels a run.
func (c *Client) CancelWorkflowRun(ctx context.Context, id string) (WorkflowRun, error) {
	return c.wfs.Cancel(ctx, id)
}

// RerunWorkflow starts a new run from a run's recorded text, inputs and
// trigger.
func (c *Client) RerunWorkflow(ctx context.Context, id string) (WorkflowRun, error) {
	return c.wfs.Rerun(ctx, id, "hiveapi")
}

// FireWebhook fires the workflows with a webhook trigger called name, with
// data as their trigger's payload.
func (c *Client) FireWebhook(ctx context.Context, name string, data any) ([]WorkflowRunSummary, error) {
	return c.wfs.Fire(ctx, name, data)
}
