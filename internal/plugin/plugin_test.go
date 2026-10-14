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
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

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

// writePlugin makes a plugin directory; with repo, a git repository.
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
		"startup.sh": "#!/bin/sh\ncat > \"$HIVE_PLUGIN_DATA/startup.json\"\necho \"$HIVE_PLUGIN_ID $HIVE_PLUGIN_VERSION $X_FROM_HIVE\" > \"$HIVE_PLUGIN_DATA/env\"\nwhile :; do sleep 1; done\n",
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
		if err := m.Supported(context.Background(), "v0.10.0"); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}
