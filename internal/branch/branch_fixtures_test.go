package branch_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/git"
)

func TestMain(m *testing.M) {
	// git must not read the developer's own configuration (signing,
	// hooks, default branch names): every repository here sets its own.
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	goleak.VerifyTestMain(m)
}

// repo creates a repository with one commit on main and returns its path.
func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "app")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.email", "hive@example.com")
	run(t, dir, "config", "user.name", "Hive Test")
	run(t, dir, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(dir, "README.md"), "hello\n")
	write(t, filepath.Join(dir, "shared.txt"), "one\ntwo\nthree\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// env is a service over a repository, with fakes for the rest of the
// daemon.
type env struct {
	t      *testing.T
	repo   string
	svc    *branch.Service
	store  *branch.FilesystemStore
	ws     *fakeWorkspace
	agents *fakeAgents
	events *recorder
	forge  *fakeForge
	git    *git.Git
	now    time.Time
}

func newEnv(t *testing.T, cfg branch.Config) *env {
	t.Helper()
	e := &env{t: t, repo: repo(t), git: git.New(), now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	e.ws = &fakeWorkspace{git: e.git, repo: e.repo, busy: map[string]bool{}}
	e.agents = &fakeAgents{byEnv: map[string][]branch.AgentInfo{}}
	e.events = &recorder{}
	e.forge = &fakeForge{}
	forges := branch.DefaultForges(func(context.Context, string, []string, []byte) ([]byte, error) {
		return nil, errors.New("no tools in tests")
	})
	forges.Register(e.forge)
	forges.Route("github.com", "fake")
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return e.now }
	}
	e.store = branch.NewFilesystemStore(t.TempDir())
	e.svc = branch.NewService(cfg, e.store, branch.Ports{
		Git: e.git, Workspace: e.ws, Agents: e.agents, Commands: commands{}, Events: e.events, Forges: forges,
		Issues: fakeIssues{"p1/fix": "https://linear.app/acme/issue/HIV-42"},
	})
	e.ws.svc = e.svc
	return e
}

// fakeIssues names the issues of plan/task.
type fakeIssues map[string]string

func (f fakeIssues) For(_ context.Context, plan, task string) string { return f[plan+"/"+task] }

// worktree makes a worktree on a new branch and tracks it, as the
// runtime does for an agent.
func (e *env) worktree(id, name string, owner *branch.Owner) branch.Branch {
	e.t.Helper()
	path := filepath.Join(filepath.Dir(e.repo), "worktrees", git.Slug(name))
	if err := e.git.AddWorktree(context.Background(), e.repo, path, name, ""); err != nil {
		e.t.Fatal(err)
	}
	b, err := e.svc.Track(context.Background(), branch.TrackRequest{EnvironmentID: id, Path: path, Repo: e.repo, Name: name, Owner: owner})
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) refresh() {
	e.t.Helper()
	if err := e.svc.Refresh(context.Background(), e.repo); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) get(ref string) branch.Branch {
	e.t.Helper()
	b, err := e.svc.Get(context.Background(), ref)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

// commit commits everything in dir.
func (e *env) commit(dir, msg string) {
	e.t.Helper()
	run(e.t, dir, "add", "-A")
	run(e.t, dir, "commit", "-q", "-m", msg)
}

type fakeWorkspace struct {
	git  *git.Git
	repo string
	svc  *branch.Service

	mu      sync.Mutex
	busy    map[string]bool
	removed []string
}

func (w *fakeWorkspace) RemoveWorktree(ctx context.Context, path string, force bool) error {
	if err := w.git.RemoveWorktree(ctx, w.repo, path, force); err != nil {
		return err
	}
	w.mu.Lock()
	w.removed = append(w.removed, path)
	w.mu.Unlock()
	w.svc.WorktreeRemoved(path)
	return nil
}

func (w *fakeWorkspace) Busy(_ context.Context, path string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.busy[path], nil
}

func (w *fakeWorkspace) setBusy(path string, busy bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.busy[path] = busy
}

type fakeAgents struct {
	mu      sync.Mutex
	byEnv   map[string][]branch.AgentInfo
	prompts []string // "agent: text"
}

func (a *fakeAgents) Get(_ context.Context, id string) (branch.AgentInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, list := range a.byEnv {
		for _, x := range list {
			if x.ID == id {
				return x, nil
			}
		}
	}
	return branch.AgentInfo{}, errors.New("no agent")
}

func (a *fakeAgents) In(_ context.Context, env string) ([]branch.AgentInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]branch.AgentInfo(nil), a.byEnv[env]...), nil
}

func (a *fakeAgents) Prompt(_ context.Context, id, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prompts = append(a.prompts, id+": "+text)
	return nil
}

func (a *fakeAgents) add(info branch.AgentInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.byEnv[info.EnvironmentID] = append(a.byEnv[info.EnvironmentID], info)
}

// commands runs gates as real processes.
type commands struct{}

func (commands) Run(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if ctx.Err() != nil {
		return -1, lines, ctx.Err()
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		err = nil
	}
	if err != nil {
		return -1, lines, err
	}
	return cmd.ProcessState.ExitCode(), lines, nil
}

type recorder struct {
	mu     sync.Mutex
	events []string // "type data"
}

func (r *recorder) Publish(typ string, data any) {
	raw, _ := json.Marshal(data)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, typ+" "+string(raw))
}

func (r *recorder) of(typ string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if strings.HasPrefix(e, typ+" ") {
			out = append(out, strings.TrimPrefix(e, typ+" "))
		}
	}
	return out
}

type fakeForge struct {
	mu       sync.Mutex
	requests []branch.ForgeRequest
}

func (*fakeForge) Name() string { return "fake" }

func (f *fakeForge) Open(_ context.Context, req branch.ForgeRequest) (branch.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return branch.PullRequest{Forge: "fake", URL: "https://github.com/acme/app/pull/7", Number: 7, Draft: req.Draft}, nil
}
