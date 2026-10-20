//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// brokenRelease runs (`hive version --json` answers) but cannot start a
// daemon: what a release built wrong for this system, or with a startup
// bug, looks like to `hive update`.
const brokenRelease = `#!/bin/sh
if [ "$1" = version ]; then echo '{"version":"v9.9.9"}'; exit 0; fi
echo 'this hive cannot start' >&2
exit 3
`

// TestM19UpdateRollsBackWhenTheNewDaemonCannotStart: an update whose daemon
// does not start is undone. The previous binary is put back, the daemon is
// started on it again, and the agents keep running throughout.
func TestM19UpdateRollsBackWhenTheNewDaemonCannotStart(t *testing.T) {
	c := newCLI(t)
	c.exe = filepath.Join(c.home, "bin", "hive")
	copyFile(t, os.Args[0], c.exe)
	broken := filepath.Join(c.home, "broken-hive")
	if err := os.WriteFile(broken, []byte(brokenRelease), 0o755); err != nil {
		t.Fatal(err)
	}

	c.must("env", "create", "dev", "--managed")
	id, pid := c.startAgent("-t", "dev", "--", "sh", "-c", "while :; do sleep 1; done")
	daemon := c.pid()

	out, errOut, code := c.run("update", "--from", broken)
	if code == 0 {
		t.Fatalf("an update whose daemon cannot start succeeded:\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "rolled back") || !strings.Contains(errOut, "agents kept running") {
		t.Fatalf("the error does not say it rolled back: %s", errOut)
	}

	// The binary that ran before is back; the broken one is kept beside it.
	if data, _ := os.ReadFile(c.exe); strings.HasPrefix(string(data), "#!/bin/sh") {
		t.Fatal("the broken release is still installed")
	}
	if data, _ := os.ReadFile(c.exe + ".old"); string(data) != brokenRelease {
		t.Fatalf("the broken release is not kept as %s.old", filepath.Base(c.exe))
	}

	// A daemon of the old binary runs again, without a command starting it.
	now := c.pid()
	if now == 0 || now == daemon {
		t.Fatalf("daemon pid %d (was %d): it was not restarted", now, daemon)
	}
	if err := syscall.Kill(now, 0); err != nil {
		t.Fatalf("the restarted daemon is not running: %v", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the agent died in the failed update: %v", err)
	}
	var p struct {
		Status string `json:"status"`
		PID    int    `json:"pid"`
	}
	decode(t, c.must("--json", "ps", "get", id), &p)
	if p.Status != "running" || p.PID != pid {
		t.Fatalf("after the rollback the agent is %+v, want running with pid %d", p, pid)
	}
}
