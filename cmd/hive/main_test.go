package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

// TestMain doubles as the hive binary: with HIVE_CLI_TEST_EXEC=1 the test
// executable runs the CLI instead of tests. End-to-end tests exec it, and the
// CLI in turn spawns `hive daemon` the way a user's shell would.
func TestMain(m *testing.M) {
	if os.Getenv("HIVE_CLI_TEST_EXEC") == "1" {
		os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
	}
	goleak.VerifyTestMain(m)
}

func TestDetachReaderStopsAtCtrlBracket(t *testing.T) {
	r := detachReader{strings.NewReader("ls -la\r\x1dignored")}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ls -la\r" {
		t.Fatalf("got %q, want input up to the detach key", got)
	}
}

// testEnv isolates an in-process CLI run. Autostart is off: spawning
// os.Executable() here would run the test binary, not hive (end-to-end tests
// use the HIVE_CLI_TEST_EXEC re-exec instead).
func testEnv(t *testing.T) func(string) string {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfg, []byte("[daemon]\nautostart = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HIVE_HOME": filepath.Join(home, "hive"), "HIVE_CONFIG": cfg}
	return func(k string) string { return env[k] }
}

func newTestApp(t *testing.T) *app {
	t.Helper()
	a, err := newApp(testEnv(t), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCommandNamesAndAliasesResolve(t *testing.T) {
	root := newRootCmd(newTestApp(t))
	root.InitDefaultCompletionCmd()
	for path, want := range map[string]string{
		"tui":                   "ui",
		"env ls":                "list",
		"environment rm":        "remove",
		"ps start":              "start",
		"process logs":          "logs",
		"terminal attach":       "attach",
		"daemon install":        "install",
		"config validate":       "validate",
		"completion zsh":        "zsh",
		"version":               "version",
		"daemon":                "daemon",
		"stop":                  "stop",
		"environment get":       "get",
		"ps ls":                 "list",
		"daemon logs":           "logs",
		"config init":           "init",
		"terminal input":        "input",
		"terminal resize":       "resize",
		"daemon restart":        "restart",
		"daemon uninstall":      "uninstall",
		"process get":           "get",
		"process stop":          "stop",
		"environment create":    "create",
		"config show":           "show",
		"config path":           "path",
		"config default":        "default",
		"config reset-keys":     "reset-keys",
		"daemon start":          "start",
		"daemon stop":           "stop",
		"daemon status":         "status",
		"demo":                  "demo",
		"ping":                  "ping",
		"status":                "status",
		"ui":                    "ui",
		"completion bash":       "bash",
		"completion fish":       "fish",
		"completion powershell": "powershell",
	} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil || cmd.Name() != want {
			t.Errorf("%q resolved to %v (%v), want %q", path, cmd.Name(), err, want)
		}
	}
}

func TestUsageErrorsExitWithCode2(t *testing.T) {
	getenv := testEnv(t)
	for _, args := range [][]string{
		{"nonsense"},
		{"env", "create"},
		{"ps", "start", "dev"},
		{"terminal", "resize", "abc", "0", "10"},
		{"status", "--no-such-flag"},
	} {
		var stderr bytes.Buffer
		if code := execute(args, io.Discard, &stderr, getenv); code != exitUsage {
			t.Errorf("hive %s: exit %d, want %d (stderr %q)", strings.Join(args, " "), code, exitUsage, stderr.String())
		}
	}
}
