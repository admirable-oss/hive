package project

import (
	"context"
	"errors"
	"io/fs"

	"github.com/admirable-oss/hive/internal/protocol"
)

// DirParams names a project by a directory.
type DirParams struct {
	Dir string `json:"dir" desc:"a directory of the project, absolute on the daemon's machine; hive.toml is found there or above it"`
}

// TrustParams is project.trust's request.
type TrustParams struct {
	Dir    string `json:"dir" desc:"a directory of the project (absolute)"`
	Revoke bool   `json:"revoke,omitempty" desc:"revoke the trust instead"`
}

// PlanParams is project.plan's request.
type PlanParams struct {
	Dir  string `json:"dir" desc:"a directory of the project (absolute)"`
	Down bool   `json:"down,omitempty" desc:"plan hive down instead of hive up"`
	// Vars are values from the caller's shell, for the names the project
	// declares; others are ignored.
	Vars map[string]string `json:"vars,omitempty" desc:"the caller's values for the variables the project declares in [env]"`
}

// UpParams is project.up's request.
type UpParams struct {
	Dir  string            `json:"dir" desc:"a directory of the project (absolute)"`
	Vars map[string]string `json:"vars,omitempty" desc:"the caller's values for the variables the project declares in [env]"`
}

// Register exposes the service on the wire as project.*.
func Register(r *protocol.Router, s *Service) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }

	m("project.get", protocol.Method(func(ctx context.Context, p DirParams) (Project, error) {
		pr, err := s.Get(ctx, p.Dir)
		return pr, wireError(err)
	}))
	m("project.list", protocol.Method(func(ctx context.Context, _ struct{}) ([]Project, error) {
		ps, err := s.List(ctx)
		return ps, wireError(err)
	}))
	m("project.trust", protocol.Method(func(ctx context.Context, p TrustParams) (Project, error) {
		pr, err := s.Trust(ctx, p.Dir, p.Revoke)
		return pr, wireError(err)
	}))
	m("project.plan", protocol.Method(func(ctx context.Context, p PlanParams) (Plan, error) {
		plan, err := s.Plan(ctx, p.Dir, p.Down, p.Vars)
		return plan, wireError(err)
	}))
	m("project.up", protocol.Method(func(ctx context.Context, p UpParams) (Applied, error) {
		res, err := s.Up(ctx, p.Dir, p.Vars)
		return res, wireError(err)
	}))
	m("project.down", protocol.Method(func(ctx context.Context, p DirParams) (Applied, error) {
		res, err := s.Down(ctx, p.Dir)
		return res, wireError(err)
	}))
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, ErrUntrusted), errors.Is(err, ErrState):
		return protocol.NewError(protocol.ErrorCodeConflict, err)
	case errors.Is(err, ErrNewer):
		return protocol.NewError(protocol.ErrorCodeUnavailable, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	}
	return err
}
