package task

import (
	"context"
	"errors"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

// RunParams is task.run's request: a plan, as structured data or as the
// text of a plan file.
type RunParams struct {
	Plan *PlanSpec `json:"plan,omitempty" desc:"the plan, as data"`
	TOML string    `json:"toml,omitempty" desc:"the plan, as the text of a plan file (instead of plan)"`
	// Source names the plan file in errors.
	Source string `json:"source,omitempty" desc:"the plan file's name, for errors"`
}

// Ref names a plan, and optionally one of its tasks.
type Ref struct {
	ID   string `json:"id" desc:"the plan's ID, or a unique prefix of it"`
	Task string `json:"task,omitempty" desc:"one of its tasks"`
}

// WaitParams is task.wait's request.
type WaitParams struct {
	ID    string    `json:"id" desc:"the plan's ID, or a unique prefix of it"`
	Task  string    `json:"task,omitempty" desc:"wait for this task rather than the whole plan"`
	Until WaitUntil `json:"until,omitempty" desc:"finished (default), attention (also a task waiting for a person) or change"`
	After uint64    `json:"after,omitempty" desc:"with until=change: the plan revision already seen"`
	// TimeoutMS bounds the wait; 0 waits until the caller gives up.
	TimeoutMS int64 `json:"timeout_ms,omitempty" desc:"give up after this many milliseconds (0: never)"`
}

// ApproveParams is task.approve's request.
type ApproveParams struct {
	ID   string `json:"id" desc:"the plan's ID, or a unique prefix of it"`
	Task string `json:"task,omitempty" desc:"the task (may be left out when one task waits)"`
	// Deny refuses instead: the task is cancelled, and what needs it is
	// skipped.
	Deny bool   `json:"deny,omitempty" desc:"refuse: cancel the task instead of starting it"`
	By   string `json:"by,omitempty" desc:"who decides, for the task's log"`
	Note string `json:"note,omitempty" desc:"why, for the task's log"`
}

// Register exposes the service on the wire as task.*.
func Register(r *protocol.Router, s *Service) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }

	m("task.run", protocol.Method(func(ctx context.Context, p RunParams) (Plan, error) {
		spec, err := p.spec()
		if err != nil {
			return Plan{}, wireError(err)
		}
		plan, err := s.Submit(ctx, spec)
		return plan, wireError(err)
	}))
	m("task.list", protocol.Method(func(ctx context.Context, _ struct{}) ([]Summary, error) {
		return s.List(ctx), nil
	}))
	m("task.get", protocol.Method(func(ctx context.Context, p Ref) (Plan, error) {
		plan, err := s.Get(ctx, p.ID)
		return plan, wireError(err)
	}))
	m("task.wait", protocol.Method(func(ctx context.Context, p WaitParams) (WaitResult, error) {
		res, err := s.Wait(ctx, WaitRequest{
			ID: p.ID, Task: p.Task, Until: p.Until, After: p.After,
			Timeout: time.Duration(p.TimeoutMS) * time.Millisecond,
		})
		return res, wireError(err)
	}))
	m("task.cancel", protocol.Method(func(ctx context.Context, p Ref) (Plan, error) {
		plan, err := s.Cancel(ctx, p.ID, p.Task)
		return plan, wireError(err)
	}))
	m("task.retry", protocol.Method(func(ctx context.Context, p Ref) (Plan, error) {
		plan, err := s.Retry(ctx, p.ID, p.Task)
		return plan, wireError(err)
	}))
	m("task.approve", protocol.Method(func(ctx context.Context, p ApproveParams) (Plan, error) {
		plan, err := s.Approve(ctx, ApproveRequest{ID: p.ID, Task: p.Task, Approve: !p.Deny, By: p.By, Note: p.Note})
		return plan, wireError(err)
	}))
}

// spec returns the plan the request carries.
func (p RunParams) spec() (PlanSpec, error) {
	switch {
	case p.Plan != nil && p.TOML != "":
		return PlanSpec{}, errors.Join(ErrInvalid, errors.New("give plan or toml, not both"))
	case p.Plan != nil:
		return *p.Plan, nil
	case p.TOML != "":
		return ParsePlan([]byte(p.TOML), cmpStr(p.Source, "plan"))
	}
	return PlanSpec{}, errors.Join(ErrInvalid, errors.New("give a plan"))
}

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
	case errors.Is(err, ErrUnreachable):
		return protocol.NewError(protocol.ErrorCodeUnavailable, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	}
	return err
}
