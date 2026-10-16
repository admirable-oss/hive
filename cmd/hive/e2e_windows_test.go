//go:build windows

package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/process"
)

// TestWindowsCoreWorkflow is M13's acceptance on Windows, through the CLI
// and a real detached daemon: the runtime starts on demand; an agent runs
// in a pseudo console under its shim, takes input and shows output; it
// keeps running through a daemon restart and is re-attached; plugins are
// held to their platforms; an observer reads over the observer socket
// until revoked; hive updates itself and rolls back; and `hive stop` ends
// everything.
func TestWindowsCoreWorkflow(t *testing.T) {
	c := newCLI(t)
	c.exe = filepath.Join(c.home, "bin", "hive.exe")
	copyFile(t, os.Args[0], c.exe)

	if out, _, code := c.run("status"); code != exitNotRunning || !strings.Contains(out, "not running") {
		t.Fatalf("status before start: exit %d, %q", code, out)
	}
	work := t.TempDir()
	if out, errOut, code := c.run("env", "create", "dev", "--cwd", work); code != 0 || !strings.Contains(errOut, "started the daemon") {
		t.Fatalf("env create: exit %d\n%s\n%s\n%s", code, out, errOut, c.daemonLog())
	}

	var p process.Process
	if err := json.Unmarshal([]byte(c.must("--json", "ps", "start", "-t", "dev", "--", "cmd.exe", "/q", "/k", "echo agent-up")), &p); err != nil || !p.Terminal {
		t.Fatalf("start: %+v %v", p, err)
	}
	screen := func(want string) {
		t.Helper()
		c.waitForUpTo(30*time.Second, "screen showing "+want, func() bool {
			out, _, code := c.run("terminal", "snapshot", p.ID)
			return code == 0 && strings.Contains(out, want)
		})
	}
	screen("agent-up")
	c.must("terminal", "input", p.ID, "echo typed-into-conpty\r")
	screen("typed-into-conpty")
	c.must("terminal", "resize", p.ID, "90", "25")
	c.waitFor("the daemon's pid file", func() bool { return c.pid() != 0 })

	// The daemon restarts; the agent's shim keeps it running and the new
	// daemon re-attaches.
	oldPID := c.pid()
	c.must("daemon", "restart")
	c.waitForUpTo(30*time.Second, "a new daemon", func() bool { return c.pid() != 0 && c.pid() != oldPID })
	var again process.Process
	c.waitForUpTo(30*time.Second, "the agent re-attached", func() bool {
		out, _, code := c.run("--json", "ps", "get", p.ID)
		return code == 0 && json.Unmarshal([]byte(out), &again) == nil && again.Active()
	})
	screen("typed-into-conpty")
	c.must("terminal", "input", p.ID, "echo after-restart\r")
	screen("after-restart")

	// Plugins: one for Windows runs; one for Unix only is refused.
	winPlugin := writePluginDir(t, `api = 1
id = "win-hello"
version = "1.0.0"
platforms = ["windows"]
[[actions]]
id = "hello"
command = ["cmd.exe", "/c", "echo hello-from-plugin"]
`)
	c.must("plugin", "link", winPlugin)
	if out := c.must("plugin", "run", "win-hello", "hello"); !strings.Contains(out, "hello-from-plugin") {
		t.Fatalf("plugin action: %q", out)
	}
	unixPlugin := writePluginDir(t, "id = \"unix-only\"\nversion = \"1.0.0\"\nplatforms = [\"linux\", \"darwin\"]\n[[actions]]\nid = \"a\"\ncommand = [\"sh\", \"-c\", \"true\"]\n")
	if _, errOut, code := c.run("plugin", "link", unixPlugin); code == 0 || !strings.Contains(errOut, "not windows") {
		t.Fatalf("a Unix-only plugin linked on Windows: exit %d %s", code, errOut)
	}

	// An observer reads the agent's screen over the observer socket, and
	// is refused once revoked.
	var created struct {
		Credential struct {
			ID string `json:"id"`
		} `json:"credential"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(c.must("--json", "observer", "create", "win-dash", "--env", "dev", "--screens")), &created); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, observerTokenEnv+"="+created.Token)
	if out := c.must("observer", "status"); !strings.Contains(out, "1 environment(s)") {
		t.Fatalf("observer status: %q", out)
	}
	if out := c.must("observer", "snapshot"); !strings.Contains(out, `"dev"`) {
		t.Fatalf("observer snapshot: %q", out)
	}
	c.must("observer", "revoke", created.Credential.ID)
	if _, _, code := c.run("observer", "status"); code == 0 {
		t.Fatal("a revoked observer token still works")
	}

	// Self-update from a release .zip, then roll back.
	release := filepath.Join(c.home, "hive_9.9.9_windows_amd64.zip")
	writeZip(t, release, "hive.exe", os.Args[0])
	if out, errOut, code := c.run("update", "--from", release, "--no-restart"); code != 0 {
		t.Fatalf("update: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(c.exe + ".old"); err != nil {
		t.Fatalf("the replaced binary was not kept: %v", err)
	}
	c.must("update", "--rollback", "--no-restart")

	c.must("stop")
	c.waitForUpTo(30*time.Second, "the agent to end", func() bool {
		return c.pid() == 0
	})
}

func writePluginDir(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hive-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeZip(t *testing.T, path, name, src string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(name)
	if err == nil {
		_, err = w.Write(data)
	}
	if err := errors.Join(err, zw.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
}
