package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/plugin"
)

// TestMain doubles as the hive binary: with HIVE_CLI_TEST_EXEC=1, or when a
// copy of it is called hive, the test executable runs the CLI instead of
// tests. End-to-end tests exec it, and the CLI in turn spawns `hive daemon`
// the way a user's shell would; plugins and agents call it as $HIVE_BIN.
func TestMain(m *testing.M) {
	if os.Getenv("HIVE_CLI_TEST_EXEC") == "1" || filepath.Base(os.Args[0]) == "hive" {
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
		"plugin validate":       "validate",
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

func TestPluginValidateCommand(t *testing.T) {
	dir := t.TempDir()
	manifest := `id = "sample-plugin"
version = "1.2.3"

[[actions]]
id = "open"
command = ["echo", "ready"]
`
	if err := os.WriteFile(filepath.Join(dir, "hive-plugin.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"plugin", "validate", dir}, &stdout, &stderr, testEnv(t)); code != 0 {
		t.Fatalf("validate exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "valid sample-plugin 1.2.3") {
		t.Fatalf("unexpected validation output: %q", stdout.String())
	}
}

func TestPluginInitCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test-plugin")
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"plugin", "init", "test-plugin", dir}, &stdout, &stderr, testEnv(t)); code != 0 {
		t.Fatalf("init exit=%d stderr=%q", code, stderr.String())
	}
	if _, err := plugin.LoadManifest(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "hello.sh"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("scaffold missing %s: %v", name, err)
		}
	}
	if code := execute([]string{"plugin", "init", "test-plugin", dir}, &stdout, &stderr, testEnv(t)); code == 0 {
		t.Fatal("init overwrote an existing directory")
	}
}

func TestPluginUpdateRejectsLocalInstallWithoutRegistry(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "config.toml")
	if err := os.WriteFile(config, []byte("[daemon]\nautostart = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"HIVE_HOME":                filepath.Join(home, "hive"),
		"HIVE_CONFIG":              config,
		"HIVE_PLUGIN_REGISTRY_URL": "file:///unavailable/registry.json",
	}
	getenv := func(key string) string { return values[key] }
	a, err := newApp(getenv, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pluginDir := t.TempDir()
	manifest := `id = "local-plugin"
version = "1.0.0"

[[actions]]
id = "open"
command = ["echo", "ready"]
`
	if err := os.WriteFile(filepath.Join(pluginDir, plugin.ManifestFile), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.plugins().Link(context.Background(), pluginDir, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := execute([]string{"plugin", "update", "local-plugin"}, io.Discard, &stderr, getenv); code == 0 || !strings.Contains(stderr.String(), "not registry-installed") {
		t.Fatalf("local plugin update exit=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(a.plugins().Dir("local-plugin"), plugin.ManifestFile)); err != nil {
		t.Fatalf("plugin changed after refused update: %v", err)
	}
}

func TestPluginUpdateRegistryFailureLeavesInstalledReleaseUntouched(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "config.toml")
	if err := os.WriteFile(config, []byte("[daemon]\nautostart = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"HIVE_HOME":                filepath.Join(home, "hive"),
		"HIVE_CONFIG":              config,
		"HIVE_PLUGIN_REGISTRY_URL": "file:///unavailable/registry.json",
	}
	getenv := func(key string) string { return values[key] }
	a, err := newApp(getenv, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	store := a.plugins()
	id := "catalog-plugin"
	dir := store.Dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `id = "catalog-plugin"
version = "1.0.0"

[[actions]]
id = "open"
command = ["echo", "ready"]
`
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, []byte("known-good"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed := plugin.Installed{ID: id, Source: "https://example.test/catalog-plugin", Ref: "v1.0.0", Commit: strings.Repeat("a", 40), Version: "1.0.0", Checksum: "sha256:" + strings.Repeat("b", 64), Registry: true, Enabled: true, InstalledAt: time.Now().UTC()}
	state, err := json.Marshal(map[string]any{"schema": 1, "plugins": []plugin.Installed{installed}})
	if err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(filepath.Dir(dir), "registry.json")
	if err := os.WriteFile(registryPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := execute([]string{"plugin", "update", id}, io.Discard, &stderr, getenv); code == 0 || !strings.Contains(stderr.String(), "invalid plugin registry URL") {
		t.Fatalf("update exit=%d stderr=%q", code, stderr.String())
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "known-good" {
		t.Fatalf("known-good plugin changed: %q (%v)", got, err)
	}
	if got, err := os.ReadFile(registryPath); err != nil || !bytes.Equal(got, state) {
		t.Fatalf("installation record changed: %s (%v)", got, err)
	}
}

func TestPluginConfirmationRequiresExplicitYesWithoutTerminal(t *testing.T) {
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	defer func() { os.Stdin = oldStdin; _ = reader.Close(); _ = writer.Close() }()
	a := newTestApp(t)
	if a.confirm("install plugin") {
		t.Fatal("non-interactive confirmation unexpectedly accepted")
	}
	a.yes = true
	if !a.confirm("install plugin") {
		t.Fatal("--yes did not accept the non-interactive confirmation")
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
