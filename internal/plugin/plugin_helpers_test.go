package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

// Shared by the tests on every system.

type published struct {
	mu     sync.Mutex
	events []string
}

func (p *published) publish(typ string, _ any) {
	p.mu.Lock()
	p.events = append(p.events, typ)
	p.mu.Unlock()
}

// startHost links manifest (with files) and runs a host over it.
func startHost(t *testing.T, manifest string, files map[string]string, cfg HostConfig) (*Host, *Store, *published) {
	t.Helper()
	dir := writePlugin(t, manifest, files, false)
	s := NewStore(filepath.Join(t.TempDir(), "plugins"))
	if _, _, err := s.Link(context.Background(), dir, "dev"); err != nil {
		t.Fatal(err)
	}
	pub := &published{}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Launcher == "" {
		// TestMain is the launcher a confined plugin needs on Linux.
		cfg.Launcher, _ = os.Executable()
	}
	cfg.Store, cfg.Publish = s, pub.publish
	h := NewHost(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the host to load", func() bool { return len(h.Status()) > 0 })
	return h, s, pub
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func statusOf(h *Host, id string) Status {
	for _, s := range h.Status() {
		if s.ID == id {
			return s
		}
	}
	return Status{}
}
