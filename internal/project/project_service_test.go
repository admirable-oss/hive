package project_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/project"
)

// fakeWorkspace is a workspace in memory: what State reports is what the
// changes made.
type fakeWorkspace struct {
	st    project.State
	calls []string
	fail  string // a call that fails, by its first word and name
	// envs and wraps are what new panes got, in order.
	envs  []map[string]string
	wraps [][]string
}

func newFake() *fakeWorkspace {
	return &fakeWorkspace{st: project.State{
		Tabs: map[string][]project.TabState{}, Known: []string{"claude", "codex"}, Installed: []string{"codex"},
	}}
}

func (w *fakeWorkspace) State(context.Context) (project.State, error) { return w.st, nil }

func (w *fakeWorkspace) record(call string) error {
	w.calls = append(w.calls, call)
	if w.fail != "" && strings.HasPrefix(call, w.fail) {
		return errors.New("it failed")
	}
	return nil
}

func (w *fakeWorkspace) CreateEnvironment(_ context.Context, id, root string, vars map[string]string) error {
	if err := w.record("env " + id); err != nil {
		return err
	}
	w.st.Environments = append(w.st.Environments, project.EnvState{ID: id, Root: root, Vars: vars})
	return nil
}

func (w *fakeWorkspace) SetVars(_ context.Context, id string, vars map[string]string) error {
	if err := w.record(fmt.Sprintf("vars %s %v", id, vars)); err != nil {
		return err
	}
	for i := range w.st.Environments {
		if w.st.Environments[i].ID == id {
			if w.st.Environments[i].Vars == nil {
				w.st.Environments[i].Vars = map[string]string{}
			}
			for k, v := range vars {
				w.st.Environments[i].Vars[k] = v
			}
		}
	}
	return nil
}

func (w *fakeWorkspace) CreateTab(_ context.Context, envID string, t project.Tab, env map[string]string, wrap []string) error {
	w.envs = append(w.envs, env)
	w.wraps = append(w.wraps, wrap)
	if err := w.record("tab " + t.Name); err != nil {
		return err
	}
	w.st.Tabs[envID] = append(w.st.Tabs[envID], project.TabState{ID: "t-" + t.Name, Name: t.Name})
	return nil
}

func (w *fakeWorkspace) StartAgent(_ context.Context, envID string, a project.Agent, kind, in string, vars map[string]string) error {
	w.envs = append(w.envs, vars)
	if err := w.record(fmt.Sprintf("agent %s %s in=%s", a.Name, kind, in)); err != nil {
		return err
	}
	env := envID
	switch {
	case in != "":
		env = in
	case a.Isolate:
		env = envID + "-wt-" + a.Name
		repo := ""
		for _, e := range w.st.Environments {
			if e.ID == envID {
				repo = e.Root
			}
		}
		w.st.Environments = append(w.st.Environments, project.EnvState{
			ID: env, Root: "/worktrees/" + env, WorktreeRepo: repo, Branch: "hive/" + a.Name,
			Vars: map[string]string{project.EnvProjectAgent: a.Name},
		})
	}
	w.st.Agents = append(w.st.Agents, project.AgentState{ID: "a-" + a.Name, Name: a.Name, Kind: kind, EnvironmentID: env, TabID: "t-" + a.Name})
	w.st.Tabs[env] = append(w.st.Tabs[env], project.TabState{ID: "t-" + a.Name, Name: a.Name})
	return nil
}

func (w *fakeWorkspace) RestartAgent(_ context.Context, id string, a project.Agent, env map[string]string) error {
	w.envs = append(w.envs, env)
	if err := w.record("restart " + a.Name); err != nil {
		return err
	}
	for i := range w.st.Agents {
		if w.st.Agents[i].ID == id {
			w.st.Agents[i].Exited = false
		}
	}
	return nil
}

func (w *fakeWorkspace) CloseTab(_ context.Context, id string) error {
	if err := w.record("close " + id); err != nil {
		return err
	}
	for env, tabs := range w.st.Tabs {
		w.st.Tabs[env] = slices.DeleteFunc(tabs, func(t project.TabState) bool { return t.ID == id })
	}
	w.st.Agents = slices.DeleteFunc(w.st.Agents, func(a project.AgentState) bool { return a.TabID == id })
	return nil
}

const serviceFile = `version = 1
name = "shop"
[env]
required = ["DATABASE_URL"]
[[tab]]
name = "dev"
  [[tab.pane]]
  command = ["npm", "run", "dev"]
[[agent]]
name = "reviewer"
kind = ["claude", "codex"]
[[agent]]
name = "fixer"
kind = "codex"
isolate = true
`

func newService(t *testing.T, text string) (*project.Service, *fakeWorkspace, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	w := newFake()
	return project.NewService(project.Config{Dir: filepath.Join(t.TempDir(), "projects"), Workspace: w}), w, dir
}

