package workflow_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/workflow"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fakeTasks runs agent steps' plans in memory: each task succeeds unless
// its prompt says TASK-FAILS, after a short while, or never while it says
// HANG.
type fakeTasks struct {
	mu      sync.Mutex
	plans   map[string]*task.Plan
	done    map[string]chan struct{}
	seq     int
	prompts []string
	// finish decides how a task ends; nil uses the prompt's words.
	finish func(t task.Spec) (task.State, *task.Result)
	// hold keeps tasks running until released.
	hold chan struct{}
}

func newFakeTasks() *fakeTasks {
	return &fakeTasks{plans: map[string]*task.Plan{}, done: map[string]chan struct{}{}}
}

func (f *fakeTasks) Submit(_ context.Context, spec task.PlanSpec) (task.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("p%04d", f.seq)
	t := spec.Tasks[0]
	f.prompts = append(f.prompts, t.Kind+": "+t.Prompt)
	p := &task.Plan{ID: id, Name: spec.Name, State: task.PlanRunning, Tasks: []task.Task{{Spec: t, State: task.StateRunning}}}
	f.plans[id] = p
	done := make(chan struct{})
	f.done[id] = done
	hold := f.hold
	go func() {
		if strings.Contains(t.Prompt, "HANG") {
			return
		}
		if hold != nil {
			<-hold
		}
		time.Sleep(5 * time.Millisecond)
		f.mu.Lock()
		defer f.mu.Unlock()
		if p.State.Finished() {
			return
		}
		state, res := task.StateSucceeded, &task.Result{Outcome: "met", Summary: "Did: " + t.Prompt, Changed: []string{"a.go"}}
		if strings.Contains(t.Prompt, "TASK-FAILS") {
			state, res = task.StateFailed, &task.Result{Outcome: "failed", Screen: []string{"it broke"}}
		}
		if f.finish != nil {
			state, res = f.finish(t)
		}
		f.endLocked(id, state, res)
	}()
	return *p, nil
}

func (f *fakeTasks) endLocked(id string, state task.State, res *task.Result) {
	p := f.plans[id]
	p.Tasks[0].State, p.Tasks[0].Result = state, res
	p.State = task.PlanSucceeded
	if state != task.StateSucceeded {
		p.State = task.PlanFailed
	}
	if state == task.StateCancelled {
		p.State = task.PlanCancelled
	}
	close(f.done[id])
}

func (f *fakeTasks) Wait(ctx context.Context, req task.WaitRequest) (task.WaitResult, error) {
	f.mu.Lock()
	p, done := f.plans[req.ID], f.done[req.ID]
	f.mu.Unlock()
	if p == nil {
		return task.WaitResult{}, fmt.Errorf("%w: no plan %q", task.ErrNotFound, req.ID)
	}
	var expired <-chan time.Time
	if req.Timeout > 0 {
		expired = time.After(req.Timeout)
	}
	select {
	case <-done:
	case <-ctx.Done():
		return task.WaitResult{}, ctx.Err()
	case <-expired:
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return task.WaitResult{Outcome: task.OutcomeFinished, Plan: *p}, nil
}

func (f *fakeTasks) Cancel(_ context.Context, id, _ string) (task.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.plans[id]
	if p == nil {
		return task.Plan{}, task.ErrNotFound
	}
	if p.State.Finished() {
		return *p, task.ErrState
	}
	f.endLocked(id, task.StateCancelled, &task.Result{Outcome: "cancelled"})
	return *p, nil
}

// adopt makes a plan as a previous daemon's task service would have it.
func (f *fakeTasks) adopt(id string, spec task.Spec) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plans[id] = &task.Plan{ID: id, State: task.PlanRunning, Tasks: []task.Task{{Spec: spec, State: task.StateRunning}}}
	f.done[id] = make(chan struct{})
	return f.done[id]
}

func (f *fakeTasks) submitted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.prompts)
}

