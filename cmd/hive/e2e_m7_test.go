//go:build !windows

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/remote"
)

// fakeSSH stands in for ssh: each host is a directory under
// $HIVE_FAKE_SSH_ROOT, its own home with its own HIVE_HOME, and the remote
// command runs there in sh, with the system's PATH only (a hive installed on
// this machine is not on the fake host). Options are accepted and ignored;
// -O exit (the shared connection) succeeds.
const fakeSSH = `#!/bin/sh
host=""
while [ $# -gt 0 ]; do
	case "$1" in
	-o|-O|-p|-l|-i) shift 2 ;;
	-t|-T) shift ;;
	--) shift; break ;;
	-*) shift ;;
	*) if [ -z "$host" ]; then host="$1"; shift; else break; fi ;;
	esac
done
[ "$1" = "--" ] && shift
dir="$HIVE_FAKE_SSH_ROOT/$host"
if [ ! -d "$dir" ]; then
	echo "ssh: Could not resolve hostname $host" >&2
	exit 255
fi
[ $# -eq 0 ] && exit 0
unset HIVE_SSH HIVE_SOCKET_PATH HIVE_SESSION
export HOME="$dir" HIVE_HOME="$dir/.hive" HIVE_CONFIG="$dir/config.toml" PATH=/usr/bin:/bin:/usr/sbin:/sbin
cd "$dir" && exec sh -c "$*"
`

// fakeHosts sets up the fake ssh and returns the directory hosts live in.
func (c *cli) fakeHosts() string {
	c.t.Helper()
	root := filepath.Join(c.home, "h")
	if err := os.MkdirAll(root, 0o755); err != nil {
		c.t.Fatal(err)
	}
	ssh := filepath.Join(c.home, "fake-ssh")
	if err := os.WriteFile(ssh, []byte(fakeSSH), 0o755); err != nil {
		c.t.Fatal(err)
	}
	c.env = append(c.env, "HIVE_SSH="+ssh, "HIVE_FAKE_SSH_ROOT="+root)
	return root
}

// host makes a fake machine; with hive installed, a copy of this binary is
// at ~/.local/bin/hive there. Its daemon is stopped when the test ends.
func (c *cli) host(root, name string, installed bool) string {
	c.t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.t.Fatal(err)
	}
	if installed {
		copyFile(c.t, os.Args[0], filepath.Join(dir, ".local", "bin", "hive"))
	}
	c.t.Cleanup(func() {
		c.run("--remote", name, "stop")
		data, err := os.ReadFile(filepath.Join(dir, ".hive", "hive.pid"))
		if pid := strings.TrimSpace(string(data)); err == nil && pid != "" {
			_ = exec.Command("kill", "-9", pid).Run()
		}
	})
	return dir
}

// remoteTarget is how a test's own client reaches a fake host.
func (c *cli) remoteTarget(root, host string) remote.Target {
	ssh := filepath.Join(c.home, "fake-ssh")
	return remote.Target{Host: host, SSH: []string{ssh}}
}

