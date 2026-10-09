package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// cli runs the hive CLI (this test binary, re-executed) against one
// isolated HIVE_HOME, the way a user's shell would.
type cli struct {
	t    *testing.T
	home string
	cfg  string
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
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		"HIVE_CLI_TEST_EXEC=1",
		"HIVE_HOME="+c.home,
		"HIVE_CONFIG="+c.cfg,
		"HIVE_LOG=debug",
		// Race-enabled binaries otherwise sleep a second at exit; shims and
		// daemons here are race-enabled children.
		"GORACE=atexit_sleep_ms=0",
	)
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
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (c *cli) waitFor(desc string, cond func() bool) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %s\ndaemon log:\n%s", desc, c.daemonLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestE2E_AutostartLifecycleAndLogs(t *testing.T) {
	c := newCLI(t)

	// Nothing runs yet: status says so with its own exit code.
	if out, _, code := c.run("status"); code != exitNotRunning || !strings.Contains(out, "not running") {
		t.Fatalf("status before start: exit %d, %q", code, out)
	}

	// Any daemon command starts it on demand.
	if out, errOut, code := c.run("env", "create", "dev"); code != 0 || !strings.Contains(out, `created environment "dev"`) || !strings.Contains(errOut, "started the daemon") {
		t.Fatalf("env create: exit %d\n%s\n%s\n%s", code, out, errOut, c.daemonLog())
	}
	pid := c.pid()
	if pid == 0 {
		t.Fatal("daemon wrote no pid file")
	}

	var st struct {
		Status string `json:"status"`
		PID    int    `json:"pid"`
	}
	if err := json.Unmarshal([]byte(c.must("status", "--json")), &st); err != nil || st.Status != "running" || st.PID != pid {
		t.Fatalf("status --json = %+v, %v (pid file %d)", st, err, pid)
	}

	// The original `start <env> -t` form and the flag-first form both work.
	out := c.must("ps", "start", "dev", "--", "sh", "-c", "echo out-line; echo err-line >&2")
	plainID := strings.Fields(out)[2]
	var started struct {
		ID       string `json:"id"`
		Terminal bool   `json:"terminal"`
	}
	if err := json.Unmarshal([]byte(c.must("--json", "ps", "start", "-t", "dev", "--", "sh", "-c", "echo in-a-pty; sleep 30")), &started); err != nil || !started.Terminal {
		t.Fatalf("ps start -t --json: %+v, %v", started, err)
	}
	legacy := c.must("ps", "start", "dev", "-t", "--", "sh", "-c", "sleep 30")
	if !strings.Contains(legacy, "started process") {
		t.Fatalf("legacy start form: %q", legacy)
	}

	c.waitFor("plain process to exit", func() bool { return strings.Contains(c.must("ps", "get", plainID), "exited") })
	if got := c.must("ps", "logs", plainID); got != "out-line\n" {
		t.Fatalf("stdout log = %q", got)
	}
	if got := c.must("ps", "logs", "--stderr", plainID); got != "err-line\n" {
		t.Fatalf("stderr log = %q", got)
	}
	c.waitFor("terminal output", func() bool { return strings.Contains(c.must("ps", "logs", started.ID), "in-a-pty") })
	if _, errOut, code := c.run("ps", "logs", "--stderr", started.ID); code == 0 || !strings.Contains(errOut, "stderr to stdout") {
		t.Fatalf("stderr of a terminal process should fail clearly: exit %d, %q", code, errOut)
	}
	if _, errOut, code := c.run("ps", "get", "ffffffffffffffff"); code != exitError || !strings.Contains(errOut, "not found") {
		t.Fatalf("unknown process: exit %d, %q", code, errOut)
	}

	list := c.must("ps", "list", "dev")
	if strings.Count(list, "\n") != 4 { // header + three processes
		t.Fatalf("ps list:\n%s", list)
	}

	// The daemon log records lifecycle events.
	for _, want := range []string{"daemon listening", "process started", "process ended"} {
		if !strings.Contains(c.daemonLog(), want) {
			t.Fatalf("daemon log lacks %q:\n%s", want, c.daemonLog())
		}
	}

	// stop shuts down the daemon and its agents, and is idempotent.
	if out := c.must("stop"); !strings.Contains(out, "stopped") {
		t.Fatalf("stop: %q", out)
	}
	if _, err := os.Stat(filepath.Join(c.home, "hive.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pid file should be gone after stop: %v", err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		c.waitFor("daemon process to exit", func() bool { return syscall.Kill(pid, 0) != nil })
	}
	if out := c.must("stop"); !strings.Contains(out, "not running") {
		t.Fatalf("second stop: %q", out)
	}

	// After a restart the old agents' records are closed out, not resurrected.
	c.must("daemon", "start")
	if got := c.must("ps", "get", started.ID); !strings.Contains(got, "killed") {
		t.Fatalf("agent stopped with the daemon should be recorded as killed:\n%s", got)
	}
}

func TestE2E_AutostartCanBeDisabled(t *testing.T) {
	c := newCLI(t)
	if err := os.WriteFile(c.cfg, []byte("[daemon]\nautostart = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := c.run("env", "list")
	if code != exitError || !strings.Contains(errOut, "hive daemon start") {
		t.Fatalf("with autostart off: exit %d, %q", code, errOut)
	}
	if c.pid() != 0 {
		t.Fatal("no daemon may be started when autostart is off")
	}
	c.must("daemon", "start")
	c.must("env", "list")
}

func TestE2E_ConfigCommands(t *testing.T) {
	c := newCLI(t)
	if out := c.must("config", "path"); strings.TrimSpace(out) != c.cfg {
		t.Fatalf("config path = %q, want %q", out, c.cfg)
	}
	c.must("config", "init")
	if _, _, code := c.run("config", "init"); code == 0 {
		t.Fatal("config init must not overwrite without --force")
	}
	if out := c.must("config", "validate"); !strings.Contains(out, "is valid") {
		t.Fatalf("validate defaults: %q", out)
	}

	if err := os.WriteFile(c.cfg, []byte("[log]\nlevel = \"loud\"\n[nope]\nx = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := c.run("config", "validate")
	if code != exitError || !strings.Contains(out, "log.level") || !strings.Contains(out, `"nope"`) {
		t.Fatalf("validate bad file: exit %d, %q", code, out)
	}
	// Clients warn and carry on.
	if _, errOut, _ := c.run("status"); !strings.Contains(errOut, "warning: config") {
		t.Fatalf("clients should surface config warnings, got %q", errOut)
	}

	if err := os.WriteFile(c.cfg, []byte("[daemon\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The daemon refuses a config it cannot parse, and autostart says why.
	_, errOut, code := c.run("env", "list")
	if code == 0 || !strings.Contains(errOut, "syntax error") {
		t.Fatalf("broken config: exit %d, %q", code, errOut)
	}
}
