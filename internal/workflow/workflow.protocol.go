package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

// NameParams names a workflow.
type NameParams struct {
	Name string `json:"name" desc:"the workflow's name (hive workflow list)"`
}

// ValidateParams is workflow.validate's request.
type ValidateParams struct {
	TOML   string `json:"toml" desc:"the text of a workflow file"`
	Source string `json:"source,omitempty" desc:"the file's name, for errors (and the workflow's name when it gives none)"`
}

// RunParams is workflow.run's request: a workflow by name, or the text of
// one.
type RunParams struct {
	Name   string            `json:"name,omitempty" desc:"a known workflow's name"`
	TOML   string            `json:"toml,omitempty" desc:"the text of a workflow file (instead of name)"`
	Source string            `json:"source,omitempty" desc:"the file's name, for errors"`
	Inputs map[string]string `json:"inputs,omitempty" desc:"the workflow's inputs, by name"`
	Dir    string            `json:"dir,omitempty" desc:"the directory steps run in unless they name an environment (absolute)"`
	By     string            `json:"by,omitempty" desc:"who asked, for the run's record"`
}

// RunsParams is workflow.runs's request.
type RunsParams struct {
	Workflow string `json:"workflow,omitempty" desc:"only this workflow's runs"`
	Limit    int    `json:"limit,omitempty" desc:"at most this many (0: every one kept)"`
}

// RunRef names a run.
type RunRef struct {
	ID string `json:"id" desc:"the run's ID, or a unique prefix of it"`
	By string `json:"by,omitempty" desc:"who asks, for a rerun's record"`
}

// WaitParams is workflow.wait's request.
type WaitParams struct {
	ID string `json:"id" desc:"the run's ID, or a unique prefix of it"`
	// TimeoutMS bounds the wait; 0 waits until the caller gives up.
	TimeoutMS int64 `json:"timeout_ms,omitempty" desc:"give up after this many milliseconds (0: never)"`
}

// FireParams is workflow.fire's request.
type FireParams struct {
	Webhook string          `json:"webhook" desc:"the webhook's name, as workflows' [[on]] webhook says"`
	Data    json.RawMessage `json:"data,omitempty" desc:"the payload, which expressions read as trigger.data"`
}

// TrustParams is workflow.trust's request.
type TrustParams struct {
	Name   string `json:"name" desc:"the workflow's name"`
	Revoke bool   `json:"revoke,omitempty" desc:"stop its triggers instead"`
}

// Register exposes the service on the wire as workflow.*.
func Register(r *protocol.Router, s *Service) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }

	m("workflow.list", protocol.Method(func(ctx context.Context, _ struct{}) ([]Workflow, error) {
		return s.Workflows(ctx), nil
	}))
	m("workflow.get", protocol.Method(func(ctx context.Context, p NameParams) (Workflow, error) {
		w, err := s.Workflow(ctx, p.Name)
		return w, wireError(err)
	}))
	m("workflow.validate", protocol.Method(func(_ context.Context, p ValidateParams) (Spec, error) {
		spec, err := s.Validate(p.TOML, p.Source)
		return spec, wireError(err)
	}))
	m("workflow.run", protocol.Method(func(ctx context.Context, p RunParams) (Run, error) {
		run, err := s.Submit(ctx, SubmitRequest(p))
		return run, wireError(err)
	}))
	m("workflow.runs", protocol.Method(func(ctx context.Context, p RunsParams) ([]RunSummary, error) {
		return s.List(ctx, p.Workflow, p.Limit), nil
	}))
	m("workflow.status", protocol.Method(func(ctx context.Context, p RunRef) (Run, error) {
		run, err := s.Get(ctx, p.ID)
		return run, wireError(err)
	}))
	m("workflow.wait", protocol.Method(func(ctx context.Context, p WaitParams) (WaitResult, error) {
		res, err := s.Wait(ctx, p.ID, time.Duration(p.TimeoutMS)*time.Millisecond)
		return res, wireError(err)
	}))
	m("workflow.cancel", protocol.Method(func(ctx context.Context, p RunRef) (Run, error) {
		run, err := s.Cancel(ctx, p.ID)
		return run, wireError(err)
	}))
	m("workflow.rerun", protocol.Method(func(ctx context.Context, p RunRef) (Run, error) {
		run, err := s.Rerun(ctx, p.ID, p.By)
		return run, wireError(err)
	}))
	m("workflow.fire", protocol.Method(func(ctx context.Context, p FireParams) ([]RunSummary, error) {
		var data any
		if len(p.Data) > 0 {
			if err := json.Unmarshal(p.Data, &data); err != nil {
				return nil, wireError(invalid("data is not JSON: %v", err))
			}
		}
		runs, err := s.Fire(ctx, p.Webhook, data)
		return runs, wireError(err)
	}))
	m("workflow.trust", protocol.Method(func(ctx context.Context, p TrustParams) (Workflow, error) {
		w, err := s.Trust(ctx, p.Name, p.Revoke)
		return w, wireError(err)
	}))
	m("workflow.reload", protocol.Method(func(ctx context.Context, _ struct{}) ([]Workflow, error) {
		return s.Reload(ctx), nil
	}))
}

// WireError gives domain errors their protocol codes, for methods served
// beside the service's own.
func WireError(err error) error { return wireError(err) }

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrAmbiguous):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, ErrState):
		return protocol.NewError(protocol.ErrorCodeConflict, err)
	case errors.Is(err, ErrNewer):
		return protocol.NewError(protocol.ErrorCodeUnavailable, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	}
	return err
}
