//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/client"
)

// fakeGHPullRequest stands in for GitHub's gh: it records how it was
// called and prints a pull request's URL, as gh pr create does.
const fakeGHPullRequest = `#!/bin/sh
printf '%s\n' "$*" >> "$GH_LOG"
echo "https://github.com/acme/proj/pull/42"
`

// TestM23ParallelBranches is M23's acceptance: three agents work in
// parallel branches of one repository. Hive reports their overlap before
// anything merges, shows each diff for review, gates readiness on tests
// passing, and opens a pull request only on request. The merged worktree
// is cleaned up, and the unmerged work is never lost.
func TestM23ParallelBranches(t *testing.T) {
	c := newCLI(t)
	cfg := `[terminal]
shell = "/bin/sh"

[branches]
check_interval = "1s"

[[branches.gate]]
name = "tests"
command = ["sh", "-c", "test -s result.txt"]
`
	if err := os.WriteFile(c.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for name, script := range map[string]string{"codex": fakeTaskAgent, "gh": fakeGHPullRequest} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ghLog := filepath.Join(t.TempDir(), "gh.log")
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_LOG="+ghLog)

	// One repository, whose remote names GitHub and whose pushes land in a
	// bare repository here.
	repo := newRepo(t, filepath.Join(t.TempDir(), "proj"))
	bare := filepath.Join(t.TempDir(), "proj.git")
	gitRun(t, "", "init", "-q", "--bare", bare)
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/acme/proj.git")
	gitRun(t, repo, "config", "url."+bare+".pushInsteadOf", "https://github.com/acme/proj.git")
	c.must("env", "create", "proj", "--cwd", repo)

	// Three agents, each on a branch of its own.
	var agents []client.AgentStarted
	for range 3 {
		var started client.AgentStarted
		decode(t, c.must("--json", "agent", "start", "--kind", "codex", "--env", "proj", "--isolate"), &started)
		if started.Environment.Worktree == nil || !strings.HasPrefix(started.Environment.Worktree.Branch, "hive/codex/") {
			t.Fatalf("an isolated agent's environment: %+v", started.Environment)
		}
		agents = append(agents, started)
	}
	list := func(all bool) []branch.Branch {
		args := []string{"--json", "branch", "list"}
		if all {
			args = append(args, "--all")
		}
		var out []branch.Branch
		decode(t, c.must(args...), &out)
		return out
	}
	get := func(ref string) branch.Branch {
		var b branch.Branch
		decode(t, c.must("--json", "branch", "get", ref), &b)
		return b
	}
	if bs := list(false); len(bs) != 3 || slices.ContainsFunc(bs, func(b branch.Branch) bool { return b.Base != "main" || b.Owner == nil || b.Owner.Kind != "codex" }) {
		t.Fatalf("three branches from main, each for its agent: %+v", bs)
	}

	// They work in parallel, each writing result.txt its own way.
	for i, a := range agents {
		c.must("agent", "prompt", a.Pane.ID, "the work of agent "+string(rune('a'+i)), "--wait", "--timeout", "1m")
	}

	// Hive reports their overlap, down to the conflict, before any merge.
	c.must("branch", "check")
	for _, a := range agents {
		b := get(a.Agent.ID)
		if b.State != branch.StateWorking || b.Files != 1 || len(b.Overlaps) != 2 {
			t.Fatalf("%s overlaps the other two: %+v", b.Name, b)
		}
		for _, o := range b.Overlaps {
			if !slices.Equal(o.Files, []string{"result.txt"}) || (!o.Unknown && !slices.Equal(o.Conflicts, []string{"result.txt"})) {
				t.Fatalf("%s's overlap with %s: %+v", b.Name, o.Branch, o)
			}
		}
	}
	a, b2, b3 := get(agents[0].Agent.ID), get(agents[1].Agent.ID), get(agents[2].Agent.ID)
	if out, _, code := c.run("branch", "conflicts", a.ID, "--with", b2.ID); code != exitError || !strings.Contains(out, "<<<<<<<") {
		t.Fatalf("the conflict, shown: exit %d\n%s", code, out)
	}
	if listed := c.must("branch", "list"); !strings.Contains(listed, "conflicting") {
		t.Fatalf("the list says so:\n%s", listed)
	}

	// Each diff, for review.
	for i, x := range []branch.Branch{a, b2, b3} {
		diff := c.must("branch", "diff", x.ID, "--color", "never")
		if !strings.Contains(diff, "+the work of agent "+string(rune('a'+i))) {
			t.Fatalf("%s's diff:\n%s", x.Name, diff)
		}
	}

	// A review comment goes back to the agent as a prompt.
	c.must("branch", "comment", a.ID, "Mention the ticket", "--file", "result.txt", "--line", "1", "--send")
	c.waitForUpTo(time.Minute, "the comment's prompt to be done", func() bool {
		data, _ := os.ReadFile(filepath.Join(a.Path, "result.txt"))
		return strings.Contains(string(data), "Review comments on your changes")
	})
	c.must("agent", "wait", agents[0].Pane.ID, "--until", "idle", "--timeout", "1m")

	// Readiness waits on the tests: a failing gate leaves a branch working.
	if out, _, code := c.run("branch", "ready", b2.ID, "--", "sh", "-c", "echo 2 tests failed; exit 1"); code != exitError || !strings.Contains(out, "2 tests failed") {
		t.Fatalf("a failing gate: exit %d\n%s", code, out)
	}
	if got := get(b2.ID); got.State != branch.StateWorking || len(got.Gates) != 1 || got.Gates[0].Passed {
		t.Fatalf("not ready: %+v", got)
	}
	if out := c.must("branch", "ready", a.ID); !strings.Contains(out, "tests: ok") || !strings.Contains(out, "is ready") {
		t.Fatalf("the configured gate passes:\n%s", out)
	}

	// No pull request, and no push, until asked.
	gitRun(t, a.Path, "add", "-A")
	gitRun(t, a.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Do the work of agent a")
	if _, err := os.Stat(ghLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("gh ran before anyone asked")
	}
	if _, errOut, code := c.run("branch", "pr", a.ID); code == 0 || !strings.Contains(errOut, "push it first") {
		t.Fatalf("a branch the remote lacks: exit %d %s", code, errOut)
	}
	if refs := gitRun(t, bare, "branch", "--list"); refs != "" {
		t.Fatalf("nothing is pushed unasked: %q", refs)
	}
	if out := c.must("branch", "pr", a.ID, "--push"); strings.TrimSpace(out) != "https://github.com/acme/proj/pull/42" {
		t.Fatalf("pr: %q", out)
	}
	logged, _ := os.ReadFile(ghLog)
	if !strings.Contains(string(logged), "pr create --repo acme/proj --head "+a.Name+" --base main --title Do the work of agent a") {
		t.Fatalf("gh was asked: %q", logged)
	}
	if refs := gitRun(t, bare, "branch", "--list"); !strings.Contains(refs, a.Name) {
		t.Fatalf("pushed on request: %q", refs)
	}

	// a merges; its agent is done with it. Its worktree is cleaned up, and
	// the unmerged branches keep theirs.
	c.must("agent", "stop", agents[0].Agent.ID)
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "merge", "-q", "--no-edit", a.Name)
	c.waitForUpTo(time.Minute, a.Name+" to be cleaned up", func() bool {
		return get(a.ID).State == branch.StateCleaned
	})
	if _, err := os.Stat(a.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the merged worktree is removed: %v", err)
	}
	for _, x := range []branch.Branch{b2, b3} {
		if got := get(x.ID); got.State != branch.StateWorking || got.Path == "" {
			t.Fatalf("%s is kept: %+v", x.Name, got)
		}
		if _, err := os.Stat(filepath.Join(x.Path, "result.txt")); err != nil {
			t.Fatalf("%s's work is there: %v", x.Name, err)
		}
	}
	if bs := list(false); len(bs) != 2 {
		t.Fatalf("two branches remain active: %+v", bs)
	}
	if all := list(true); len(all) != 3 {
		t.Fatalf("the cleaned branch is remembered: %+v", all)
	}

	// Removing unmerged work takes --force, and even then it is saved.
	if _, errOut, code := c.run("branch", "clean", b2.ID); code == 0 || !strings.Contains(errOut, "uncommitted") {
		t.Fatalf("uncommitted work is kept unasked: exit %d %s", code, errOut)
	}
	var cleaned []branch.Branch
	decode(t, c.must("--json", "branch", "clean", b2.ID, "--force"), &cleaned)
	if len(cleaned) != 1 || cleaned[0].State != branch.StateRemoved || !strings.HasPrefix(cleaned[0].Saved, "refs/hive/saved/") {
		t.Fatalf("forced: %+v", cleaned)
	}
	if saved := gitRun(t, repo, "show", cleaned[0].Saved+":result.txt"); !strings.Contains(saved, "the work of agent b") {
		t.Fatalf("the saved work: %q", saved)
	}
	if !strings.Contains(gitRun(t, repo, "branch", "--list"), b2.Name) {
		t.Fatal("an unmerged branch is never deleted")
	}
}
