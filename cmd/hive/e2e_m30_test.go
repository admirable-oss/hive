//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/project"
)

// m30Project is M30's acceptance file: a main environment with a tab of
// two panes and two agents (one in a worktree of its own), another
// environment in a subdirectory, and a variable whose value comes from
// hive.local.toml.
const m30Project = `version = 1
name = "shop"

[vars]
NODE_ENV = "development"

[env]
required = ["DATABASE_URL"]

[[tab]]
name = "dev"
  [[tab.pane]]
  name = "server"
  command = ["sh", "-c", "echo serving; exec sleep 600"]
  cwd = "web"
  [[tab.pane]]
  name = "shell"

[[agent]]
name = "reviewer"
kind = "codex"

[[agent]]
name = "fixer"
kind = "codex"
isolate = true

[[environment]]
id = "shop-api"
path = "api"
  [[environment.tab]]
  name = "api"
    [[environment.tab.pane]]
    command = ["sh", "-c", "echo api; exec sleep 600"]
`

type m30Agents struct {
	Agents []struct {
		Name          string `json:"name"`
		EnvironmentID string `json:"environment_id"`
		PaneID        string `json:"pane_id"`
		State         string `json:"state"`
	} `json:"agents"`
}

// running are the agents with panes, by name, and the environment each
// runs in.
func (c *cli) m30Running() map[string]string {
	c.t.Helper()
	var list m30Agents
	decode(c.t, c.must("--json", "agent", "list"), &list)
	out := map[string]string{}
	for _, a := range list.Agents {
		if a.PaneID != "" && a.State != "exited" {
			out[a.Name] = a.EnvironmentID
		}
	}
	return out
}

func TestM30ProjectsAsCode(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeTaskAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := newRepo(t, filepath.Join(t.TempDir(), "shop"))
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("web/.keep", "")
	write("api/.keep", "")
	write(".gitignore", project.LocalFileName+"\n")
	write(project.FileName, m30Project)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "hive.toml")
	write(project.LocalFileName, "version = 1\n[vars]\nDATABASE_URL = \"postgres://localhost/shop\"\n")

	// Checked without a daemon, and from a directory below the file.
	if out := c.must("project", "check", filepath.Join(repo, "web")); !strings.Contains(out, "2 environments, 2 tabs, 2 agents") {
		t.Fatalf("check: %s", out)
	}

	// Untrusted, it runs nothing, off a terminal and without --yes.
	_, errOut, code := c.run("up", repo)
	if code == 0 || !strings.Contains(errOut, "is not trusted") || !strings.Contains(errOut, "echo serving; exec sleep 600") {
		t.Fatalf("up untrusted: exit %d\n%s", code, errOut)
	}
	if got := c.m30Running(); len(got) != 0 {
		t.Fatalf("agents started by an untrusted file: %v", got)
	}

	// The plan is what runs.
	var plan project.Plan
	decode(t, c.must("--json", "up", repo, "--plan"), &plan)
	var planned []string
	for _, a := range plan.Actions {
		planned = append(planned, string(a.Op)+" "+a.Environment+"/"+a.Name)
	}
	want := []string{
		"create-environment shop/", "create-tab shop/dev", "start-agent shop/reviewer", "start-agent shop/fixer",
		"create-environment shop-api/", "create-tab shop-api/api",
	}
	if !slices.Equal(planned, want) || len(plan.Problems) != 0 {
		t.Fatalf("plan = %q (problems %q), want %q", planned, plan.Problems, want)
	}

	if out := c.must("project", "trust", repo); !strings.Contains(out, "runs while it keeps this text") {
		t.Fatalf("trust: %s", out)
	}
	if out := c.must("up", repo); !strings.Contains(out, "shop is up") {
		t.Fatalf("up: %s", out)
	}
	running := c.m30Running()
	if running["reviewer"] != "shop" || running["fixer"] == "" || running["fixer"] == "shop" {
		t.Fatalf("agents = %v: the reviewer in shop, the fixer in a worktree", running)
	}
	fixerEnv := running["fixer"]
	var env struct {
		Env map[string]string `json:"env"`
	}
	decode(t, c.must("--json", "env", "get", "shop"), &env)
	if env.Env["DATABASE_URL"] != "postgres://localhost/shop" || env.Env["NODE_ENV"] != "development" {
		t.Errorf("shop's variables = %v", env.Env)
	}
	if out := c.must("tab", "list", "shop-api"); !strings.Contains(out, "api") {
		t.Errorf("shop-api's tabs: %s", out)
	}

	// The second time, nothing changes.
	if out := c.must("up", repo); !strings.Contains(out, "nothing to do: shop is up") {
		t.Fatalf("second up: %s", out)
	}

	// Down stops the agents and keeps the fixer's worktree, with its work.
	var wt struct {
		Path string `json:"path"`
	}
	decode(t, c.must("--json", "env", "get", fixerEnv), &wt)
	work := filepath.Join(wt.Path, "work-in-progress.txt")
	if err := os.WriteFile(work, []byte("not committed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := c.must("down", repo); !strings.Contains(out, "shop is down") {
		t.Fatalf("down: %s", out)
	}
	if got := c.m30Running(); len(got) != 0 {
		t.Fatalf("agents after down: %v", got)
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("down lost the worktree's work: %v", err)
	}

	// Up again: the fixer goes back to its worktree.
	c.must("up", repo)
	if got := c.m30Running(); got["fixer"] != fixerEnv || got["reviewer"] != "shop" {
		t.Fatalf("agents after up again = %v, want the fixer in %s", got, fixerEnv)
	}

	// A change to the file needs trusting again.
	write(project.FileName, m30Project+"\n[[agent]]\nname = \"extra\"\nkind = \"codex\"\n")
	if _, errOut, code := c.run("up", repo); code == 0 || !strings.Contains(errOut, "is not trusted") || !strings.Contains(errOut, "agent extra") {
		t.Fatalf("up after a change: exit %d\n%s", code, errOut)
	}
	if out := c.must("project", "show", repo); !strings.Contains(out, "trusted: no") {
		t.Errorf("show after a change: %s", out)
	}
	if out := c.must("--yes", "up", repo); !strings.Contains(out, "+ agent shop/extra") {
		t.Errorf("up --yes: %s", out)
	}
}
