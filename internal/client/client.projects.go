package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/project"
)

// Projects is the typed project API (project.*) over any Client.
type Projects struct {
	c Client
}

// NewProjects wraps c.
func NewProjects(c Client) Projects { return Projects{c: c} }

func projectCall[R any](ctx context.Context, p Projects, method string, params any) (R, error) {
	var r R
	err := p.c.Call(ctx, method, params, &r)
	return r, err
}

// Get reads the project at dir (or above it), and says whether it is
// trusted.
func (p Projects) Get(ctx context.Context, dir string) (project.Project, error) {
	return projectCall[project.Project](ctx, p, "project.get", project.DirParams{Dir: dir})
}

// List returns the trusted projects.
func (p Projects) List(ctx context.Context) ([]project.Project, error) {
	return projectCall[[]project.Project](ctx, p, "project.list", struct{}{})
}

// Trust trusts the project at dir at its current text, or revokes it.
func (p Projects) Trust(ctx context.Context, dir string, revoke bool) (project.Project, error) {
	return projectCall[project.Project](ctx, p, "project.trust", project.TrustParams{Dir: dir, Revoke: revoke})
}

// Plan says what Up (or, with down, Down) would do.
func (p Projects) Plan(ctx context.Context, dir string, down bool, vars map[string]string) (project.Plan, error) {
	return projectCall[project.Plan](ctx, p, "project.plan", project.PlanParams{Dir: dir, Down: down, Vars: vars})
}

// Up makes the workspace match the project file.
func (p Projects) Up(ctx context.Context, dir string, vars map[string]string) (project.Applied, error) {
	return projectCall[project.Applied](ctx, p, "project.up", project.UpParams{Dir: dir, Vars: vars})
}

// Down stops the project's agents and closes its tabs.
func (p Projects) Down(ctx context.Context, dir string) (project.Applied, error) {
	return projectCall[project.Applied](ctx, p, "project.down", project.DirParams{Dir: dir})
}
