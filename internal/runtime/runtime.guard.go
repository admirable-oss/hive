package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/environment"
)

// envGuard decorates the environment service so that deleting an environment
// first stops the agents running in it; nothing is left executing inside a
// deleted workspace. Embedding inherits Create, Get and List unchanged.
//
// It lives here because it is the one rule that spans two domains, and the
// runtime is the only layer that knows both.
type envGuard struct {
	environment.Service
	procs interface {
		StopEnvironment(ctx context.Context, envID string) error
	}
}

func (g envGuard) Delete(ctx context.Context, id string) error {
	if err := g.procs.StopEnvironment(ctx, id); err != nil {
		return err
	}
	return g.Service.Delete(ctx, id)
}