// fakeCommands runs commands in Go, so the tests run anywhere:
//
//	ok                 exits 0
//	fail [code]        exits 1 (or code)
//	echo words…        prints the words
//	out k=v…           writes outputs to $HIVE_OUTPUT
//	sleep              runs until stopped
//	flaky name n       fails the first n times it runs under name
//	count name         exits 0, counting its runs under name
type fakeCommands struct {
	mu   sync.Mutex
	runs map[string]int
	log  []string
}

func newFakeCommands() *fakeCommands { return &fakeCommands{runs: map[string]int{}} }

func (f *fakeCommands) Run(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error) {
	f.mu.Lock()
	f.log = append(f.log, dir+": "+strings.Join(argv, " "))
	f.mu.Unlock()
	if len(argv) >= 3 && argv[0] == "sh" && argv[1] == "-c" {
		argv = strings.Fields(argv[2])
	}
	if len(argv) == 0 {
		return -1, nil, errors.New("no command")
	}
	switch argv[0] {
	case "ok":
		return 0, nil, nil
	case "fail":
		code := 1
		if len(argv) > 1 {
			code, _ = strconv.Atoi(argv[1])
		}
		return code, []string{"FAIL: TestSomething"}, nil
	case "echo":
		return 0, []string{strings.Join(argv[1:], " ")}, nil
	case "out":
		var b strings.Builder
		for _, kv := range argv[1:] {
			b.WriteString(kv + "\n")
		}
		if err := os.WriteFile(env[workflow.EnvOutput], []byte(b.String()), 0o600); err != nil {
			return -1, nil, err
		}
		return 0, nil, nil
	case "sleep":
		<-ctx.Done()
		return -1, nil, ctx.Err()
	case "flaky":
		n, _ := strconv.Atoi(argv[2])
		f.mu.Lock()
		f.runs[argv[1]]++
		seen := f.runs[argv[1]]
		f.mu.Unlock()
		if seen <= n {
			return 1, []string{"flaked"}, nil
		}
		return 0, nil, nil
	case "count":
		f.mu.Lock()
		f.runs[argv[1]]++
		f.mu.Unlock()
		return 0, nil, nil
	}
	return 127, nil, fmt.Errorf("fake command %q", argv[0])
}

func (f *fakeCommands) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[name]
}

func (f *fakeCommands) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

type fakeActions struct {
	mu   sync.Mutex
	reqs []workflow.ActionRequest
}

func (f *fakeActions) Action(_ context.Context, req workflow.ActionRequest) (workflow.ActionResult, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if req.Plugin == "missing" {
		return workflow.ActionResult{}, errors.New("no plugin missing")
	}
	_ = os.WriteFile(req.Output, []byte("posted="+req.With["text"]+"\n"), 0o600)
	return workflow.ActionResult{ExitCode: 0, Output: "posted\n"}, nil
}

// fakeWorkspace has environments by directory and repositories' refs.
type fakeWorkspace struct {
	mu    sync.Mutex
	envs  map[string]string // ID → directory
	refs  map[string]map[string]string
	roots map[string]string // directory → repository root
}

func newFakeWorkspace() *fakeWorkspace {
	return &fakeWorkspace{envs: map[string]string{"api": "/src/api"}, refs: map[string]map[string]string{}, roots: map[string]string{}}
}

func (w *fakeWorkspace) EnvironmentDir(_ context.Context, id string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	d, ok := w.envs[id]
	if !ok {
		return "", fmt.Errorf("no environment %q", id)
	}
	return d, nil
}

func (w *fakeWorkspace) EnvironmentFor(_ context.Context, dir string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, d := range w.envs {
		if d == dir {
			return id, nil
		}
	}
	id := filepath.Base(dir)
	w.envs[id] = dir
	return id, nil
}

func (w *fakeWorkspace) Repos(context.Context) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for r := range w.refs {
		out = append(out, r)
	}
	slices.Sort(out)
	return out, nil
}

func (w *fakeWorkspace) RepoRoot(_ context.Context, dir string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r, ok := w.roots[dir]; ok {
		return r, nil
	}
	if _, ok := w.refs[dir]; ok {
		return dir, nil
	}
	return "", nil
}

