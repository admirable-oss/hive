package edits_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/edits"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/git"
)

type fakeAgents struct {
	mu   sync.Mutex
	list []agent.Agent
}

func (f *fakeAgents) List(context.Context, bool) ([]agent.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Agent(nil), f.list...), nil
}

func (f *fakeAgents) set(id string, state agent.State) agent.Agent {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].State, f.list[i].Since = state, time.Now()
			return f.list[i]
		}
	}
	a := agent.Agent{ID: id, Kind: "claude", Name: "agent " + id, EnvironmentID: "env", PaneID: "p" + id}
	a.State, a.Since = state, time.Now()
	f.list = append(f.list, a)
	return a
}

func (f *fakeAgents) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == id {
			f.list = append(f.list[:i], f.list[i+1:]...)
			return
		}
	}
}

type fakeEnvs map[string]string

func (f fakeEnvs) Get(_ context.Context, id string) (environment.Environment, error) {
	if p, ok := f[id]; ok {
		return environment.Environment{ID: id, Path: p}, nil
	}
	return environment.Environment{}, errors.New("no such environment")
}

type recorder struct {
	mu     sync.Mutex
	events []edits.Files
}

func (r *recorder) Publish(typ string, data any) {
	if typ != edits.EventFiles {
		return
	}
	raw, _ := json.Marshal(data)
	var f edits.Files
	_ = json.Unmarshal(raw, &f)
	r.mu.Lock()
	r.events = append(r.events, f)
	r.mu.Unlock()
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "hive@example.com")
	gitRun(t, dir, "config", "user.name", "Hive Test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(dir, "a.go"), "package a\n")
	write(t, filepath.Join(dir, "b.go"), "package b\n")
	write(t, filepath.Join(dir, "mine.go"), "package mine\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// touch writes a file and moves its modification time on, so a change
// within the same clock tick is still seen.
func touch(t *testing.T, path, content string, at time.Time) {
	t.Helper()
	write(t, path, content)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestFilesAreTheAgentsWhoChangeThem(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	agents := &fakeAgents{}
	events := &recorder{}
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	svc := edits.NewService(edits.Config{Now: func() time.Time { return clock }}, agents, fakeEnvs{"env": dir}, git.New(), events)

	// The person's own change, made before any agent works, is nobody's.
	touch(t, filepath.Join(dir, "mine.go"), "package mine // edited\n", time.Now().Add(-time.Hour))
	svc.Changed(agents.set("1", agent.StateIdle))
	svc.Tick(ctx)

	// Agent 1 starts working: the tree is read first, so mine.go stays
	// the person's.
	a := agents.set("1", agent.StateWorking)
	svc.Tick(ctx)
	clock = clock.Add(time.Second)
	touch(t, filepath.Join(dir, "a.go"), "package a // by the agent\n", clock)
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "new.go"), "package a\n")
	svc.Tick(ctx)

	got := svc.List(ctx, edits.ListRequest{})
	paths := map[string]edits.Edit{}
	for _, e := range got.Files {
		paths[e.Rel] = e
	}
	if len(paths) != 3 || paths["mine.go"].Agent != "" {
		t.Fatalf("a.go, b.go and new.go are the agent's, mine.go is not: %+v", got.Files)
	}
	e := paths["a.go"]
	if e.Agent != "1" || e.Kind != "claude" || e.PaneID != "p1" || e.State != "working" || e.Path != filepath.Join(dir, "a.go") || e.Worktree != dir || !e.Since.Equal(clock) {
		t.Fatalf("a.go's edit: %+v", e)
	}
	if !paths["b.go"].Deleted {
		t.Fatalf("b.go was deleted: %+v", paths["b.go"])
	}
	if events.count() == 0 {
		t.Fatal("changes are published as agent.files")
	}

	// A second agent in the same tree shares what changes while both work.
	agents.set("2", agent.StateWorking)
	svc.Tick(ctx)
	clock = clock.Add(time.Second)
	touch(t, filepath.Join(dir, "a.go"), "package a // by both\n", clock)
	svc.Tick(ctx)
	both := 0
	for _, e := range svc.List(ctx, edits.ListRequest{Dir: dir}).Files {
		if e.Rel == "a.go" {
			both++
		}
	}
	if both != 2 {
		t.Fatalf("both agents change a.go: %+v", svc.List(ctx, edits.ListRequest{}).Files)
	}

	// Done: the edit stays the agent's, with its state.
	svc.Changed(agents.set("2", agent.StateDone))
	agents.set("1", agent.StateIdle)
	svc.Tick(ctx)
	for _, e := range svc.List(ctx, edits.ListRequest{EnvironmentID: "env"}).Files {
		if e.Agent == "1" && e.State != "idle" {
			t.Fatalf("an edit carries its agent's state: %+v", e)
		}
	}

	// A file back to HEAD is nobody's, once a tree is read again.
	agents.set("1", agent.StateWorking)
	svc.Tick(ctx)
	gitRun(t, dir, "checkout", "--", "a.go")
	svc.Tick(ctx)
	for _, e := range svc.List(ctx, edits.ListRequest{}).Files {
		if e.Rel == "a.go" {
			t.Fatalf("a.go is clean again: %+v", e)
		}
	}

	// An agent that exits changes nothing any more.
	agents.remove("1")
	agents.remove("2")
	svc.Tick(ctx)
	if left := svc.List(ctx, edits.ListRequest{}).Files; len(left) != 0 {
		t.Fatalf("no edits after the agents exit: %+v", left)
	}
	if outside := svc.List(ctx, edits.ListRequest{Dir: t.TempDir()}).Files; len(outside) != 0 {
		t.Fatalf("a directory elsewhere has none: %+v", outside)
	}
	_ = a
}

func TestRunReadsATreeWhenAnAgentStarts(t *testing.T) {
	dir := repo(t)
	agents := &fakeAgents{}
	svc := edits.NewService(edits.Config{Interval: 20 * time.Millisecond}, agents, fakeEnvs{"env": dir}, git.New(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	svc.Changed(agents.set("1", agent.StateWorking))
	write(t, filepath.Join(dir, "a.go"), "package a // changed\n")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f := svc.List(ctx, edits.ListRequest{}).Files; len(f) == 1 && f[0].Rel == "a.go" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a.go is the working agent's: %+v", svc.List(ctx, edits.ListRequest{}).Files)
}
