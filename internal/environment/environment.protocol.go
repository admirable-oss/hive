package environment

import (
	"context"
	"errors"

	"github.com/admirable-oss/hive/internal/protocol"
)

type idParams struct {
	ID string `json:"id"`
}

// Register exposes svc on the wire as environment.*.
func Register(r *protocol.Router, svc Service) {
	r.MustRegister("environment.list", protocol.Method(func(ctx context.Context, _ struct{}) ([]Environment, error) {
		return svc.List(ctx)
	}))
	r.MustRegister("environment.create", protocol.Method(func(ctx context.Context, p idParams) (Environment, error) {
		env, err := svc.Create(ctx, p.ID)
		return env, wireError(err)
	}))
	r.MustRegister("environment.get", protocol.Method(func(ctx context.Context, p idParams) (Environment, error) {
		env, err := svc.Get(ctx, p.ID)
		return env, wireError(err)
	}))
	r.MustRegister("environment.remove", protocol.Method(func(ctx context.Context, p idParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(svc.Delete(ctx, p.ID))
	}))
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalidID), errors.Is(err, ErrAlreadyExists):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}
