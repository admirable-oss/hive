//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
)

// TestM19UpgradeFromEarlierReleases upgrades a running hive of each earlier
// release in HIVE_UPGRADE_FROM (tags such as v1.0.0-rc.1, built from this
// repository, or paths to release binaries) to this build, and back:
//
//   - the earlier release starts the daemon, an agent and a tab, and saves
//     a snapshot;
//   - its own `hive update --from` installs this build, and the agent, its
//     screen and the tabs are still there under the new daemon;
//   - the earlier CLI still works against the newer daemon, and does not
//     replace it;
//   - this build's `hive update --rollback` goes back, and the earlier
//     daemon reads what the newer one wrote, the agent still running.
//
// The nightly workflow runs it from every earlier 1.x tag.
func TestM19UpgradeFromEarlierReleases(t *testing.T) {
	from := os.Getenv("HIVE_UPGRADE_FROM")
	if from == "" {
		t.Skip("set HIVE_UPGRADE_FROM to earlier releases (tags or binaries), e.g. v1.0.0-rc.1")
	}
	for _, release := range strings.Fields(strings.ReplaceAll(from, ",", " ")) {
		t.Run(release, func(t *testing.T) { upgradeFrom(t, release) })
	}
}

func upgradeFrom(t *testing.T, release string) {
	c := newCLI(t)
	earlier := releaseBinary(t, release)
	c.exe = filepath.Join(c.home, "bin", "hive")
	copyFile(t, earlier, c.exe)
	// The earlier CLI, which stays what it is when c.exe is replaced.
	earlierCLI := filepath.Join(c.home, "earlier-hive")
	copyFile(t, earlier, earlierCLI)
	this := filepath.Join(c.home, "this-hive")
	copyFile(t, os.Args[0], this)

	c.must("env", "create", "dev", "--managed")
	id, pid := c.startAgent("-t", "dev", "--", "sh", "-c", agentScript("upgrade-agent"))
	c.waitSnapshot(id, "upgrade-agent")
	c.must("tab", "create", "dev", "--name", "work")
	c.must("session", "save")
	tabsBefore := tabNames(c.tabs())
	daemon := c.pid()

	// Upgrade, with the earlier release's own update command.
	var up struct {
		Updated   bool     `json:"updated"`
		Restarted []string `json:"restarted"`
	}
	decode(t, c.must("--json", "update", "--from", this), &up)
	if !up.Updated || !slices.Equal(up.Restarted, []string{"default"}) {
		t.Fatalf("update from %s: %+v", release, up)
	}
	upgraded := c.pid()
	if upgraded == daemon {
		t.Fatal("the daemon was not restarted on this build")
	}
	if level := daemonAPILevel(t, c); level != protocol.APILevel {
		t.Fatalf("after the upgrade the daemon reports API level %d, want %d", level, protocol.APILevel)
	}
	checkAgentKept(t, c, id, pid, tabsBefore, "the upgrade")

	// An earlier client and a newer daemon work together, and the earlier
	// client leaves the newer daemon alone.
	old := *c
	old.exe = earlierCLI
	checkAgentKept(t, &old, id, pid, tabsBefore, "the earlier CLI's commands")
	old.must("status")
	if c.pid() != upgraded {
		t.Fatal("the earlier CLI replaced the newer daemon")
	}

	// Go back, with this build's update command: --rollback when the
	// earlier release's update kept the binary it replaced, as releases do
	// from M13 on; otherwise by installing the earlier binary, which is the
	// documented way back from those (docs/upgrading.md).
	if _, err := os.Stat(c.exe + ".old"); err == nil {
		var back struct {
			RolledBack bool `json:"rolled_back"`
		}
		decode(t, c.must("--json", "update", "--rollback"), &back)
		if !back.RolledBack {
			t.Fatal("update --rollback did not roll back")
		}
	} else {
		t.Logf("%s's update kept no previous binary; going back with `hive update --from`", release)
		decode(t, c.must("--json", "update", "--from", earlierCLI), &up)
		if !up.Updated {
			t.Fatalf("installing %s again: %+v", release, up)
		}
	}
	if c.pid() == upgraded {
		t.Fatal("the daemon was not restarted on the earlier release")
	}
	checkAgentKept(t, c, id, pid, tabsBefore, "the rollback")
}

// releaseBinary returns a hive binary of release: the file itself when it
// is one, otherwise the tag built from this repository.
func releaseBinary(t *testing.T, release string) string {
	t.Helper()
	if info, err := os.Stat(release); err == nil && !info.IsDir() {
		return release
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tree := filepath.Join(dir, "src")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", tree, release).CombinedOutput(); err != nil {
		t.Fatalf("check out %s: %v\n%s", release, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("git", "-C", root, "worktree", "remove", "--force", tree).Run() })
	bin := filepath.Join(dir, "hive")
	build := exec.Command("go", "build", "-trimpath", "-o", bin,
		"-ldflags", "-X github.com/admirable-oss/hive/internal/buildinfo.version="+release, "./cmd/hive")
	build.Dir = tree
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", release, err, out)
	}
	return bin
}

// checkAgentKept checks that the agent still runs as the same process, its
// screen is shown, and the tabs are the ones there were before.
func checkAgentKept(t *testing.T, c *cli, id string, pid int, tabs []string, after string) {
	t.Helper()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the agent died in %s: %v", after, err)
	}
	var p struct {
		Status string `json:"status"`
		PID    int    `json:"pid"`
	}
	decode(t, c.must("--json", "ps", "get", id), &p)
	if p.Status != "running" || p.PID != pid {
		t.Fatalf("after %s the agent is %+v, want running with pid %d", after, p, pid)
	}
	c.waitSnapshot(id, "upgrade-agent")
	if got := tabNames(c.tabs()); !slices.Equal(got, tabs) {
		t.Fatalf("after %s the tabs are %v, want %v", after, got, tabs)
	}
}

func daemonAPILevel(t *testing.T, c *cli) int {
	t.Helper()
	var v struct {
		Daemon *client.Status `json:"daemon"`
	}
	decode(t, c.must("--json", "version"), &v)
	if v.Daemon == nil {
		t.Fatal("no daemon answers")
	}
	return v.Daemon.APILevel
}

// tabNames identifies tabs by ID, environment and name, sorted.
func tabNames(tabs []pane.Tab) []string {
	out := make([]string, 0, len(tabs))
	for _, tb := range tabs {
		out = append(out, tb.ID+" "+tb.EnvironmentID+" "+tb.Name)
	}
	slices.Sort(out)
	return out
}
