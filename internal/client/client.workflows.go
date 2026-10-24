package client

import (
	"context"
	"encoding/json"
	"time"

	"github.com/admirable-oss/hive/internal/workflow"
)

// Workflows is the typed workflow API (workflow.*) over any Client.
type Workflows struct {
	c Client
}

// NewWorkflows wraps c.
func NewWorkflows(c Client) Workflows { return Workflows{c: c} }

func workflowCall[R any](ctx context.Context, w Workflows, method string, params any) (R, error) {
	var r R
	err := w.c.Call(ctx, method, params, &r)
	return r, err
}

// List returns the workflow files the daemon knows, by name.
func (w Workflows) List(ctx context.Context) ([]workflow.Workflow, error) {
	return workflowCall[[]workflow.Workflow](ctx, w, "workflow.list", struct{}{})
}

// Get returns one workflow, with its spec and text.
func (w Workflows) Get(ctx context.Context, name string) (workflow.Workflow, error) {
	return workflowCall[workflow.Workflow](ctx, w, "workflow.get", workflow.NameParams{Name: name})
}

// Validate parses the text of a workflow file.
func (w Workflows) Validate(ctx context.Context, text, source string) (workflow.Spec, error) {
	return workflowCall[workflow.Spec](ctx, w, "workflow.validate", workflow.ValidateParams{TOML: text, Source: source})
}

// RunOptions are a run's inputs, directory and who asked.
type RunOptions struct {
	Inputs map[string]string
	Dir    string
	By     string
}

// Run starts a run of a known workflow.
func (w Workflows) Run(ctx context.Context, name string, o RunOptions) (workflow.Run, error) {
	return workflowCall[workflow.Run](ctx, w, "workflow.run", workflow.RunParams{Name: name, Inputs: o.Inputs, Dir: o.Dir, By: o.By})
}

// RunFile starts a run of the text of a workflow file; source names it.
func (w Workflows) RunFile(ctx context.Context, text, source string, o RunOptions) (workflow.Run, error) {
	return workflowCall[workflow.Run](ctx, w, "workflow.run", workflow.RunParams{TOML: text, Source: source, Inputs: o.Inputs, Dir: o.Dir, By: o.By})
}

// Runs lists runs, newest first: of one workflow when named, at most
// limit when positive.
func (w Workflows) Runs(ctx context.Context, name string, limit int) ([]workflow.RunSummary, error) {
	return workflowCall[[]workflow.RunSummary](ctx, w, "workflow.runs", workflow.RunsParams{Workflow: name, Limit: limit})
}

// Status returns a run by its ID or a unique prefix.
func (w Workflows) Status(ctx context.Context, id string) (workflow.Run, error) {
	return workflowCall[workflow.Run](ctx, w, "workflow.status", workflow.RunRef{ID: id})
}

// Wait waits for a run to finish, or for timeout (0: no bound).
func (w Workflows) Wait(ctx context.Context, id string, timeout time.Duration) (workflow.WaitResult, error) {
	return workflowCall[workflow.WaitResult](ctx, w, "workflow.wait", workflow.WaitParams{ID: id, TimeoutMS: timeout.Milliseconds()})
}

// Cancel cancels a run.
func (w Workflows) Cancel(ctx context.Context, id string) (workflow.Run, error) {
	return workflowCall[workflow.Run](ctx, w, "workflow.cancel", workflow.RunRef{ID: id})
}

// Rerun starts a new run from a run's recorded text, inputs and trigger.
func (w Workflows) Rerun(ctx context.Context, id, by string) (workflow.Run, error) {
	return workflowCall[workflow.Run](ctx, w, "workflow.rerun", workflow.RunRef{ID: id, By: by})
}

// Fire fires the workflows with a webhook trigger called name.
func (w Workflows) Fire(ctx context.Context, name string, data any) ([]workflow.RunSummary, error) {
	var raw json.RawMessage
	if data != nil {
		var err error
		if raw, err = json.Marshal(data); err != nil {
			return nil, err
		}
	}
	return workflowCall[[]workflow.RunSummary](ctx, w, "workflow.fire", workflow.FireParams{Webhook: name, Data: raw})
}

// Trust lets a shared workflow's triggers fire while its file keeps its
// text; revoke stops them.
func (w Workflows) Trust(ctx context.Context, name string, revoke bool) (workflow.Workflow, error) {
	return workflowCall[workflow.Workflow](ctx, w, "workflow.trust", workflow.TrustParams{Name: name, Revoke: revoke})
}

// Reload reads the workflow files again.
func (w Workflows) Reload(ctx context.Context) ([]workflow.Workflow, error) {
	return workflowCall[[]workflow.Workflow](ctx, w, "workflow.reload", struct{}{})
}
