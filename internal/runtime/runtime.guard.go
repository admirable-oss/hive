package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
)

// envGuard decorates the environment service with the rules that span
// domains: deleting an environment first stops the agents running in it, so
// nothing is left executing inside a deleted workspace, and changes are
// published as events. Embedding inherits Get and List unchanged.
//
// It lives here because the runtime is the only layer that knows every
// domain involved.
type envGuard struct {
	environment.Service
	procs interface {
		StopEnvironment(ctx context.Context, envID string) error
	}
	events event.Publisher
}

func (g envGuard) Create(ctx context.Context, id string) (environment.Environment, error) {
	env, err := g.Service.Create(ctx, id)
	if err == nil && g.events != nil {
		g.events.Publish(event.EnvironmentCreated, env)
	}
	return env, err
}

func (g envGuard) Delete(ctx context.Context, id string) error {
	if err := g.procs.StopEnvironment(ctx, id); err != nil {
		return err
	}
	if err := g.Service.Delete(ctx, id); err != nil {
		return err
	}
	if g.events != nil {
		g.events.Publish(event.EnvironmentRemoved, map[string]string{"id": id})
	}
	return nil
}
