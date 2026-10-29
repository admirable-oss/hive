//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/issue"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/task"
)

const m26ForgePlugin = `api = 2
id = "acme"
version = "1.0.0"
min_hive_version = "1.7.0"

[permissions]
required = ["network"]
[permissions.network]
hosts = ["git.example.org:443"]

[[contributes.forges]]
name = "acme"
hosts = ["git.example.org"]
command = ["sh", "-c", "read -r in; case \"$in\" in *'\"op\":\"pr.create\"'*'\"project\":\"acme/proj\"'*) echo '{\"url\":\"https://git.example.org/acme/proj/pulls/5\",\"number\":5}';; *) echo \"unexpected: $in\" >&2; exit 1;; esac"]

[[contributes.trackers]]
name = "acme"
command = ["sh", "-c", "read -r in; case \"$in\" in *'\"op\":\"get\"'*) echo '{\"title\":\"Fix the login redirect\",\"body\":\"It loops.\",\"state\":\"open\",\"url\":\"https://tracker.example.org/A-1\"}';; *'\"op\":\"search\"'*) echo '{\"issues\":[{\"key\":\"A-1\",\"title\":\"Fix the login redirect\",\"state\":\"open\"}]}';; *'\"op\":\"comment\"'*) echo '{}';; *) exit 1;; esac"]
`

// TestM26PluginForgeAndTracker: a plugin's forge opens the pull request
// of a repository whose remote is on a host it lists; its tracker serves
// issue.* and starts a task from an issue.
func TestM26PluginForgeAndTracker(t *testing.T) {
	c := newCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte(m26ForgePlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.cfg, []byte("[terminal]\nshell = \"/bin/sh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := newRepo(t, filepath.Join(t.TempDir(), "proj"))
	bare := filepath.Join(t.TempDir(), "proj.git")
	gitRun(t, "", "init", "-q", "--bare", bare)
	gitRun(t, repo, "remote", "add", "origin", "https://git.example.org/acme/proj.git")
	gitRun(t, repo, "config", "url."+bare+".pushInsteadOf", "https://git.example.org/acme/proj.git")
	c.must("daemon", "start")
	c.must("plugin", "link", dir)
	c.must("env", "create", "proj", "--cwd", repo)

	// The forge.
	if out := c.must("plugin", "contributions"); !strings.Contains(out, "acme (git.example.org)") {
		t.Fatalf("contributions:\n%s", out)
	}
	c.must("worktree", "create", "fix/login", "--repo", repo, "--id", "login")
	var env environment.Environment
	decode(t, c.must("--json", "env", "get", "login"), &env)
	path := env.Path
	if err := os.WriteFile(filepath.Join(path, "fix.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, path, "add", "-A")
	gitRun(t, path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Fix the login redirect")
	if out := c.must("branch", "pr", "login", "--push"); strings.TrimSpace(out) != "https://git.example.org/acme/proj/pulls/5" {
		t.Fatalf("pr: %q", out)
	}

	// The tracker.
	var i issue.Issue
	decode(t, c.must("--json", "issue", "get", "acme:A-1"), &i)
	if i.Ref != "acme:A-1" || i.Title != "Fix the login redirect" || i.URL != "https://tracker.example.org/A-1" {
		t.Fatalf("issue: %+v", i)
	}
	if out := c.must("issue", "search", "login", "--tracker", "acme"); !strings.Contains(out, "acme:A-1") {
		t.Fatalf("search:\n%s", out)
	}
	c.must("issue", "comment", "acme:A-1", "On it.")
	if _, stderr, code := c.run("issue", "get", "nowhere:X-1"); code == 0 || !strings.Contains(stderr, "no issue tracker") {
		t.Fatalf("an unknown tracker: %d %s", code, stderr)
	}

	// A task from the issue, held for approval so no agent starts.
	var plan task.Plan
	decode(t, c.must("--json", "task", "run", "--from-issue", "acme:A-1", "--env", "proj", "--isolate", "--close-issue", "--approve"), &plan)
	if len(plan.Tasks) != 1 {
		t.Fatalf("plan: %+v", plan)
	}
	s := plan.Tasks[0].Spec
	if s.Issue != "acme:A-1" || !s.CloseIssue || s.Title != "Fix the login redirect" || !strings.Contains(s.Prompt, "It loops.") || plan.Name != "acme:A-1" {
		t.Fatalf("the task: %+v (plan %q)", s, plan.Name)
	}
	c.must("task", "cancel", plan.ID)
}