var vars = map[string]string{"DATABASE_URL": "postgres://x", "UNRELATED": "not sent on"}

func TestUpNeedsTrust(t *testing.T) {
	ctx := context.Background()
	svc, w, dir := newService(t, serviceFile)
	sub := filepath.Join(dir, "web", "src")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	// Found from a directory below it, and untrusted at first.
	p, err := svc.Get(ctx, sub)
	if err != nil || p.Root != dir || p.Trusted || p.Spec == nil || p.Digest == "" {
		t.Fatalf("get = %+v, %v", p, err)
	}
	if _, err := svc.Up(ctx, dir, vars); !errors.Is(err, project.ErrUntrusted) {
		t.Fatalf("up untrusted: %v", err)
	}
	if len(w.calls) != 0 {
		t.Fatalf("an untrusted file ran: %v", w.calls)
	}
	// A plan needs no trust: it is what a person reads first.
	plan, err := svc.Plan(ctx, dir, false, vars)
	if err != nil || len(plan.Actions) != 4 || len(plan.Problems) != 0 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}

	if p, err = svc.Trust(ctx, sub, false); err != nil || !p.Trusted {
		t.Fatalf("trust = %+v, %v", p, err)
	}
	res, err := svc.Up(ctx, dir, vars)
	if err != nil || res.Done != 4 {
		t.Fatalf("up = %+v, %v", res, err)
	}
	want := []string{"env shop", "tab dev", "agent reviewer codex in=", "agent fixer codex in="}
	if !slices.Equal(w.calls, want) {
		t.Errorf("calls = %q, want %q", w.calls, want)
	}
	if v := w.st.Environments[0].Vars; v["DATABASE_URL"] != "postgres://x" || v["UNRELATED"] != "" {
		t.Errorf("environment vars = %v: only declared names are taken from the caller", v)
	}

	// The second time, nothing.
	w.calls = nil
	if res, err = svc.Up(ctx, dir, vars); err != nil || len(res.Plan.Actions) != 0 || res.Plan.Present != 4 || len(w.calls) != 0 {
		t.Fatalf("second up = %+v, %v, calls %v", res, err, w.calls)
	}

	// A change to the file needs trusting again.
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte(serviceFile+"\n# more\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Up(ctx, dir, vars); !errors.Is(err, project.ErrUntrusted) || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("up after a change: %v", err)
	}
	if p, _ := svc.Get(ctx, dir); p.Trusted {
		t.Error("a changed file is still trusted")
	}
	if list, err := svc.List(ctx); err != nil || len(list) != 1 || list[0].Trusted {
		t.Errorf("list = %+v, %v", list, err)
	}
	if p, err := svc.Trust(ctx, dir, true); err != nil || p.Trusted {
		t.Errorf("revoke = %+v, %v", p, err)
	}
	if list, _ := svc.List(ctx); len(list) != 0 {
		t.Errorf("list after revoke = %+v", list)
	}
}

func TestDownKeepsWorktreesAndUpReusesThem(t *testing.T) {
	ctx := context.Background()
	svc, w, dir := newService(t, serviceFile)
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Up(ctx, dir, vars); err != nil {
		t.Fatal(err)
	}
	w.calls = nil
	plan, err := svc.Plan(ctx, dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, a := range plan.Actions {
		ops = append(ops, string(a.Op)+" "+a.Name)
	}
	if want := []string{"stop-agent reviewer", "stop-agent fixer", "close-tab dev"}; !slices.Equal(ops, want) {
		t.Errorf("down plan = %q, want %q", ops, want)
	}
	if _, err := svc.Down(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if len(w.st.Agents) != 0 || len(w.st.Environments) != 2 {
		t.Fatalf("after down: agents %v, environments %v", w.st.Agents, w.st.Environments)
	}

	w.calls = nil
	res, err := svc.Up(ctx, dir, vars)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.calls, "agent fixer codex in=shop-wt-fixer") {
		t.Errorf("calls = %q: the fixer should start again in its kept worktree", w.calls)
	}
	for _, a := range res.Plan.Actions {
		if a.Name == "fixer" && (a.In != "shop-wt-fixer" || a.Worktree != "hive/fixer" || a.Isolate) {
			t.Errorf("fixer's action = %+v", a)
		}
	}
}

func TestPlanProblemsChangeNothing(t *testing.T) {
	ctx := context.Background()
	svc, w, dir := newService(t, serviceFile)
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	w.st.Installed = nil
	w.st.Environments = []project.EnvState{{ID: "shop", Root: "/elsewhere"}}
	res, err := svc.Up(ctx, dir, nil)
	if !errors.Is(err, project.ErrState) || len(w.calls) != 0 {
		t.Fatalf("up = %v, calls %v", err, w.calls)
	}
	joined := strings.Join(res.Plan.Problems, "\n")
	for _, want := range []string{"needs DATABASE_URL", "exists at /elsewhere"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems %q lack %q", res.Plan.Problems, want)
		}
	}

	// Agents whose kinds are not installed, or unknown, are problems too.
	w.st.Environments = nil
	plan, _ := svc.Plan(ctx, dir, false, vars)
	joined = strings.Join(plan.Problems, "\n")
	if !strings.Contains(joined, "none of claude, codex is installed") || !strings.Contains(joined, "codex is not installed") {
		t.Errorf("problems = %q", plan.Problems)
	}
	w.st.Known = []string{"codex"}
	plan, _ = svc.Plan(ctx, dir, false, vars)
	if !strings.Contains(strings.Join(plan.Problems, "\n"), `no agent manifest "claude"`) {
		t.Errorf("problems = %q", plan.Problems)
	}
}

