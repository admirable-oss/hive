package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
)

// withShell makes new panes run /bin/sh, which starts at once (a user's
// login shell may take seconds).
func (c *cli) withShell() {
	c.t.Helper()
	if err := os.WriteFile(c.cfg, []byte("[terminal]\nshell = \"/bin/sh\"\n"), 0o600); err != nil {
		c.t.Fatal(err)
	}
}

// session registers cleanup for a named session's daemon.
func (c *cli) session(name string) {
	c.t.Cleanup(func() {
		c.run("--session", name, "stop")
		data, err := os.ReadFile(filepath.Join(c.home, "sessions", name, "hive.pid"))
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

// m2Layout is the acceptance workspace: 3 environments (two in directories
// of the user's, one managed), 5 tabs and 12 panes, with nested splits,
// commands, working directories and variables.
func m2Layout(apiRoot string) string {
	return fmt.Sprintf(`{
  "version": 1,
  "environments": [
    {"id": "api", "root": %q, "env": {"PORT": "8080"}, "tabs": [
      {"name": "agents", "layout": {"split": "horizontal", "ratio": 0.6,
        "first": {"pane": {"name": "claude", "command": ["sh", "-c", "read x"]}},
        "second": {"split": "vertical", "ratio": 0.5,
          "first": {"pane": {"name": "tests", "command": ["sh", "-c", "read x"], "cwd": "src"}},
          "second": {"split": "horizontal", "ratio": 0.3,
            "first": {"pane": {"name": "logs", "env": {"LEVEL": "debug"}}},
            "second": {"pane": {}}}}}},
      {"name": "shell", "layout": {"split": "vertical", "ratio": 0.7,
        "first": {"pane": {"name": "main"}},
        "second": {"pane": {"command": ["sh", "-c", "read x"]}}}}
    ]},
    {"id": "web", "root": "web", "tabs": [
      {"name": "dev", "layout": {"split": "horizontal", "ratio": 0.5,
        "first": {"pane": {"name": "server", "command": ["sh", "-c", "read x"]}},
        "second": {"split": "vertical", "ratio": 0.4,
          "first": {"pane": {}},
          "second": {"pane": {"name": "watch"}}}}},
      {"name": "review", "layout": {"pane": {"name": "reviewer"}}}
    ]},
    {"id": "scratch", "tabs": [
      {"name": "main", "layout": {"split": "horizontal", "ratio": 0.5,
        "first": {"pane": {}},
        "second": {"pane": {"name": "notes"}}}}
    ]}
  ]
}`, apiRoot)
}

func TestM2LayoutAppliesAndRoundTripsThroughExport(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	c.session("copy")
	project := t.TempDir()
	for _, d := range []string{"api/src", "web"} {
		if err := os.MkdirAll(filepath.Join(project, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(project, "layout.json")
	if err := os.WriteFile(file, []byte(m2Layout(filepath.Join(project, "api"))), 0o600); err != nil {
		t.Fatal(err)
	}

	out := c.must("layout", "apply", file)
	if !strings.Contains(out, "opened 5 tabs with 12 panes") || strings.Count(out, "created environment") != 3 {
		t.Fatalf("apply said:\n%s", out)
	}
	var panes []pane.Pane
	decode(t, c.must("pane", "list", "--json"), &panes)
	if len(panes) != 12 {
		t.Fatalf("%d panes, want 12", len(panes))
	}

	// The export is the file, with the relative root resolved.
	var want pane.LayoutSpec
	decode(t, m2Layout(filepath.Join(project, "api")), &want)
	want.Environments[1].Root = filepath.Join(project, "web")
	// Export lists environments by ID.
	slices.SortFunc(want.Environments, func(a, b pane.EnvironmentSpec) int { return strings.Compare(a.ID, b.ID) })
	exported := c.must("layout", "export")
	var got pane.LayoutSpec
	decode(t, exported, &got)
	if a, b := canonical(t, want), canonical(t, got); a != b {
		t.Fatalf("export differs from the applied file:\nwant %s\n got %s", a, b)
	}

	// Applying the export in a fresh session rebuilds the same workspace.
	exportFile := filepath.Join(project, "exported.json")
	if err := os.WriteFile(exportFile, []byte(exported), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("--session", "copy", "layout", "apply", exportFile)
	var again pane.LayoutSpec
	decode(t, c.must("--session", "copy", "layout", "export"), &again)
	if a, b := canonical(t, got), canonical(t, again); a != b {
		t.Fatalf("round trip through a second session differs:\n%s\n%s", a, b)
	}

	// The sessions are separate: other panes, other processes.
	var copyPanes []pane.Pane
	decode(t, c.must("--session", "copy", "pane", "list", "--json"), &copyPanes)
	if len(copyPanes) != 12 {
		t.Fatalf("copy session has %d panes", len(copyPanes))
	}
	seen := map[string]bool{}
	for _, p := range panes {
		seen[p.ID], seen[p.ProcessID] = true, true
	}
	for _, p := range copyPanes {
		if seen[p.ID] || seen[p.ProcessID] {
			t.Fatalf("pane %s / process %s shared between sessions", p.ID, p.ProcessID)
		}
	}

	// Panes know where they are, and where their working directory and
	// variables came from.
	var logs pane.Pane
	for _, p := range copyPanes {
		if p.Name == "logs" {
			logs = p
		}
	}
	c.must("--session", "copy", "pane", "zoom", logs.ID) // wide enough for one line
	c.must("--session", "copy", "pane", "run", logs.ID, `echo "at=$(pwd) s=$HIVE_SESSION p=$HIVE_PANE_ID e=$HIVE_ENV_ID v=$PORT/$LEVEL tp=$TERM_PROGRAM"`)
	line := strings.TrimSpace(c.must("--session", "copy", "pane", "wait-output", logs.ID, "--regex", "^at=/", "--anywhere", "--timeout", "10s"))
	wantLine := fmt.Sprintf("s=copy p=%s e=api v=8080/debug tp=hive", logs.ID)
	if !strings.HasSuffix(line, wantLine) || !strings.Contains(line, "/api") {
		t.Fatalf("pane environment line %q, want it to end in %q", line, wantLine)
	}
	// The tests pane started in the api environment's src directory.
	var tests pane.Pane
	for _, p := range copyPanes {
		if p.Name == "tests" {
			tests = p
		}
	}
	if tests.Process == nil || filepath.Base(tests.Process.WorkingDir) != "src" {
		t.Fatalf("tests pane process = %+v", tests.Process)
	}

	// Removing a session stops its agents and deletes its data only.
	c.must("session", "remove", "copy")
	if _, err := os.Stat(filepath.Join(c.home, "sessions", "copy")); !os.IsNotExist(err) {
		t.Fatalf("session directory still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "api", "src")); err != nil {
		t.Fatalf("a session removal touched the user's directory: %v", err)
	}
	decode(t, c.must("pane", "list", "--json"), &panes)
	if len(panes) != 12 {
		t.Fatalf("the default session lost panes: %d", len(panes))
	}
}

func TestM2PaneCommands(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	c.must("env", "create", "dev", "--managed")
	fields := strings.Fields(c.must("tab", "create", "dev", "--name", "work"))
	if len(fields) != 2 {
		t.Fatalf("tab create printed %q", fields)
	}
	tab, first := fields[0], fields[1]
	second := strings.TrimSpace(c.must("pane", "split", first, "-d", "down", "--name", "below", "--", "sh", "-c", `printf 'ready\n'; read line; echo "got:$line"; read x`))
	c.must("pane", "wait-output", second, "--text", "ready", "--anywhere", "--timeout", "10s")

	// Typing and keys, then a wait that sees only new output.
	c.must("pane", "send-text", second, "hello")
	c.must("pane", "send-keys", second, "Enter")
	if got := strings.TrimSpace(c.must("pane", "wait-output", second, "--regex", "^got:", "--anywhere", "--timeout", "10s")); got != "got:hello" {
		t.Fatalf("wait-output printed %q", got)
	}
	if _, _, code := c.run("pane", "wait-output", second, "--text", "ready", "--timeout", "300ms"); code != exitTimeout {
		t.Fatalf("a timed-out wait exits %d, want %d", code, exitTimeout)
	}
	if !strings.Contains(c.must("pane", "read", second), "got:hello") {
		t.Fatal("read does not show the screen")
	}

	// Layout operations show up in the pane list.
	c.must("pane", "zoom", second)
	var p pane.Pane
	decode(t, c.must("pane", "get", second, "--json"), &p)
	if !p.Zoomed || p.Rect == nil || p.Rect.H != 50 {
		t.Fatalf("zoomed pane = %+v rect %+v", p, p.Rect)
	}
	c.must("pane", "zoom", second, "--off")
	decode(t, c.must("pane", "get", first, "--json"), &p)
	before := p.Rect.H
	c.must("pane", "resize", first, "-d", "down", "-n", "10")
	decode(t, c.must("pane", "get", first, "--json"), &p)
	if p.Rect == nil || p.Rect.H != before+10 {
		t.Fatalf("resized pane rect %+v, want height %d", p.Rect, before+10)
	}
	c.must("pane", "swap", first, second)
	c.must("pane", "rename", second, "renamed")
	c.must("env", "create", "other", "--managed")
	c.must("pane", "move", second, "--env", "other")
	decode(t, c.must("pane", "get", second, "--json"), &p)
	if p.EnvironmentID != "other" || p.Name != "renamed" || p.Process == nil || p.Process.Status != "running" {
		t.Fatalf("moved pane = %+v", p)
	}
	// The moved process still answers.
	c.must("pane", "send-keys", second, "q", "Enter")
	c.waitFor("the moved process to exit", func() bool {
		decode(t, c.must("pane", "get", second, "--json"), &p)
		return p.Process != nil && p.Process.Status == "exited"
	})

	c.must("pane", "close", first)
	var tabs []pane.Tab
	decode(t, c.must("tab", "list", "dev", "--json"), &tabs)
	if len(tabs) != 0 {
		t.Fatalf("closing tab %s's last pane closes it: %+v", tab, tabs)
	}

	// Without a pane argument, commands use $HIVE_PANE_ID.
	if _, errOut, code := c.run("pane", "read"); code != exitUsage || !strings.Contains(errOut, "HIVE_PANE_ID") {
		t.Fatalf("pane read with no pane: exit %d, %s", code, errOut)
	}
}

func TestM2Worktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	c := newCLI(t)
	c.withShell()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := filepath.Join(t.TempDir(), "proj")
	gitRun(t, "", "init", "-q", "-b", "main", repo)
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")

	c.must("env", "create", "main", "--cwd", repo)
	var env environment.Environment
	c.waitFor("the git status of the repository", func() bool {
		decode(t, c.must("env", "get", "main", "--json"), &env)
		return env.Git != nil && env.Git.Branch == "main"
	})

	// A file appears: the environment turns dirty.
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("worktree", "create", "feature/login", "--repo", repo)
	decode(t, c.must("env", "get", "feature-login", "--json"), &env)
	wantPath := filepath.Join(c.home, "worktrees", "proj", "feature-login")
	if env.Path != wantPath || env.Worktree == nil || env.Worktree.Branch != "feature/login" || env.Managed {
		t.Fatalf("worktree environment = %+v", env)
	}
	c.waitFor("the worktree's git status", func() bool {
		decode(t, c.must("env", "get", "feature-login", "--json"), &env)
		return env.Git != nil && env.Git.Branch == "feature/login" && !env.Git.Dirty()
	})
	list := c.must("worktree", "list", "--repo", repo)
	if !strings.Contains(list, "feature/login") || !strings.Contains(list, "feature-login") {
		t.Fatalf("worktree list:\n%s", list)
	}
	// The same branch twice is refused.
	if _, _, code := c.run("worktree", "create", "feature/login", "--repo", repo); code == 0 {
		t.Fatal("a second worktree at the same path must be refused")
	}

	// An agent in the worktree is stopped when it is removed; uncommitted
	// work is protected unless --force.
	c.must("tab", "create", "feature-login", "--", "sh", "-c", "read x")
	if err := os.WriteFile(filepath.Join(wantPath, "wip.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := c.run("worktree", "remove", "feature-login"); code == 0 || !strings.Contains(errOut, "uncommitted") {
		t.Fatalf("removing a dirty worktree: exit %d, %s", code, errOut)
	}
	var panes []pane.Pane
	decode(t, c.must("pane", "list", "--env", "feature-login", "--json"), &panes)
	if len(panes) != 1 || panes[0].Process.Status != "running" {
		t.Fatal("a refused removal must not stop the agents")
	}
	c.must("worktree", "remove", "feature-login", "--force")
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("worktree directory still there: %v", err)
	}
	if _, _, code := c.run("env", "get", "feature-login"); code == 0 {
		t.Fatal("the worktree's environment must be removed")
	}
	if out := gitRun(t, repo, "branch", "--list", "feature/login"); !strings.Contains(out, "feature/login") {
		t.Fatal("the branch must be kept")
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func decode(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
}

func canonical(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
