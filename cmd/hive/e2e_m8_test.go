//go:build !windows

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/metadata"
)

// fakeGH answers `gh pr view <branch> …` like GitHub would for one open
// pull request whose checks passed, on branch feature/login.
const fakeGH = `#!/bin/sh
[ "$1 $2" = "pr view" ] || exit 2
if [ "$3" = "feature/login" ]; then echo "#12 open ✓"; exit 0; fi
echo "no pull requests found for branch \"$3\"" >&2
exit 1
`

// pluginRepo copies a reference plugin into a new git repository, as its
// author would publish it.
func pluginRepo(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "examples", "plugins", name)
	dst := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		return os.WriteFile(filepath.Join(dst, rel), data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, "", "init", "-q", "-b", "main", dst)
	gitRun(t, dst, "add", ".")
	gitRun(t, dst, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "release")
	return dst
}

// TestM8ReferencePlugins is M8's acceptance: the two reference plugins,
// installed from git, work end to end. "PR status" puts a worktree's pull
// request in the sidebar; "spawn reviewer" starts a reviewer agent in every
// new worktree and asks it to review the branch.
func TestM8ReferencePlugins(t *testing.T) {
	c := newCLI(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	// The installed hive: plugins call it back as $HIVE_BIN.
	c.exe = filepath.Join(c.home, "bin", "hive")
	copyFile(t, os.Args[0], c.exe)
	bin := filepath.Join(c.home, "fake-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"gh": fakeGH, "codex": fakeCodex} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c.env = append(c.env,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PR_STATUS_INTERVAL=1", "SPAWN_REVIEWER_KIND=codex", "GIT_CONFIG_GLOBAL="+os.DevNull)

	// Installing shows what a plugin runs, and needs a yes.
	prStatus, spawn := pluginRepo(t, "pr-status"), pluginRepo(t, "spawn-reviewer")
	if _, errOut, code := c.run("plugin", "install", prStatus); code == 0 || !strings.Contains(errOut, "./pr-status.sh watch") || !strings.Contains(errOut, "--yes") {
		t.Fatalf("install without a yes: exit %d\n%s", code, errOut)
	}
	c.must("daemon", "start")
	for _, repo := range []string{prStatus, spawn} {
		if out := c.must("plugin", "install", repo, "--yes"); !strings.Contains(out, "installed plugin") {
			t.Fatalf("install %s: %s", repo, out)
		}
	}
	var plugins []pluginRow
	c.waitFor("pr-status's watcher to run", func() bool {
		decode(t, c.must("--json", "plugin", "list"), &plugins)
		return len(plugins) == 2 && plugins[0].ID == "pr-status" && plugins[0].Running == 1 && plugins[1].ID == "spawn-reviewer"
	})

	// A worktree for a branch with a pull request.
	repo := filepath.Join(c.home, "app")
	gitRun(t, "", "init", "-q", "-b", "main", repo)
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	c.must("worktree", "create", "feature/login", "--repo", repo)
	env := "feature-login"

	// PR status: the token appears on the environment.
	tokenOn := func() string {
		var list []metadata.Entry
		decode(t, c.must("--json", "metadata"), &list)
		for _, e := range list {
			if e.Environment == env {
				for _, tok := range e.Tokens {
					if tok.Key == "pr" {
						return tok.Text
					}
				}
			}
		}
		return ""
	}
	c.waitForUpTo(30*time.Second, "the PR token", func() bool { return tokenOn() == "#12 open ✓" })
	if out := c.must("plugin", "run", "pr-status", "refresh"); !strings.Contains(out, "PR tokens refreshed") {
		t.Fatalf("refresh action: %q", out)
	}

	// Spawn reviewer: a reviewer agent works in the new worktree.
	var reviewer agent.Agent
	c.waitForUpTo(30*time.Second, "the reviewer agent", func() bool {
		var res agent.ListResult
		decode(t, c.must("--json", "agent", "list"), &res)
		i := slices.IndexFunc(res.Agents, func(a agent.Agent) bool { return a.Name == "reviewer" && a.EnvironmentID == env })
		if i >= 0 {
			reviewer = res.Agents[i]
		}
		return i >= 0
	})
	if reviewer.Kind != "codex" {
		t.Fatalf("reviewer = %+v", reviewer)
	}
	result := filepath.Join(c.home, "worktrees", "app", "feature-login", "result.txt")
	c.waitForUpTo(30*time.Second, "the review prompt", func() bool {
		data, _ := os.ReadFile(result)
		return strings.Contains(string(data), "Review the changes on branch feature/login")
	})

	// Disabling a plugin stops it, and its token runs out on its own.
	c.must("plugin", "disable", "pr-status")
	c.waitForUpTo(30*time.Second, "the token to expire", func() bool { return tokenOn() == "" })
	c.must("plugin", "uninstall", "spawn-reviewer")
	decode(t, c.must("--json", "plugin", "list"), &plugins)
	if len(plugins) != 1 || plugins[0].Enabled {
		t.Fatalf("after disable and uninstall: %+v", plugins)
	}
	if logs := c.must("plugin", "logs", "spawn-reviewer"); !strings.Contains(logs, "asked") {
		t.Fatalf("spawn-reviewer's log:\n%s", logs)
	}
}