func TestUpStopsAtTheFirstFailure(t *testing.T) {
	ctx := context.Background()
	svc, w, dir := newService(t, serviceFile)
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	w.fail = "tab dev"
	res, err := svc.Up(ctx, dir, vars)
	if err == nil || res.Done != 1 || !strings.Contains(err.Error(), "after 1 of 4") {
		t.Fatalf("up = %+v, %v", res, err)
	}
	if want := []string{"env shop", "tab dev"}; !slices.Equal(w.calls, want) {
		t.Errorf("calls = %q, want %q", w.calls, want)
	}
}

func TestPlanUpdatesVarsAndRestartsExitedAgents(t *testing.T) {
	ctx := context.Background()
	svc, w, dir := newService(t, serviceFile)
	w.st.Environments = []project.EnvState{{ID: "shop", Root: dir, Vars: map[string]string{"DATABASE_URL": "old"}}}
	w.st.Tabs["shop"] = []project.TabState{{ID: "t1", Name: "dev"}, {ID: "t2", Name: "reviewer"}}
	w.st.Agents = []project.AgentState{{ID: "a1", Name: "reviewer", Kind: "claude", EnvironmentID: "shop", TabID: "t2", Exited: true}}
	plan, err := svc.Plan(ctx, dir, false, vars)
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, a := range plan.Actions {
		ops = append(ops, fmt.Sprintf("%s %s%v", a.Op, a.Name, a.Vars))
	}
	want := []string{"set-vars [DATABASE_URL]", "restart-agent reviewer[]", "start-agent fixer[]"}
	if !slices.Equal(ops, want) || plan.Present != 1 {
		t.Errorf("plan = %q (present %d), want %q", ops, plan.Present, want)
	}
	// Values never travel in a plan.
	for _, a := range plan.Actions {
		if strings.Contains(fmt.Sprintf("%+v", a.Vars), "postgres") {
			t.Errorf("a value in the plan: %+v", a)
		}
	}
}

func TestTrackedLocalFileIsTrusted(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(project.FileName, "version = 1\n")
	write(project.LocalFileName, "version = 1\n[vars]\nA = \"1\"\n")
	tracked := false
	svc := project.NewService(project.Config{
		Dir: filepath.Join(t.TempDir(), "p"), Workspace: newFake(),
		Tracked: func(string, string) bool { return tracked },
	})
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	// The person's own file changes freely.
	write(project.LocalFileName, "version = 1\n[vars]\nA = \"2\"\n")
	if p, _ := svc.Get(ctx, dir); !p.Trusted || p.LocalFile == "" {
		t.Errorf("an untracked local change lost trust: %+v", p)
	}
	// One in git is the project's, and under trust.
	tracked = true
	if p, _ := svc.Get(ctx, dir); p.Trusted {
		t.Error("a tracked local file is not under trust")
	}
}

func TestBrokenFileCannotBeTrusted(t *testing.T) {
	ctx := context.Background()
	svc, _, dir := newService(t, "version = 1\nnonsense = true\n")
	p, err := svc.Get(ctx, dir)
	if err != nil || p.Error == "" || p.Spec != nil {
		t.Fatalf("get = %+v, %v", p, err)
	}
	if _, err := svc.Trust(ctx, dir, false); !errors.Is(err, project.ErrState) {
		t.Errorf("trust a broken file: %v", err)
	}
	if _, err := svc.Plan(ctx, dir, false, nil); !errors.Is(err, project.ErrInvalid) {
		t.Errorf("plan a broken file: %v", err)
	}
	if _, err := svc.Get(ctx, t.TempDir()); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("no file: %v", err)
	}
	if _, err := svc.Get(ctx, "relative"); !errors.Is(err, project.ErrInvalid) {
		t.Errorf("a relative directory: %v", err)
	}
}
