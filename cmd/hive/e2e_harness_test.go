package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// cli runs the hive CLI (this test binary, re-executed) against one
// isolated HIVE_HOME, the way a user's shell would.
type cli struct {
	t    *testing.T
	home string
	cfg  string
	env  []string // added to every command's environment (and so the daemon's)
	exe  string   // the hive binary to run; empty means this test binary
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	// Short path: the socket must fit macOS's 104-byte limit.
	home, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	c := &cli{t: t, home: home, cfg: filepath.Join(home, "config.toml")}
	// The test's temporary directories (its repositories) are removed by a
	// cleanup t.TempDir registers on its first call; cleanups run last
	// registered first. Registered here, before the daemon's stop, they are
	// removed after it: a daemon still writing git objects (snapshots, the
	// git tracker) into a repository cannot make their removal fail.
	_ = t.TempDir()
	t.Cleanup(func() {
		c.run("stop")
		c.killLeftoverDaemon()
		_ = os.RemoveAll(home)
	})
	return c
}

// run executes `hive args...` and returns stdout, stderr and the exit code.
func (c *cli) run(args ...string) (string, string, int) {
	c.t.Helper()
	exe := c.exe
	if exe == "" {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(),
		"HIVE_CLI_TEST_EXEC=1",
		"HIVE_HOME="+c.home,
		"HIVE_CONFIG="+c.cfg,
		"HIVE_LOG=debug",
		// Race-enabled binaries otherwise sleep a second at exit; shims and
		// daemons here are race-enabled children.
		"GORACE=atexit_sleep_ms=0",
	)
	cmd.Env = append(cmd.Env, c.env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		c.t.Fatalf("run hive %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), code
}

// must runs a command that has to succeed.
func (c *cli) must(args ...string) string {
	c.t.Helper()
	out, errOut, code := c.run(args...)
	if code != 0 {
		c.t.Fatalf("hive %s: exit %d\nstdout: %s\nstderr: %s\ndaemon log:\n%s", strings.Join(args, " "), code, out, errOut, c.daemonLog())
	}
	return out
}

func (c *cli) daemonLog() string {
	data, _ := os.ReadFile(filepath.Join(c.home, "logs", "daemon.log"))
	return string(data)
}

func (c *cli) pid() int {
	data, err := os.ReadFile(filepath.Join(c.home, "hive.pid"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// killLeftoverDaemon is a safety net so a failed test never leaks a daemon.
func (c *cli) killLeftoverDaemon() {
	if pid := c.pid(); pid > 0 {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
}

func (c *cli) waitFor(desc string, cond func() bool) {
	c.t.Helper()
	c.waitForUpTo(10*time.Second, desc, cond)
}

func (c *cli) waitForUpTo(d time.Duration, desc string, cond func() bool) {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %s\ndaemon log:\n%s", desc, c.daemonLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