func (w *fakeWorkspace) Refs(_ context.Context, repo string) (map[string]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	refs, ok := w.refs[repo]
	if !ok {
		return nil, fmt.Errorf("not a repository: %s", repo)
	}
	out := map[string]string{}
	for k, v := range refs {
		out[k] = v
	}
	return out, nil
}

func (w *fakeWorkspace) setRef(repo, ref, commit string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refs[repo] == nil {
		w.refs[repo] = map[string]string{}
	}
	w.refs[repo][ref] = commit
}

type fakeAgents []string

func (a fakeAgents) Installed() []string { return a }

// recorder keeps the events published.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) Publish(typ string, _ any) {
	r.mu.Lock()
	r.events = append(r.events, typ)
	r.mu.Unlock()
}

func (r *recorder) count(typ string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == typ {
			n++
		}
	}
	return n
}

// env is a running service over fakes, in a temporary directory.
type env struct {
	t      *testing.T
	dir    string // the service's state
	own    string // the person's workflows
	shared string // a shared workflows directory
	svc    *workflow.Service
	tasks  *fakeTasks
	cmds   *fakeCommands
	acts   *fakeActions
	ws     *fakeWorkspace
	events *recorder
	agents fakeAgents
	cfg    workflow.Config
	stop   func()
}

func newEnv(t *testing.T, cfg workflow.Config) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{
		t: t, dir: filepath.Join(root, "state"), own: filepath.Join(root, "own"), shared: filepath.Join(root, "repo", ".hive", "workflows"),
		tasks: newFakeTasks(), cmds: newFakeCommands(), acts: &fakeActions{}, ws: newFakeWorkspace(),
		events: &recorder{}, agents: fakeAgents{"codex", "claude"},
	}
	for _, d := range []string{e.own, e.shared} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if cfg.Interval == 0 {
		cfg.Interval = 50 * time.Millisecond
	}
	cfg.OwnDir = e.own
	if cfg.Paths == nil {
		cfg.Paths = []string{e.shared}
	}
	e.cfg = cfg
	e.start()
	return e
}

// start builds the service over the state directory and runs it.
func (e *env) start() {
	e.t.Helper()
	e.svc = workflow.NewService(e.cfg, workflow.NewFilesystemStore(e.dir), workflow.Ports{
		Tasks: e.tasks, Commands: e.cmds, Actions: e.acts, Workspace: e.ws, Agents: &e.agents, Events: e.events,
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.svc.Load(ctx); err != nil {
		e.t.Logf("load: %v", err)
	}
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	stopped := false
	e.stop = func() {
		if !stopped {
			stopped = true
			cancel()
			<-done
		}
	}
	e.t.Cleanup(e.stop)
}

// restart stops the service and starts another over the same state.
func (e *env) restart() {
	e.t.Helper()
	e.stop()
	e.start()
}

func (e *env) write(dir, name, text string) string {
	e.t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.svc.Reload(context.Background())
	return path
}

// run runs workflow text and waits for it to finish.
func (e *env) run(text string, inputs map[string]string) workflow.Run {
	e.t.Helper()
	ctx := context.Background()
	r, err := e.svc.Submit(ctx, workflow.SubmitRequest{TOML: text, Source: "test.toml", Inputs: inputs, Dir: "/src/api"})
	if err != nil {
		e.t.Fatal(err)
	}
	return e.wait(r.ID)
}

func (e *env) wait(id string) workflow.Run {
	e.t.Helper()
	res, err := e.svc.Wait(context.Background(), id, 10*time.Second)
	if err != nil {
		e.t.Fatal(err)
	}
	if res.Outcome != workflow.WaitFinished {
		e.t.Fatalf("run %s did not finish: %+v", id, res.Run)
	}
	return res.Run
}

// waitFor polls cond.
func waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", desc)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func states(r workflow.Run) string {
	var parts []string
	for _, st := range r.Steps {
		parts = append(parts, st.ID+"="+string(st.State))
	}
	return strings.Join(parts, " ")
}
