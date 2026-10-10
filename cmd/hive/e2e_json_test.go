package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/client"
)

// TestEveryCommandSpeaksJSON runs commands with --json and checks that
// what they print is one JSON document: scripts and agents rely on it.
// Streams (logs, events, attach) and the UI are left out.
func TestEveryCommandSpeaksJSON(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	gitRun(t, "", "init", "-q", "-b", "main", repo)
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")

	var created client.TabCreated
	steps := [][]string{
		{"env", "create", "dev", "--managed"}, // starts the daemon
		{"status"},
		{"ping"},
		{"version"},
		{"daemon", "status"},
		{"config", "path"},
		{"config", "show"},
		{"config", "validate"},
		{"session", "list"},
		{"env", "create", "repo", "--cwd", repo},
		{"env", "list"},
		{"env", "get", "dev"},
		{"env", "set", "dev", "A=1"},
		{"env", "unset", "dev", "A"},
		{"tab", "create", "dev", "--name", "work"},
		{"tab", "list"},
		{"pane", "list"},
		{"ps", "list"},
		{"agent", "list", "--all"},
		{"agent", "manifests"},
		{"worktree", "list", "--repo", repo},
		{"layout", "export"},
		{"integration", "status"},
		{"api", "snapshot"},
		{"api", "schema"},
	}
	for _, args := range steps {
		out, errOut, code := c.run(append([]string{"--json"}, args...)...)
		var v any
		if err := json.Unmarshal([]byte(out), &v); code != 0 || err != nil {
			t.Errorf("hive --json %s: exit %d, %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, err, out, errOut)
		}
		if args[0] == "tab" && args[1] == "create" {
			_ = json.Unmarshal([]byte(out), &created)
		}
	}

	p := created.Pane.ID
	tab := created.Tab.ID
	more := [][]string{
		{"pane", "get", p},
		{"pane", "split", p, "--no-focus"},
		{"pane", "zoom", p},
		{"pane", "read", p},
		{"pane", "rename", p, "main"},
		{"tab", "rename", tab, "w"},
		{"agent", "get", p},
		{"agent", "explain", p},
		{"agent", "read", p},
		{"tab", "close", tab},
		{"env", "remove", "dev"},
	}
	for _, args := range more {
		out, errOut, code := c.run(append([]string{"--json"}, args...)...)
		var v any
		if err := json.Unmarshal([]byte(out), &v); code != 0 || err != nil {
			t.Errorf("hive --json %s: exit %d, %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, err, out, errOut)
		}
	}
}