func withFakeSSHEnv(t *testing.T, root string) {
	t.Setenv("HIVE_FAKE_SSH_ROOT", root)
	t.Setenv("HIVE_CLI_TEST_EXEC", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
}

// TestM7RemoteInstallsAndRunsCommands: --remote reaches a machine without
// hive, installs it after confirmation, then every kind of command works
// there: daemon calls over the ssh bridge, and the rest run there whole.
func TestM7RemoteInstallsAndRunsCommands(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	alpha := c.host(root, "alpha", false)

	// Not installed: without a terminal to ask on, nothing is installed.
	if _, errOut, code := c.run("--remote", "alpha", "env", "list"); code == 0 || !strings.Contains(errOut, "hive is not installed on alpha") || !strings.Contains(errOut, "--yes") {
		t.Fatalf("no hive there: exit %d: %s", code, errOut)
	}
	if _, errOut, code := c.run("--remote", "nowhere", "env", "list"); code == 0 || !strings.Contains(errOut, "Could not resolve hostname") {
		t.Fatalf("unknown host: exit %d: %s", code, errOut)
	}
	_, errOut, code := c.run("--remote", "alpha", "--yes", "env", "create", "dev", "--managed")
	if code != 0 || !strings.Contains(errOut, "installed hive on alpha") {
		t.Fatalf("install: exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(alpha, ".hive", "environments", "dev")); err != nil {
		t.Fatalf("the environment is not on alpha: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.home, "environments", "dev")); err == nil {
		t.Fatal("the environment was made here instead")
	}

	// Daemon calls go over the bridge.
	var started struct {
		ID string `json:"id"`
	}
	decode(t, c.must("--remote", "alpha", "--json", "ps", "start", "-t", "dev", "--", "sh", "-c", "echo remote-hello; sleep 60"), &started)
	c.waitFor("the remote agent's screen", func() bool {
		return strings.Contains(c.must("--remote", "alpha", "terminal", "snapshot", started.ID), "remote-hello")
	})
	// Commands without a daemon client run there as a whole.
	var st struct {
		PID int `json:"pid"`
	}
	decode(t, c.must("--remote", "alpha", "--json", "daemon", "status"), &st)
	data, _ := os.ReadFile(filepath.Join(alpha, ".hive", "hive.pid"))
	if fmt.Sprint(st.PID) != strings.TrimSpace(string(data)) {
		t.Fatalf("daemon status ran here, not on alpha: %+v (alpha's pid file %q)", st, data)
	}
	if got := strings.TrimSpace(c.must("--remote", "alpha", "config", "path")); got != filepath.Join(alpha, "config.toml") {
		t.Fatalf("config path = %q", got)
	}
	// The local daemon was never started.
	if c.pid() != 0 {
		t.Fatal("a local daemon was started")
	}
}

// TestM7RemoteFramesAreCompressedPacedAndCarryTheClipboard reads a remote
// agent's screen as the UI does: compressed, paced by the reported link,
// and with text a program copied (OSC 52) arriving here.
func TestM7RemoteFramesAreCompressedPacedAndCarryTheClipboard(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	c.host(root, "alpha", true)
	withFakeSSHEnv(t, root)
	c.must("--remote", "alpha", "env", "create", "dev", "--managed")
	var started struct {
		ID string `json:"id"`
	}
	decode(t, c.must("--remote", "alpha", "--json", "ps", "start", "-t", "dev", "--", "sh", "-c",
		`sleep 1; printf '\033]52;c;%s\a' "$(printf 'copied on alpha' | base64)"; i=0; while :; do i=$((i+1)); echo "line $i of a busy agent"; sleep 0.01; done`), &started)

	var wire atomic.Int64
	t2 := c.remoteTarget(root, "alpha")
	cl := client.NewService(client.Config{
		Name: "test", Remote: true, FrameBudget: 16 << 10,
		Dial: func(ctx context.Context) (net.Conn, error) {
			conn, err := t2.Dial(ctx)
			if err != nil {
				return nil, err
			}
			return countingConn{Conn: conn, n: &wire}, nil
		},
	})
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fs, err := cl.TerminalFrames(ctx, client.ViewRequest{ProcessID: started.ID, Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	var copied string
	frames, start := 0, time.Now()
	for time.Since(start) < 4*time.Second {
		f, err := fs.Next()
		if err != nil {
			t.Fatal(err)
		}
		frames++
		if f.Clipboard != nil {
			copied = f.Clipboard.Data
		}
	}
	elapsed := time.Since(start)
	if copied == "" {
		t.Fatal("the copy did not arrive")
	}
	if got, _ := decodeBase64(copied); got != "copied on alpha" {
		t.Fatalf("copied %q", got)
	}
	// 16 KiB/s, with a second's burst: about 16 KiB/s × elapsed, whatever
	// the agent prints.
	if limit := int64(16<<10) * int64(elapsed.Seconds()+2); wire.Load() > limit {
		t.Fatalf("%d bytes in %s with a 16 KiB/s budget", wire.Load(), elapsed)
	}
	if frames < 4 {
		t.Fatalf("only %d frames in %s", frames, elapsed)
	}
	t.Logf("%d frames, %d bytes in %s", frames, wire.Load(), elapsed)
}

type countingConn struct {
	net.Conn
	n *atomic.Int64
}

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// TestM7SavedMachines manages saved machines and sends commands to them.
func TestM7SavedMachines(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	c.host(root, "alpha", true)
	c.host(root, "beta", true)

	c.must("machine", "add", "a", "alpha")
	c.must("machine", "add", "b", "beta", "--session", "agents")
	if _, errOut, code := c.run("machine", "add", "a", "beta"); code == 0 || !strings.Contains(errOut, "exists") {
		t.Fatalf("duplicate name: exit %d: %s", code, errOut)
	}
	if _, errOut, code := c.run("machine", "add", "x", "nowhere"); code == 0 || !strings.Contains(errOut, "--no-check") {
		t.Fatalf("unreachable host: exit %d: %s", code, errOut)
	}
	c.must("machine", "add", "x", "nowhere", "--no-check")

	// Commands go to a machine by name, in its session.
	c.must("--machine", "b", "env", "create", "work", "--managed")
	if _, err := os.Stat(filepath.Join(root, "beta", ".hive", "sessions", "agents", "environments", "work")); err != nil {
		t.Fatalf("not in beta's agents session: %v", err)
	}
	var list []remote.Machine
	decode(t, c.must("--json", "machine", "list"), &list)
	if len(list) != 3 || list[0].Name != "a" || list[1].Session != "agents" {
		t.Fatalf("list = %+v", list)
	}

	var states []machineState
	decode(t, c.must("--json", "machine", "status", "a", "b"), &states)
	if len(states) != 2 || !states[0].Reachable || !states[1].Reachable || states[0].Version == "" {
		t.Fatalf("status = %+v", states)
	}
	out, _, _ := c.run("--json", "machine", "status")
	decode(t, out, &states)
	if len(states) != 3 || states[2].Reachable || states[2].Error == "" {
		t.Fatalf("status of every machine = %+v", states)
	}

	c.must("machine", "rename", "b", "bee")
	c.must("machine", "disable", "bee")
	if _, errOut, code := c.run("--machine", "bee", "env", "list"); code == 0 || !strings.Contains(errOut, "disabled") {
		t.Fatalf("a disabled machine: exit %d: %s", code, errOut)
	}
	c.must("machine", "enable", "bee")
	if out := c.must("--machine", "bee", "env", "list"); !strings.Contains(out, "work") {
		t.Fatalf("env list on bee:\n%s", out)
	}
	c.must("machine", "reconnect", "a")
	c.must("machine", "remove", "x")
	if _, errOut, code := c.run("--machine", "x", "env", "list"); code == 0 || !strings.Contains(errOut, "no such machine") {
		t.Fatalf("a removed machine: exit %d: %s", code, errOut)
	}
}

func decodeBase64(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

// busyAgent works like Claude Code at full tilt: a spinner and timer that
// change ten times a second, a status line, and a new line of output every
// fifth of a second scrolling the transcript.
const busyAgent = `i=0
printf '\033[2J'
while :; do
	i=$((i+1))
	printf '\033[1;1H\033[2K\033[38;5;174m✻ Deliberating… (%ds · ↑ %d tokens · esc to interrupt)\033[0m' $((i/10)) $((i*37))
	if [ $((i % 2)) -eq 0 ]; then
		printf '\033[3;20r\033[20;1H\n\033[2K  ⎿ Read src/module_%d.go (%d lines)\033[r' $i $((i*13 % 900))
	fi
	printf '\033[22;1H\033[2K\033[2m  ? for shortcuts                         %d%% context left\033[0m' $((100 - i % 100))
	sleep 0.1
done`

// TestM7BusyPaneBandwidth streams a busy agent to a remote client with the
// default settings and checks the M7 target: under 50 MB an hour.
func TestM7BusyPaneBandwidth(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	c.host(root, "alpha", true)
	withFakeSSHEnv(t, root)
	c.must("--remote", "alpha", "env", "create", "dev", "--managed")
	var started struct {
		ID string `json:"id"`
	}
	decode(t, c.must("--remote", "alpha", "--json", "ps", "start", "-t", "--width", "200", "--height", "50", "dev", "--", "sh", "-c", busyAgent), &started)

	var wire atomic.Int64
	target := c.remoteTarget(root, "alpha")
	cl := client.NewService(client.Config{
		Name: "test", Remote: true, FrameBudget: 48 << 10, // the default [remote] frame_budget_kb
		Dial: func(ctx context.Context) (net.Conn, error) {
			conn, err := target.Dial(ctx)
			if err != nil {
				return nil, err
			}
			return countingConn{Conn: conn, n: &wire}, nil
		},
	})
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	fs, err := cl.TerminalFrames(ctx, client.ViewRequest{ProcessID: started.ID, Width: 200, Height: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if _, err := fs.Next(); err != nil { // the keyframe, before measuring
		t.Fatal(err)
	}
	before, start := wire.Load(), time.Now()
	frames := 0
	for time.Since(start) < 10*time.Second {
		if _, err := fs.Next(); err != nil {
			t.Fatal(err)
		}
		frames++
	}
	perHour := float64(wire.Load()-before) / time.Since(start).Hours()
	t.Logf("%d frames, %.1f MB/h", frames, perHour/1e6)
	if perHour > 50e6 {
		t.Fatalf("a busy pane costs %.1f MB/h, over the 50 MB/h target", perHour/1e6)
	}
	if frames < 50 {
		t.Fatalf("only %d frames in 10s: the screen would look frozen", frames)
	}
}
