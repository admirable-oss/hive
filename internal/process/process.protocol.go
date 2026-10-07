package process

import (
	"context"
	"errors"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/protocol"
)

type idParams struct {
	ID string `json:"id"`
}

type listParams struct {
	EnvironmentID string `json:"environment_id"` // empty lists every environment
}

type logsParams struct {
	ID   string `json:"id"`
	Tail int    `json:"tail"`
}

type logsResult struct {
	Logs string `json:"logs"`
}

const defaultTail = 50

// Register exposes svc on the wire as process.*.
func Register(r *protocol.Router, svc Service) {
	r.MustRegister("process.start", protocol.Method(func(ctx context.Context, req StartRequest) (Process, error) {
		p, err := svc.Start(ctx, req)
		return p, wireError(err)
	}))
	r.MustRegister("process.get", protocol.Method(func(ctx context.Context, p idParams) (Process, error) {
		proc, err := svc.Get(ctx, p.ID)
		return proc, wireError(err)
	}))
	r.MustRegister("process.list", protocol.Method(func(ctx context.Context, p listParams) ([]Process, error) {
		procs, err := svc.List(ctx, p.EnvironmentID)
		return procs, wireError(err)
	}))
	r.MustRegister("process.stop", protocol.Method(func(ctx context.Context, p idParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(svc.Stop(ctx, p.ID))
	}))
	r.MustRegister("process.logs", protocol.Method(func(ctx context.Context, p logsParams) (logsResult, error) {
		if p.Tail <= 0 {
			p.Tail = defaultTail
		}
		logs, err := svc.Logs(ctx, p.ID, p.Tail)
		return logsResult{Logs: logs}, wireError(err)
	}))
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, environment.ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrCommandRequired), errors.Is(err, environment.ErrInvalidID):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}
