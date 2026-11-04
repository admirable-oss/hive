package editor

import (
	"context"

	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/vt"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Hive is what the bridge needs from the daemon: a *hiveapi.Client, behind
// Connect, or a fake in tests.
type Hive interface {
	Status(ctx context.Context) (hiveapi.Status, error)
	Agents(ctx context.Context, all bool) ([]hiveapi.Agent, error)
	Agent(ctx context.Context, id string) (hiveapi.Agent, error)
	StartAgent(ctx context.Context, req hiveapi.StartAgent) (hiveapi.AgentStarted, error)
	Prompt(ctx context.Context, id, text string, opts hiveapi.PromptOptions) (hiveapi.PromptResult, error)
	SendKeys(ctx context.Context, id string, keys ...string) error
	StopAgent(ctx context.Context, id string) (hiveapi.Agent, error)
	Read(ctx context.Context, id string, opts hiveapi.ReadOptions) ([]string, error)
	Manifests(ctx context.Context) ([]hiveapi.Manifest, error)
	Environments(ctx context.Context) ([]hiveapi.Environment, error)
	CreateEnvironment(ctx context.Context, id, dir string, vars map[string]string) (hiveapi.Environment, error)
	Plans(ctx context.Context) ([]hiveapi.PlanSummary, error)
	Plan(ctx context.Context, id string) (hiveapi.Plan, error)
	ApproveTask(ctx context.Context, id, taskID string, opts hiveapi.ApproveOptions) (hiveapi.Plan, error)
	Branches(ctx context.Context, repo string, all bool) ([]hiveapi.Branch, error)
	Branch(ctx context.Context, ref string) (hiveapi.Branch, error)
	BranchDiff(ctx context.Context, req hiveapi.DiffRequest) (hiveapi.Diff, error)
	CommentBranch(ctx context.Context, req hiveapi.CommentRequest) (hiveapi.Branch, error)
	SendComments(ctx context.Context, ref string) (hiveapi.Branch, error)
	ReadyBranch(ctx context.Context, ref string, gates []hiveapi.Gate) (hiveapi.Branch, error)
	ReviewHunk(ctx context.Context, req hiveapi.HunkRequest) (hiveapi.HunkResult, error)
	AgentFiles(ctx context.Context, req hiveapi.AgentFilesRequest) (hiveapi.AgentFiles, error)
	// Terminal opens a view of a terminal; Events subscribes to events.
	Terminal(ctx context.Context, processID string, cols, rows int) (Frames, error)
	Subscribe(ctx context.Context, types ...string) (Events, error)
}

// Frames is an open view of a terminal.
type Frames interface {
	Next() (*vt.Frame, error)
	Write(p []byte) (int, error)
	Resize(ctx context.Context, cols, rows uint16) error
	Close() error
}

// Events is a subscription to the daemon's events.
type Events interface {
	Next() (event.Event, error)
	Close() error
}

// FromClient adapts a hiveapi client.
func FromClient(c *hiveapi.Client) Hive { return clientHive{c} }

type clientHive struct{ *hiveapi.Client }

func (c clientHive) Terminal(ctx context.Context, processID string, cols, rows int) (Frames, error) {
	return c.Frames(ctx, processID, cols, rows)
}

func (c clientHive) Subscribe(ctx context.Context, types ...string) (Events, error) {
	return c.Events(ctx, types...)
}
