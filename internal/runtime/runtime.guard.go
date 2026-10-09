package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
)

// envGuard decorates the environment service with the rules that span
// domains: deleting an environment first stops the agents running in it, so
// nothing is left executing inside a deleted workspace, and drops its tabs
// and panes; environments report their git status; and changes are
// published as events.
//
// It lives here because the runtime is the only layer that knows every
// domain involved.
type envGuard struct {
	environment.Service
	procs interface {
		StopEnvironment(ctx context.Context, envID string) error
	}
	events event.Publisher
	// git and panes are optional.
	git   *gitTracker
	panes interface{ ForgetEnvironment(envID string) }
}

func (g *envGuard) Create(ctx context.Context, req environment.CreateRequest) (environment.Environment, error) {
	env, err := g.Service.Create(ctx, req)
	if err != nil {
		return env, err
	}
	if g.git != nil {
		g.git.watch(ctx, env.Path)
		env.Git = g.git.status(env.Path)
	}
	if g.events != nil {
		g.events.Publish(event.EnvironmentCreated, env)
	}
	return env, nil
}

func (g *envGuard) Get(ctx context.Context, id string) (environment.Environment, error) {
	env, err := g.Service.Get(ctx, id)
	if err == nil && g.git != nil {
		env.Git = g.git.status(env.Path)
	}
	return env, err
}

func (g *envGuard) List(ctx context.Context) ([]environment.Environment, error) {
	envs, err := g.Service.List(ctx)
	if err == nil && g.git != nil {
		for i := range envs {
			envs[i].Git = g.git.status(envs[i].Path)
		}
	}
	return envs, err
}

func (g *envGuard) Update(ctx context.Context, req environment.UpdateRequest) (environment.Environment, error) {
	env, err := g.Service.Update(ctx, req)
	if err != nil {
		return env, err
	}
	if g.git != nil {
		env.Git = g.git.status(env.Path)
	}
	if g.events != nil {
		g.events.Publish(event.EnvironmentUpdated, env)
	}
	return env, nil
}

func (g *envGuard) Delete(ctx context.Context, id string) error {
	var dir string
	if g.git != nil {
		env, err := g.Service.Get(ctx, id)
		if err != nil {
			return err
		}
		dir = env.Path
	}
	if err := g.procs.StopEnvironment(ctx, id); err != nil {
		return err
	}
	if err := g.Service.Delete(ctx, id); err != nil {
		return err
	}
	if g.panes != nil {
		g.panes.ForgetEnvironment(id)
	}
	if g.git != nil {
		g.git.unwatch(dir)
	}
	if g.events != nil {
		g.events.Publish(event.EnvironmentRemoved, map[string]string{"id": id})
	}
	return nil
}
