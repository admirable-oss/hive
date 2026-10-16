package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writePlugin(t *testing.T, manifest string, files map[string]string, repo bool) string {
	t.Helper()
	dir := t.TempDir()
	files[ManifestFile] = manifest
	for name, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	if repo {
		for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	return dir
}

const sample = `
id = "pr-status"
name = "PR status"
version = "0.1.0"
description = "Shows pull requests."
min_hive_version = "0.10.0"

[[startup]]
command = ["./watch.sh"]
restart = true

[[actions]]
id = "open"
title = "Open the PR"
command = ["./open.sh", "--web"]
timeout = "10s"

[[events]]
on = "worktree.created"
command = ["./created.sh"]
`

// TestPlatformsAreEnforced: a plugin that does not list this system is
// refused, with a reason; windows is a platform plugins may name.
func TestPlatformsAreEnforced(t *testing.T) {
	m, err := ParseManifest([]byte("id = \"p\"\nversion = \"1.0.0\"\nplatforms = [\"windows\"]\n[[actions]]\nid = \"a\"\ncommand = [\"x\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = m.Supported(t.Context(), "dev")
	if (runtime.GOOS == "windows") != (err == nil) {
		t.Fatalf("on %s: %v", runtime.GOOS, err)
	}
	if _, err := ParseManifest([]byte("id = \"p\"\nversion = \"1.0.0\"\nplatforms = [\"plan9\"]\n[[actions]]\nid = \"a\"\ncommand = [\"x\"]\n")); err == nil {
		t.Fatal("plan9 accepted")
	}
}
