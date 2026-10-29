//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/plugin"
)

// writeLocalPlugin writes a plugin directory for `hive plugin link`.
func writeLocalPlugin(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files[plugin.ManifestFile] = manifest
	for name, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const probeManifest = `api = 1
id = "probe"
version = "1.0.0"

[permissions]
required = ["hive.read"]
optional = ["hive.write"]

[[actions]]
id = "sneak"
title = "Try to create an environment"
command = ["sh", "-c", "\"$HIVE_BIN\" env create sneaky --cwd \"$HIVE_PLUGIN_DATA\" 2>&1; exit 0"]

[[actions]]
id = "greet"
command = ["sh", "-c", "cat \"$HIVE_PLUGIN_CONFIG\""]

[configuration]
version = 1
[[configuration.settings]]
name = "greeting"
type = "string"
default = "hello"
[[configuration.settings]]
name = "count"
type = "integer"
default = 1
`

const crashyManifest = `api = 1
id = "crashy"
version = "1.0.0"
[[startup]]
command = ["sh", "-c", "exit 7"]
restart = true
`

// TestM12PluginRuntimeSurfaces is M12's acceptance, through the CLI and a
// real daemon: the reference plugins contribute sidebar cards, a status
// indicator and contextual actions through documented interfaces; a plugin
// reaches Hive only within its capabilities; a crashing plugin fails alone
// without taking the daemon down; and health, settings and capabilities
// can be inspected, changed and backed up.
func TestM12PluginRuntimeSurfaces(t *testing.T) {
	c := newCLI(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	c.exe = filepath.Join(c.home, "bin", "hive")
	copyFile(t, os.Args[0], c.exe)
	bin := filepath.Join(c.home, "fake-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.cfg, []byte("[plugins]\nrestart_limit = 1\nisolation = \"standard\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "PR_STATUS_INTERVAL=1", "GIT_CONFIG_GLOBAL="+os.DevNull)

	// Installing shows the permissions a plugin asks for, with reasons.
	prStatus := pluginRepo(t, "pr-status")
	_, errOut, _ := c.run("plugin", "install", prStatus)
	for _, want := range []string{"plugin API: 1", "requires hive.write", "put the PR on each environment's sidebar token", "sidebar card \"Pull request\""} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("install preview lacks %q:\n%s", want, errOut)
		}
	}
	c.must("daemon", "start")
	c.must("plugin", "install", prStatus, "--yes")
	c.must("plugin", "install", pluginRepo(t, "spawn-reviewer"), "--yes")

	status := func(id string) pluginRow {
		var rows []pluginRow
		decode(t, c.must("--json", "plugin", "list"), &rows)
		for _, r := range rows {
			if r.ID == id {
				return r
			}
		}
		return pluginRow{}
	}
	// pr-status says it is ready once its first pass is done.
	c.waitForUpTo(30*time.Second, "pr-status to be ready", func() bool {
		r := status("pr-status")
		return r.Health != nil && r.Health.State == plugin.StateReady
	})
	if g := status("pr-status").Grant.Effective; strings.Join(g, ",") != "hive.read,hive.write,network,process.exec" {
		t.Fatalf("pr-status holds %v", g)
	}

	// A worktree with a pull request: its card and the tab-bar count.
	repo := filepath.Join(c.home, "app")
	gitRun(t, "", "init", "-q", "-b", "main", repo)
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	c.must("worktree", "create", "feature/login", "--repo", repo)
	var contrib plugin.Contributions
	c.waitForUpTo(30*time.Second, "the PR card and status", func() bool {
		decode(t, c.must("--json", "plugin", "contributions"), &contrib)
		return len(contrib.Cards) == 1 && len(contrib.Status) == 1
	})
	card := contrib.Cards[0]
	if card.Plugin != "pr-status" || card.Environment != "feature-login" || card.Title != "Pull request" ||
		len(card.Rows) != 1 || card.Rows[0].Text != "#12 open ✓" || card.Rows[0].Tone != "success" || card.Rows[0].Label != "feature/login" {
		t.Fatalf("card = %+v", card)
	}
	if s := contrib.Status[0]; s.Indicator != "open-prs" || s.Text != "1 open PR(s)" {
		t.Fatalf("status = %+v", s)
	}
	keys := map[string]plugin.Contribution{}
	for _, d := range contrib.Declared {
		keys[d.Key] = d
	}
	if a := keys["pr-status:action:open"]; !slices.Contains(a.Contexts, "environment") || !a.Available {
		t.Fatalf("contextual action = %+v (all: %v)", a, keys)
	}
	for _, k := range []string{"pr-status:palette:refresh", "pr-status:link:pulls", "spawn-reviewer:action:review", "pr-status:status:open-prs", "pr-status:card:pr"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("contribution %s is not listed", k)
		}
	}

	// Doctor finds nothing wrong with them.
	if out, errOut, code := c.run("plugin", "doctor"); code != 0 || strings.Contains(out, "✗") {
		t.Fatalf("doctor: exit %d\n%s%s", code, out, errOut)
	}

	// Revoking a required capability stops the plugin, says why, and
	// doctor fails; granting it back brings it back.
	c.must("plugin", "revoke", "pr-status", "hive.write")
	c.waitFor("pr-status to fail", func() bool {
		r := status("pr-status")
		return r.Health != nil && r.Health.State == plugin.StateFailed && strings.Contains(r.LastError, "not granted")
	})
	if out, _, code := c.run("plugin", "doctor", "pr-status"); code == 0 || !strings.Contains(out, "hive plugin grant pr-status hive.write") {
		t.Fatalf("doctor after revoke: exit %d\n%s", code, out)
	}
	c.must("plugin", "grant", "pr-status", "hive.write")
	c.waitForUpTo(30*time.Second, "pr-status to recover", func() bool {
		r := status("pr-status")
		return r.Health != nil && r.Health.State == plugin.StateReady
	})

	// A plugin's own hive commands go through its socket: a write it was
	// not granted is refused, and nothing changes.
	c.must("plugin", "link", writeLocalPlugin(t, probeManifest, map[string]string{}))
	out := c.must("plugin", "run", "probe", "sneak")
	if !strings.Contains(out, "may not call environment.create") || !strings.Contains(out, "hive.write") {
		t.Fatalf("an ungranted write from a plugin:\n%s", out)
	}
	if envs := c.must("env", "list"); strings.Contains(envs, "sneaky") {
		t.Fatalf("the refused call created an environment:\n%s", envs)
	}
	if _, errOut, code := c.run("plugin", "grant", "probe", "hive.admin"); code == 0 || !strings.Contains(errOut, "does not ask for hive.admin") {
		t.Fatalf("granting an undeclared capability: exit %d %s", code, errOut)
	}

	// Settings: typed by the schema, and the plugin sees them.
	c.must("plugin", "config", "probe", "--set", "greeting=hi there", "--set", "count=3")
	if _, errOut, code := c.run("plugin", "config", "probe", "--set", "count=many"); code == 0 || !strings.Contains(errOut, "not a whole number") {
		t.Fatalf("a mistyped setting: exit %d %s", code, errOut)
	}
	if out := c.must("plugin", "run", "probe", "greet"); !strings.Contains(out, `"greeting": "hi there"`) || !strings.Contains(out, `"count": 3`) {
		t.Fatalf("settings the plugin sees: %s", out)
	}

	// Backup and restore: settings and grants come back.
	archive := filepath.Join(c.home, "plugins-backup.tar.gz")
	c.must("plugin", "grant", "probe", "hive.write")
	c.must("plugin", "export", "-o", archive, "--data")
	c.must("plugin", "config", "probe", "--unset", "greeting")
	c.must("plugin", "revoke", "probe", "hive.write")
	c.must("plugin", "import", archive)
	if out := c.must("plugin", "config", "probe"); !strings.Contains(out, "hi there") {
		t.Fatalf("settings after import:\n%s", out)
	}
	c.waitFor("probe's grant to come back", func() bool { return slices.Contains(status("probe").Grant.Effective, "hive.write") })

	// A plugin that keeps crashing fails alone: it stops being restarted,
	// the daemon and the other plugins carry on.
	c.must("plugin", "link", writeLocalPlugin(t, crashyManifest, map[string]string{}))
	c.waitForUpTo(30*time.Second, "crashy to be given up on", func() bool {
		r := status("crashy")
		return r.Health != nil && r.Health.State == plugin.StateFailed && strings.Contains(r.LastError, "no longer restarted")
	})
	c.must("ping")
	if r := status("pr-status"); r.Health == nil || r.Health.State != plugin.StateReady {
		t.Fatalf("pr-status after crashy failed: %+v", r.Health)
	}
	if out := c.must("plugin", "info", "crashy"); !strings.Contains(out, "health: failed") {
		t.Fatalf("info crashy:\n%s", out)
	}

	// Capability discovery, from the CLI.
	var d plugin.Discovery
	decode(t, c.must("--json", "plugin", "capabilities"), &d)
	if d.API != plugin.APIVersion || len(d.Capabilities) != len(plugin.CapabilitiesFor(plugin.APIVersion)) || !slices.Contains(d.Events, "worktree.created") {
		t.Fatalf("capabilities = %+v", d)
	}
}
