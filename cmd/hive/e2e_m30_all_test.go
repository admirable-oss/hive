package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/project"
)

// TestM30ProjectsEverywhere is M30's acceptance on every system, Windows
// included: a project file written from a template and checked, refused
// until trusted, brought up once and then changing nothing, a tab opened by
// hand seen by diff and written back by save (still trusted), brought down,
// and asked about again after a change. Agents are covered on macOS and
// Linux by TestM30ProjectsAsCode, whose fake agent is a shell script.
func TestM30ProjectsEverywhere(t *testing.T) {
	c := newCLI(t)
	pane := []string{"sh", "-c", "echo pane-up; exec sleep 600"}
	if runtime.GOOS == "windows" {
		c.exe = filepath.Join(c.home, "bin", "hive.exe")
		copyFile(t, os.Args[0], c.exe)
		pane = []string{"cmd.exe", "/q", "/k", "echo pane-up"}
	} else {
		c.withShell()
	}
	repo := newRepo(t, filepath.Join(t.TempDir(), "notes"))
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A template fitted to the repository, and .gitignore told about the
	// person's own file.
	out := c.must("project", "init", repo, "--from", "agents")
	if !strings.Contains(out, "wrote") || !strings.Contains(out, "added hive.local.toml to .gitignore") {
		t.Fatalf("init: %s", out)
	}
	text, err := os.ReadFile(filepath.Join(repo, project.FileName))
	if err != nil || !strings.Contains(string(text), `command = ["go", "test", "./..."]`) || !strings.Contains(string(text), `name = "reviewer"`) {
		t.Fatalf("the template wrote:\n%s (%v)", text, err)
	}
	if _, _, code := c.run("project", "init", repo); code == 0 {
		t.Fatal("init wrote over an existing hive.toml")
	}
	if ignore, _ := os.ReadFile(filepath.Join(repo, ".gitignore")); !strings.Contains(string(ignore), project.LocalFileName) {
		t.Fatalf(".gitignore: %q", ignore)
	}
	if out := c.must("project", "schema"); !strings.Contains(out, `"title": "Hive project file (hive.toml)"`) {
		t.Fatalf("schema: %s", out)
	}

	// The project this test brings up and down: no agents.
	file := "version = 1\nname = \"notes\"\n[vars]\nNOTES_MODE = \"dev\"\n[[tab]]\nname = \"dev\"\n  [[tab.pane]]\n  name = \"server\"\n  command = " + tomlArray(pane) + "\n  [[tab.pane]]\n  name = \"shell\"\n"
	if err := os.WriteFile(filepath.Join(repo, project.FileName), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := c.must("project", "check", repo); !strings.Contains(out, "1 environment, 1 tab, 0 agents") {
		t.Fatalf("check: %s", out)
	}
	if _, errOut, code := c.run("up", repo); code == 0 || !strings.Contains(errOut, "is not trusted") {
		t.Fatalf("up untrusted: exit %d\n%s", code, errOut)
	}
	c.must("project", "trust", repo)
	if out := c.must("up", repo); !strings.Contains(out, "notes is up") {
		t.Fatalf("up: %s\n%s", out, c.daemonLog())
	}
	if out := c.must("up", repo); !strings.Contains(out, "nothing to do") {
		t.Fatalf("second up: %s", out)
	}
	if out := c.must("project", "diff", repo); !strings.Contains(out, "no differences") {
		t.Fatalf("diff: %s", out)
	}

	// A tab opened by hand: diff sees it, save writes it back.
	c.must("tab", "create", "notes", "--name", "scratch")
	if out := c.must("project", "diff", repo); !strings.Contains(out, "extra    tab notes/scratch") {
		t.Fatalf("diff after a new tab: %s", out)
	}
	if _, _, code := c.run("project", "save", repo); code == 0 {
		t.Fatal("save wrote off a terminal without --yes")
	}
	if out := c.must("--yes", "project", "save", repo); !strings.Contains(out, "trusted: yes") {
		t.Fatalf("save: %s", out)
	}
	saved, _ := os.ReadFile(filepath.Join(repo, project.FileName))
	if !strings.Contains(string(saved), `name = "scratch"`) || !strings.Contains(string(saved), `NOTES_MODE = "dev"`) {
		t.Fatalf("saved file:\n%s", saved)
	}
	if out := c.must("up", repo); !strings.Contains(out, "nothing to do") {
		t.Fatalf("up after save: %s", out)
	}

	// Down closes both tabs; the environment stays.
	if out := c.must("down", repo); !strings.Contains(out, "- tab notes/dev") || !strings.Contains(out, "- tab notes/scratch") {
		t.Fatalf("down: %s", out)
	}
	c.must("env", "get", "notes")

	// A change asks again.
	if err := os.WriteFile(filepath.Join(repo, project.FileName), append(saved, []byte("\n# changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := c.run("up", repo); code == 0 || !strings.Contains(errOut, "is not trusted") {
		t.Fatalf("up after a change: exit %d\n%s", code, errOut)
	}
}

func tomlArray(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(a, `\`, `\\`), `"`, `\"`) + `"`
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
