//go:build !windows

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }


func TestManifest(t *testing.T) {
	m, err := ParseManifest([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := m.Action("open"); !ok || a.Timeout() != 10*time.Second || m.Events[0].Timeout() != DefaultTimeout {
		t.Fatalf("action = %+v", a)
	}
	if !m.Events[0].Matches("worktree.created") || m.Events[0].Matches("worktree.removed") {
		t.Fatal("exact event types")
	}
	if !(Event{On: "agent."}).Matches("agent.state") || (Event{On: "agent."}).Matches("agents.x") {
		t.Fatal("prefixes")
	}
	p := m.Preview("https://example.com/pr-status")
	for _, want := range []string{"Plugin pr-status 0.1.0 (PR status)", "restarted when it stops: ./watch.sh", "on worktree.created: ./created.sh", "action open (Open the PR): ./open.sh --web"} {
		if !strings.Contains(p, want) {
			t.Errorf("preview lacks %q:\n%s", want, p)
		}
	}
	if err := m.Supported(context.Background(), "v0.9.3"); err == nil || !strings.Contains(err.Error(), "0.10.0") {
		t.Errorf("too old a hive: %v", err)
	}
	for _, v := range []string{"v0.10.0", "v1.2.0", "dev", "v0.0.0-20261010-abc+dirty"} {
		if err := m.Supported(context.Background(), v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	for _, bad := range []string{
		`id = "X"` + "\nversion = \"1.0.0\"\n[[startup]]\ncommand=[\"x\"]",
		`id = "x"` + "\nversion = \"1\"\n[[startup]]\ncommand=[\"x\"]",
		`id = "x"` + "\nversion = \"1.0.0\"",
		`id = "x"` + "\nversion = \"1.0.0\"\n[[events]]\non = \"Bad Type\"\ncommand=[\"x\"]",
		`id = "x"` + "\nversion = \"1.0.0\"\nunknown = 1\n[[startup]]\ncommand=[\"x\"]",
		`id = "x"` + "\nversion = \"1.0.0\"\n[[actions]]\nid=\"a\"\ncommand=[\"x\"]\n[[actions]]\nid=\"a\"\ncommand=[\"y\"]",
	} {
		if _, err := ParseManifest([]byte(bad)); !errors.Is(err, ErrInvalidManifest) {
			t.Errorf("accepted:\n%s\n(%v)", bad, err)
		}
	}
}

func TestManifestConfigurationAndMigrationValidation(t *testing.T) {
	valid := `id = "config-demo"
version = "1.0.0"
max_hive_version = "2.0.0"
data_directory = "state"
[configuration]
version = 2
directory = "profile"
[[configuration.settings]]
name = "endpoint"
type = "string"
required = true
default = "https://example.test"
[[migrations]]
from = 1
to = 2
command = ["./migrate.sh"]
[[actions]]
id = "run"
command = ["./run.sh"]
`
	m, err := ParseManifest([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if m.MaxHiveVersion != "2.0.0" || m.Configuration.Version != 2 || m.Migrations[0].From != 1 {
		t.Fatalf("manifest contract = %+v", m)
	}
	if err := m.Supported(context.Background(), "2.1.0"); err == nil || !strings.Contains(err.Error(), "supports hive through") {
		t.Fatalf("max version was not enforced: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(valid, `data_directory = "state"`, `data_directory = "../outside"`, 1),
		strings.Replace(valid, `directory = "profile"`, `directory = "../../outside"`, 1),
		strings.Replace(valid, `type = "string"`, `type = "secret"`, 1),
		strings.Replace(valid, "to = 2", "to = 3", 1),
	} {
		if _, err := ParseManifest([]byte(bad)); !errors.Is(err, ErrInvalidManifest) {
			t.Errorf("accepted invalid declaration:\n%s\n%v", bad, err)
		}
	}
}

func TestPrepareConfigurationDefaultsAndMigration(t *testing.T) {
	manifest := `id = "settings"
version = "1.0.0"
[configuration]
version = 2
[[configuration.settings]]
name = "endpoint"
type = "string"
required = true
[[migrations]]
from = 1
to = 2
command = ["./migrate.sh"]
[[actions]]
id = "run"
command = ["./run.sh"]
`
	dir := writePlugin(t, manifest, map[string]string{
		"migrate.sh": "#!/bin/sh\nprintf '{\"schema_version\":1,\"values\":{\"endpoint\":\"https://migrated.test\"}}' > \"$HIVE_PLUGIN_CONFIG\"\n",
	}, false)
	store := NewStore(filepath.Join(t.TempDir(), "plugins"))
	if _, _, err := store.Link(context.Background(), dir, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(store.ConfigDir("settings"), pluginConfigFile)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"schema_version":1,"values":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	host := NewHost(HostConfig{Store: store, Version: "1.0.0"})
	loaded := &loaded{Plugin: Plugin{Installed: Installed{ID: "settings"}, Dir: dir, Manifest: m}}
	if _, err := host.prepareConfiguration(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	var got ConfigurationFile
	if err := jsonfile.Read(configPath, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 2 || got.Values["endpoint"] != "https://migrated.test" {
		t.Fatalf("migrated settings = %+v", got)
	}
}

func TestPrepareConfigurationWritesTypedDefaults(t *testing.T) {
	manifest := `id = "defaults"
version = "1.0.0"
[configuration]
version = 1
[[configuration.settings]]
name = "enabled"
type = "boolean"
default = true
[[configuration.settings]]
name = "endpoint"
type = "string"
default = "https://example.test"
required = true
[[actions]]
id = "run"
command = ["./run.sh"]
`
	dir := writePlugin(t, manifest, map[string]string{}, false)
	store := NewStore(filepath.Join(t.TempDir(), "plugins"))
	if _, _, err := store.Link(context.Background(), dir, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	host := NewHost(HostConfig{Store: store, Version: "1.0.0"})
	l := &loaded{Plugin: Plugin{Installed: Installed{ID: "defaults"}, Dir: dir, Manifest: m}}
	if _, err := host.prepareConfiguration(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	var got ConfigurationFile
	if err := jsonfile.Read(filepath.Join(store.ConfigDir("defaults"), pluginConfigFile), &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.Values["enabled"] != true || got.Values["endpoint"] != "https://example.test" {
		t.Fatalf("default settings = %+v", got)
	}
	info, err := os.Stat(filepath.Join(store.ConfigDir("defaults"), pluginConfigFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file permissions = %v, %v", info, err)
	}
}

// writePlugin makes a plugin directory; with repo, a git repository.

func TestStoreInstallLinkAndUninstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	src := writePlugin(t, sample, map[string]string{"watch.sh": "#!/bin/sh\n"}, true)
	s := NewStore(filepath.Join(t.TempDir(), "plugins"))
	ctx := context.Background()

	f, err := s.Fetch(ctx, src, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Preview(), "from "+src+" @ ") || len(f.Commit) != 40 {
		t.Fatalf("preview:\n%s", f.Preview())
	}
	if _, err := s.Install(ctx, f, "v0.9.0"); err == nil {
		t.Fatal("installed a plugin that needs a newer hive")
	}
	f, _ = s.Fetch(ctx, src, "")
	in, err := s.Install(ctx, f, "v0.10.0")
	if err != nil || !in.Enabled || in.Commit == "" {
		t.Fatalf("install = %+v, %v", in, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir("pr-status"), "watch.sh")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := filepath.Glob(filepath.Join(s.dir, ".fetch-*")); len(entries) != 0 {
		t.Fatalf("clones left behind: %v", entries)
	}
	f, _ = s.Fetch(ctx, src, "")
	if _, err := s.Install(ctx, f, "v0.10.0"); !errors.Is(err, ErrExists) {
		t.Fatalf("second install: %v", err)
	}
	if _, err := s.Fetch(ctx, t.TempDir(), ""); err == nil {
		t.Fatal("fetched something that is not a repository")
	}

	dev := writePlugin(t, strings.Replace(sample, `id = "pr-status"`, `id = "dev-tool"`, 1), map[string]string{}, false)
	if _, _, err := s.Link(ctx, dev, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled("dev-tool", false); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "v1.0.0")
	if err != nil || len(list) != 2 || list[0].ID != "dev-tool" || list[0].Enabled || !list[0].Linked || list[1].Problem != "" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.Uninstall("dev-tool"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dev, ManifestFile)); err != nil {
		t.Fatal("uninstalling a linked plugin removed its sources")
	}
	if err := s.Uninstall("pr-status"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx, ""); len(list) != 0 {
		t.Fatalf("left after uninstall: %+v", list)
	}
}

func TestStoreUpdateRollbackAndRecovery(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	ctx := context.Background()
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "plugins"))
	firstSrc := writePlugin(t, strings.Replace(sample, `version = "0.1.0"`, `version = "1.0.0"`, 1), map[string]string{"marker": "one"}, true)
	f, err := s.Fetch(ctx, firstSrc, "")
	if err != nil {
		t.Fatal(err)
	}
	f.Ref, f.Version, f.Checksum, f.Registry, f.checksumVerified = "v1.0.0", "1.0.0", "sha256:"+strings.Repeat("a", 64), true, true
	if _, err := s.Install(ctx, f, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	data := s.DataDir("pr-status")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "keep"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondSrc := writePlugin(t, strings.Replace(sample, `version = "0.1.0"`, `version = "2.0.0"`, 1), map[string]string{"marker": "two"}, true)
	f, err = s.Fetch(ctx, secondSrc, "")
	if err != nil {
		t.Fatal(err)
	}
	f.Ref, f.Version, f.Checksum, f.Registry, f.checksumVerified = "v2.0.0", "2.0.0", "sha256:"+strings.Repeat("b", 64), true, true
	updated, err := s.Update(ctx, f, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != "2.0.0" || updated.PreviousVersion != "1.0.0" {
		t.Fatalf("updated metadata: %+v", updated)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Dir("pr-status"), "marker")); string(got) != "two" {
		t.Fatalf("active marker = %q", got)
	}
	rolled, err := s.Rollback("pr-status")
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Version != "1.0.0" || rolled.PreviousVersion != "2.0.0" {
		t.Fatalf("rollback metadata: %+v", rolled)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Dir("pr-status"), "marker")); string(got) != "one" {
		t.Fatalf("rolled marker = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(data, "keep")); string(got) != "data" {
		t.Fatalf("plugin data changed: %q", got)
	}

	// Simulate a crash after moving the active directory to its release backup.
	list, err := s.loadLocked()
	if err != nil {
		t.Fatal(err)
	}
	oldDir := s.Dir("pr-status")
	next := list[0]
	next.PreviousVersion, next.PreviousCommit = next.Version, next.Commit
	next.Version, next.Commit = "3.0.0", strings.Repeat("f", 40)
	backup := filepath.Join(s.VersionsDir("pr-status"), safeReleaseDir(next.PreviousVersion, next.PreviousCommit))
	if err := os.Rename(oldDir, backup); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(s.dir, ".fetch-crash", "src")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "candidate"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stage, oldDir); err != nil {
		t.Fatal(err)
	}
	tx := storeTransaction{Schema: 1, PluginID: "pr-status", Operation: "update", Backup: backup, Stage: stage, Next: next}
	if err := jsonfile.Write(filepath.Join(s.dir, ".transaction.json"), tx); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Dir("pr-status"), "marker")); string(got) != "one" {
		t.Fatalf("recovery marker = %q", got)
	}

	// Simulate an interrupted rollback after the outgoing release and target have swapped.
	list, err = s.loadLocked()
	if err != nil {
		t.Fatal(err)
	}
	cur := list[0]
	rollbackTarget := filepath.Join(s.VersionsDir(cur.ID), safeReleaseDir(cur.PreviousVersion, cur.PreviousCommit))
	rollbackStage := filepath.Join(s.VersionsDir(cur.ID), safeReleaseDir(cur.Version, cur.Commit))
	nextRollback := cur
	nextRollback.Version, nextRollback.Commit, nextRollback.Checksum = cur.PreviousVersion, cur.PreviousCommit, cur.PreviousChecksum
	nextRollback.PreviousVersion, nextRollback.PreviousCommit, nextRollback.PreviousChecksum = cur.Version, cur.Commit, cur.Checksum
	rollbackTx := storeTransaction{Schema: 1, PluginID: cur.ID, Operation: "rollback", Backup: rollbackTarget, Stage: rollbackStage, KeepStage: true, Next: nextRollback}
	if err := jsonfile.Write(filepath.Join(s.dir, ".transaction.json"), rollbackTx); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.Dir(cur.ID), rollbackStage); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rollbackTarget, s.Dir(cur.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Dir("pr-status"), "marker")); string(got) != "one" {
		t.Fatalf("rollback recovery marker = %q", got)
	}
}

func TestHostRunsStartupEventsAndActions(t *testing.T) {
	manifest := `
id = "probe"
version = "1.0.0"

[[startup]]
command = ["./startup.sh"]

[[actions]]
id = "hello"
command = ["./action.sh", "world"]

[[actions]]
id = "fail"
command = ["sh", "-c", "echo broken >&2; exit 3"]

[[events]]
on = "worktree."
command = ["./event.sh"]
`
	dir := writePlugin(t, manifest, map[string]string{
		// Startup writes its context and environment, then stays up.
		"startup.sh": "#!/bin/sh\ncat > \"$HIVE_PLUGIN_DATA/startup.json\"\necho \"$HIVE_PLUGIN_ID $HIVE_PLUGIN_VERSION $X_FROM_HIVE\" > \"$HIVE_PLUGIN_DATA/env\"\nprintf '%s\\n%s' \"$HIVE_PLUGIN_CONFIG\" \"$HIVE_PLUGIN_CONFIG_DIR\" > \"$HIVE_PLUGIN_DATA/config-paths\"\nwhile :; do sleep 1; done\n",
		"action.sh":  "#!/bin/sh\necho \"hello $1 from $(pwd)\"\ncat \"$HIVE_PLUGIN_CONTEXT\"\n",
		"event.sh":   "#!/bin/sh\ncat > \"$HIVE_PLUGIN_DATA/event.json\"\n",
	}, false)
	root := filepath.Join(t.TempDir(), "plugins")
	s := NewStore(root)
	if _, _, err := s.Link(context.Background(), dir, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	h := NewHost(HostConfig{
		Store: s, Version: "v1.0.0", Session: "work", LogDir: filepath.Join(root, "logs"),
		Env: func(vars map[string]string) []string {
			env := append(os.Environ(), "X_FROM_HIVE=yes")
			for k, v := range vars {
				env = append(env, k+"="+v)
			}
			return env
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	data := s.DataDir("probe")
	waitFile := func(name string) []byte {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if b, err := os.ReadFile(filepath.Join(data, name)); err == nil && len(b) > 0 {
				return b
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never written", name)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	var c Context
	if err := json.Unmarshal(waitFile("startup.json"), &c); err != nil || c.Trigger != "startup" || c.Plugin != "probe" || c.Session != "work" {
		t.Fatalf("startup context = %+v, %v", c, err)
	}
	if env := strings.TrimSpace(string(waitFile("env"))); env != "probe 1.0.0 yes" {
		t.Fatalf("startup env = %q", env)
	}
	paths := strings.Split(string(waitFile("config-paths")), "\n")
	if len(paths) != 2 || paths[0] != filepath.Join(s.ConfigDir("probe"), pluginConfigFile) || paths[1] != s.ConfigDir("probe") {
		t.Fatalf("plugin configuration paths = %q", paths)
	}
	if st := h.Status(); len(st) != 1 || st[0].Running != 1 {
		t.Fatalf("status = %+v", st)
	}

	resolved, _ := filepath.EvalSymlinks(dir)
	res, err := h.Action(context.Background(), "probe", "hello", Context{Environment: "api", Pane: "p1"})
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Output, "hello world from "+resolved) || !strings.Contains(res.Output, `"pane":"p1"`) || !strings.Contains(res.Output, `"trigger":"action"`) {
		t.Fatalf("action = %+v, %v", res, err)
	}
	if res, err := h.Action(context.Background(), "probe", "fail", Context{}); err != nil || res.ExitCode != 3 || !strings.Contains(res.Output, "broken") {
		t.Fatalf("failing action = %+v, %v", res, err)
	}
	if _, err := h.Action(context.Background(), "probe", "nope", Context{}); !errors.Is(err, ErrNoAction) {
		t.Fatalf("unknown action: %v", err)
	}
	if _, err := h.Action(context.Background(), "ghost", "x", Context{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown plugin: %v", err)
	}

	h.Event("environment.created", json.RawMessage(`{}`)) // no handler
	h.Event("worktree.created", json.RawMessage(`{"environment":{"id":"fix-login"}}`))
	if err := json.Unmarshal(waitFile("event.json"), &c); err != nil || c.Trigger != "event" || c.Event.Type != "worktree.created" || c.Environment != "fix-login" {
		t.Fatalf("event context = %+v, %v", c, err)
	}

	// Disabled and reloaded: its startup command stops.
	if _, err := s.SetEnabled("probe", false); err != nil {
		t.Fatal(err)
	}
	if st := h.Reload(); len(st) != 0 {
		t.Fatalf("after disabling: %+v", st)
	}
	log, _ := os.ReadFile(filepath.Join(root, "logs", "probe.log"))
	if !strings.Contains(string(log), "hello world") {
		t.Fatalf("plugin log:\n%s", log)
	}
}

// TestReferencePlugins keeps the shipped examples valid.
func TestReferencePlugins(t *testing.T) {
	dirs, _ := filepath.Glob("../../examples/plugins/*")
	if len(dirs) < 2 {
		t.Fatalf("reference plugins missing: %v", dirs)
	}
	for _, d := range dirs {
		m, err := LoadManifest(d)
		if err != nil {
			t.Errorf("%s: %v", d, err)
			continue
		}
		if err := m.Supported(context.Background(), "v1.0.2"); err != nil {
			t.Errorf("%s: %v", d, err)
		}
		if m.Legacy() {
			t.Errorf("%s: reference plugins use plugin API %d with declared permissions", d, APIVersion)
		}
	}
}
