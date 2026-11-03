package hiveapi

import (
	"context"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/project"
)

// Projects: a repository's Hive setup in its hive.toml, brought up and
// down as a whole (`hive up`, `hive down`, `hive project`). Trusting a
// project file, and rewriting one, is left to a person: `hive project
// trust` and `hive project save`. Directories
// are absolute, on the daemon's machine; the project file is found there
// or above it.
//
//	plan, err := c.ProjectPlan(ctx, "/src/shop", false, nil)
//	res, err := c.ProjectUp(ctx, "/src/shop", map[string]string{"DATABASE_URL": url})
type (
	Project            = project.Project
	ProjectSpec        = project.Spec
	ProjectEnvironment = project.Environment
	ProjectTab         = project.Tab
	ProjectPane        = project.Pane
	ProjectAgent       = project.Agent
	ProjectPlan        = project.Plan
	ProjectAction      = project.Action
	ProjectApplied     = project.Applied
	ProjectGate        = project.Gate
	ProjectToolchain   = project.Toolchain
	ProjectDiff        = project.Diff
	ProjectChange      = project.Change
)

// The steps of project plans.
const (
	ProjectCreateEnvironment = project.OpCreateEnvironment
	ProjectSetVars           = project.OpSetVars
	ProjectCreateTab         = project.OpCreateTab
	ProjectStartAgent        = project.OpStartAgent
	ProjectRestartAgent      = project.OpRestartAgent
	ProjectStopAgent         = project.OpStopAgent
	ProjectCloseTab          = project.OpCloseTab
)

// ParseProject reads a project file and the hive.local.toml beside it
// (nil for none), as the daemon does; root is their directory.
func ParseProject(data, local []byte, root string) (ProjectSpec, error) {
	spec, _, err := project.Parse(data, local, root)
	return spec, err
}

func (c *Client) projects() client.Projects { return client.NewProjects(c.c) }

// Project reads the project at dir, and says whether its text is trusted.
func (c *Client) Project(ctx context.Context, dir string) (Project, error) {
	return c.projects().Get(ctx, dir)
}

// Projects lists the trusted projects.
func (c *Client) Projects(ctx context.Context) ([]Project, error) { return c.projects().List(ctx) }

// ProjectPlan says what ProjectUp (or, with down, ProjectDown) would do.
// vars are values for the variables the project declares.
func (c *Client) ProjectPlan(ctx context.Context, dir string, down bool, vars map[string]string) (ProjectPlan, error) {
	return c.projects().Plan(ctx, dir, down, vars)
}

// ProjectUp makes the workspace match a trusted project file.
func (c *Client) ProjectUp(ctx context.Context, dir string, vars map[string]string) (ProjectApplied, error) {
	return c.projects().Up(ctx, dir, vars)
}

// DiffProject says how the running workspace differs from the project
// file: what is missing, extra or changed.
func (c *Client) DiffProject(ctx context.Context, dir string, vars map[string]string) (ProjectDiff, error) {
	return c.projects().Diff(ctx, dir, vars)
}

// ProjectDown stops the agents and closes the tabs a project file declares.
func (c *Client) ProjectDown(ctx context.Context, dir string) (ProjectApplied, error) {
	return c.projects().Down(ctx, dir)
}
