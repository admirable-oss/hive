package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/workflow"
)

// Workflows run steps through the ports package workflow declares; this
// file fills them in: agent steps as tasks, commands as checks run, plugin
// actions through the host, and this machine's environments and
// repositories.

// WorkflowsConfig is the [workflows] policy, with [retention]'s bounds on
// runs. Zero values are the defaults.
type WorkflowsConfig struct {
	// Paths are shared workflow files and directories (absolute).
	Paths []string
	// MaxRuns caps runs running at once.
	MaxRuns int
	// Keep and KeepAge bound the finished runs kept.
	Keep    int
	KeepAge time.Duration
	// Interval is how often workflow files and watched repositories are
	// read again.
	Interval time.Duration
	// UserHome makes "~/" in triggers' repositories absolute.
	UserHome string
}

// newWorkflows builds the workflow service over this daemon's tasks,
// environments, agents and plugins.
func newWorkflows(cfg Config, tasks *task.Service, envs environment.Service, agents *agent.Service, plugins *plugin.Host, bus event.Publisher, log *slog.Logger) *workflow.Service {
	wc := cfg.Workflows
	own := cfg.Home
	if own == "" {
		own = cfg.root()
	}
	return workflow.NewModule(workflow.ModuleConfig{
		BaseDir: cfg.root(),
		Service: workflow.Config{
			Logger: log, OwnDir: workflow.Dir(own), Paths: wc.Paths, Home: wc.UserHome,
			MaxRuns: wc.MaxRuns, Keep: wc.Keep, KeepAge: wc.KeepAge, Interval: wc.Interval,
		},
	}, workflow.Ports{
		Tasks:     tasks,
		Commands:  workflowCommands{},
		Actions:   workflowActions{host: plugins},
		Workspace: &workflowWorkspace{envs: envs, git: git.New()},
		Agents:    workflowAgents{agents: agents},
		Events:    bus,
	}).Service
}

// feedWorkflows hands every event to the workflow service, whose event
// triggers match them, until the daemon stops; git changes also have its
// git triggers read the repositories again.
func feedWorkflows(ctx context.Context, svc *workflow.Service, bus *event.Bus) {
	sub := bus.Subscribe(event.DefaultBuffer)
	defer sub.Close()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		switch ev.Type {
		case event.Lost:
			svc.GitChanged() // a missed push is found by reading the references
			continue
		case event.EnvironmentGit, EventWorktreeCreated:
			svc.GitChanged()
		}
		svc.Notify(ctx, ev.Type, ev.Seq, ev.Data)
	}
}

// workflowCommands runs command steps as task checks and branch gates
// run: bounded output, everything stopped on the deadline.
type workflowCommands struct{}

func (workflowCommands) Run(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error) {
	return runCheck(ctx, dir, argv, env)
}

// workflowActions runs action steps as the plugins' own actions.
type workflowActions struct {
	host *plugin.Host
}

func (a workflowActions) Action(ctx context.Context, req workflow.ActionRequest) (workflow.ActionResult, error) {
	if a.host == nil {
		return workflow.ActionResult{}, errors.New("plugins do not run in this daemon")
	}
	res, err := a.host.Action(ctx, req.Plugin, req.Action, plugin.Context{
		Environment: req.Environment,
		Workflow: &plugin.WorkflowContext{
			Workflow: req.Workflow, Run: req.Run, Step: req.Step, With: req.With, Output: req.Output,
		},
	})
	return workflow.ActionResult{ExitCode: res.ExitCode, Output: res.Output}, err
}

// workflowAgents lists the agent kinds whose start command is on the
// PATH, in the manifests' order.
type workflowAgents struct {
	agents *agent.Service
}

func (a workflowAgents) Installed() []string {
	var out []string
	for _, m := range a.agents.Manifests() {
		if len(m.Start.Command) == 0 {
			continue
		}
		if _, err := exec.LookPath(m.Start.Command[0]); err == nil {
			out = append(out, m.ID)
		}
	}
	return out
}

// workflowWorkspace reads environments and repositories for workflows.
type workflowWorkspace struct {
	envs environment.Service
	git  *git.Git
}

func (w *workflowWorkspace) EnvironmentDir(ctx context.Context, id string) (string, error) {
	e, err := w.envs.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return e.Path, nil
}

// envIDChars are what an environment ID made from a directory's name may
// keep.
var envIDChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (w *workflowWorkspace) EnvironmentFor(ctx context.Context, dir string) (string, error) {
	dir = canonicalDir(dir)
	list, err := w.envs.List(ctx)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, e := range list {
		taken[e.ID] = true
		if e.Worktree == nil && canonicalDir(e.Path) == dir {
			return e.ID, nil
		}
	}
	base := strings.Trim(envIDChars.ReplaceAllString(filepath.Base(dir), "-"), "-.")
	if base == "" || !environment.ValidID(base) {
		base = "workflow"
	}
	if len(base) > 56 {
		base = base[:56]
	}
	id := base
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	e, err := w.envs.Create(ctx, environment.CreateRequest{ID: id, Root: dir})
	if err != nil {
		return "", fmt.Errorf("making an environment for %s: %w", dir, err)
	}
	return e.ID, nil
}

// canonicalDir is dir cleaned, with symbolic links resolved where they
// can be (macOS's /var is /private/var).
func canonicalDir(dir string) string {
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		return d
	}
	return filepath.Clean(dir)
}

func (w *workflowWorkspace) Repos(ctx context.Context) ([]string, error) {
	list, err := w.envs.List(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range list {
		root, err := w.RepoRoot(ctx, e.Path)
		if err != nil || root == "" || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out, nil
}

// RepoRoot is the main worktree of dir's repository: worktrees share its
// references, so pushes from any of them are seen there.
func (w *workflowWorkspace) RepoRoot(ctx context.Context, dir string) (string, error) {
	_, common, err := w.git.Dirs(ctx, dir)
	if errors.Is(err, git.ErrNotRepository) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if filepath.Base(common) == ".git" {
		return canonicalDir(filepath.Dir(common)), nil
	}
	return canonicalDir(common), nil // a bare repository
}

func (w *workflowWorkspace) Refs(ctx context.Context, repo string) (map[string]string, error) {
	return w.git.Refs(ctx, repo)
}
