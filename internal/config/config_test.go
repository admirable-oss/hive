package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/config"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRenderedDefaultsParseBackWithoutWarnings(t *testing.T) {
	cfg, warnings, err := config.Parse(config.Render(config.Defaults()))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if !reflect.DeepEqual(cfg, config.Defaults()) {
		t.Fatalf("round trip changed the config:\n got %+v\nwant %+v", cfg, config.Defaults())
	}
}

func TestRenderRoundTripsCustomValues(t *testing.T) {
	want := config.Defaults()
	want.Daemon.Autostart = false
	want.Daemon.ShutdownTimeout = 90 * time.Second
	want.Log.Level, want.Log.Format = "debug", "json"
	want.Process.StopGrace = 1500 * time.Millisecond
	want.Terminal.DefaultWidth = 120
	want.Terminal.Shell = "/bin/zsh -l"
	want.Git.RefreshInterval = 30 * time.Second
	want.Worktrees.Directory = "~/src/worktrees"
	cfg, warnings, err := config.Parse(config.Render(want))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("parse: %v, warnings %v", err, warnings)
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestParseOverridesOnlyGivenKeys(t *testing.T) {
	cfg, warnings, err := config.Parse([]byte(`
[log]
level = "WARNING"

[terminal]
scrollback_mb = 64
`))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("parse: %v, warnings %v", err, warnings)
	}
	want := config.Defaults()
	want.Log.Level = "warn"
	want.Terminal.ScrollbackMB = 64
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestParseWarnsAndKeepsDefaultsForBadValues(t *testing.T) {
	cfg, warnings, err := config.Parse([]byte(`
colour = "blue"

[daemon]
autostart = "yes"
shutdown_timeout = "forever"
unknown = 1

[log]
level = "loud"
max_size_mb = 0

[process]
stop_grace = "1h"

[terminal]
default_width = 1.5

[worktrees]
directory = "relative/dir"
`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, config.Defaults()) {
		t.Fatalf("invalid values must keep defaults, got %+v", cfg)
	}
	wantFragments := []string{
		`"colour"`,
		"daemon.autostart: must be a boolean",
		"daemon.shutdown_timeout: invalid duration",
		"unknown key daemon.unknown",
		"log.level: unknown log level",
		"log.max_size_mb: 0 is outside 1..1024",
		"process.stop_grace: 1h0m0s is outside",
		"terminal.default_width: must be an integer",
		"worktrees.directory: \"relative/dir\" must be absolute",
	}
	if len(warnings) != len(wantFragments) {
		t.Fatalf("got %d warnings, want %d:\n%s", len(warnings), len(wantFragments), strings.Join(warnings, "\n"))
	}
	for i, frag := range wantFragments {
		if !strings.Contains(warnings[i], frag) {
			t.Errorf("warning %d = %q, want it to mention %q", i, warnings[i], frag)
		}
	}
}

func TestParseRejectsNonTableSections(t *testing.T) {
	_, warnings, err := config.Parse([]byte(`daemon = 3`))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "must be a table") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestParseSyntaxErrorReportsPosition(t *testing.T) {
	_, _, err := config.Parse([]byte("[daemon]\nautostart = = true\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("want a syntax error naming line 2, got %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	cfg, warnings, err := config.Load(filepath.Join(dir, "missing.toml"))
	if err != nil || warnings != nil || !reflect.DeepEqual(cfg, config.Defaults()) {
		t.Fatalf("missing file should give defaults: %+v %v %v", cfg, warnings, err)
	}

	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[daemon]\nautostart = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = config.Load(path)
	if err != nil || cfg.Daemon.Autostart {
		t.Fatalf("got %+v, %v; want autostart disabled", cfg, err)
	}

	if err := os.WriteFile(path, []byte("[daemon"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("syntax errors should name the file, got %v", err)
	}
}

func TestResolvePath(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"explicit", map[string]string{"HIVE_CONFIG": "/etc/hive.toml", "XDG_CONFIG_HOME": "/x"}, "/etc/hive.toml"},
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/x"}, "/x/hive/config.toml"},
		{"home", nil, "/home/u/.config/hive/config.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := config.ResolvePath(env(tt.env), "/home/u"); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyEnv(t *testing.T) {
	cfg, warnings := config.ApplyEnv(config.Defaults(), func(k string) string {
		if k == config.EnvLogLevel {
			return "debug"
		}
		return ""
	})
	if cfg.Log.Level != "debug" || len(warnings) != 0 {
		t.Fatalf("got level %q, warnings %v", cfg.Log.Level, warnings)
	}
	cfg, warnings = config.ApplyEnv(config.Defaults(), func(string) string { return "shouty" })
	if cfg.Log.Level != "info" || len(warnings) != 1 {
		t.Fatalf("bad HIVE_LOG must warn and keep the level: %q %v", cfg.Log.Level, warnings)
	}
}

func TestLoggingConfig(t *testing.T) {
	cfg := config.Defaults()
	cfg.Log.Level, cfg.Log.MaxSizeMB = "error", 2
	lc := cfg.LoggingConfig("/tmp/d.log", true)
	if lc.Level != slog.LevelError || lc.MaxSizeBytes != 2<<20 || lc.Path != "/tmp/d.log" || !lc.Stderr {
		t.Fatalf("unexpected logging config %+v", lc)
	}
}

func TestDeprecatedKeysWarnButLoad(t *testing.T) {
	cfg, warnings, err := config.Parse([]byte("[terminal]\nhistory_kb = 64\n"))
	if err != nil || !reflect.DeepEqual(cfg, config.Defaults()) {
		t.Fatalf("parse: %+v, %v", cfg, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "terminal.history_kb: is no longer used") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestWorkspaceSettings(t *testing.T) {
	cfg := config.Defaults()
	if got := cfg.WorktreeDir("/home/me", "/home/me/.hive"); got != "/home/me/.hive/worktrees" {
		t.Errorf("default worktree dir = %q", got)
	}
	cfg.Worktrees.Directory = "~/wt"
	if got := cfg.WorktreeDir("/home/me", "/x"); got != "/home/me/wt" {
		t.Errorf("~ worktree dir = %q", got)
	}
	if cfg.ShellArgv() != nil {
		t.Error("the default shell is $SHELL")
	}
	cfg.Terminal.Shell = " fish  --login "
	if got := cfg.ShellArgv(); len(got) != 2 || got[0] != "fish" || got[1] != "--login" {
		t.Errorf("shell argv = %q", got)
	}
}

func TestUIThemeAndKeys(t *testing.T) {
	cfg, warnings, err := config.Parse([]byte(`
[ui]
sidebar = false
sidebar_width = 40
mouse = false
clipboard = "OSC52"

[theme]
name = "Nord"

[theme.custom]
base = "gruvbox"
accent = "#ff79c6"

[keys]
prefix_keys = ["ctrl+a", "ctrl+b"]

[keys.prefix]
split_right = "|"
zoom = []

[keys.terminal]
focus_left = ["alt+h"]
`))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("parse: %v, warnings %v", err, warnings)
	}
	want := config.Defaults()
	want.UI = config.UI{Sidebar: false, SidebarWidth: 40, Mouse: false, Clipboard: "osc52"}
	want.Theme = config.Theme{Name: "nord", Custom: map[string]string{"base": "gruvbox", "accent": "#ff79c6"}}
	want.Keys = config.Keys{
		Prefix: []string{"ctrl+a", "ctrl+b"},
		Modes: map[string]map[string][]string{
			"prefix":   {"split_right": {"|"}, "zoom": {}},
			"terminal": {"focus_left": {"alt+h"}},
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v\nwant %+v", cfg, want)
	}
	// And back: custom values survive a render.
	again, warnings, err := config.Parse(config.Render(cfg))
	if err != nil || len(warnings) != 0 || !reflect.DeepEqual(again, cfg) {
		t.Fatalf("round trip: %v %v\n%+v", err, warnings, again)
	}
}

func TestUIThemeAndKeysWarn(t *testing.T) {
	cfg, warnings, err := config.Parse([]byte(`
[ui]
sidebar_width = 3
clipboard = "carrier-pigeon"

[keys]
prefix = "ctrl+a"
prefix_keys = []

[keys.copy]
copy_yank = 3
copy_exit = "q"

[keys.chaos]
x = "y"
`))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		"ui.sidebar_width: 3 is outside 16..120",
		`ui.clipboard: "carrier-pigeon" is not one of auto, osc52, local, off`,
		"keys.prefix: is the table of prefix-mode bindings; set the prefix key itself with keys.prefix_keys",
		"keys.prefix_keys: needs at least one key",
		"keys.copy: copy_yank must be a key or a list of keys, got int64 (ignored)",
		"unknown key keys.chaos ignored",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	if cfg.UI != config.Defaults().UI || !reflect.DeepEqual(cfg.Keys.Prefix, []string{config.DefaultPrefix}) {
		t.Errorf("bad values keep the defaults: %+v %+v", cfg.UI, cfg.Keys.Prefix)
	}
	if got := cfg.Keys.Modes["copy"]; !reflect.DeepEqual(got, map[string][]string{"copy_exit": {"q"}}) {
		t.Errorf("the valid binding beside a bad one still applies: %v", got)
	}
}

func TestResetKeys(t *testing.T) {
	doc := `# my config
[log]
level = "debug"

[keys] # mine
prefix_keys = ["ctrl+a"]

[keys.prefix]
zoom = [
  "Z",
]

[ui]
mouse = false

[ "keys" . "terminal" ]
focus_left = "alt+h"
`
	out, found := config.ResetKeys([]byte(doc))
	if !found {
		t.Fatal("the document had key settings")
	}
	cfg, warnings, err := config.Parse(out)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("parse: %v %v\n%s", err, warnings, out)
	}
	if !reflect.DeepEqual(cfg.Keys, config.Defaults().Keys) {
		t.Errorf("keys = %+v, want the defaults", cfg.Keys)
	}
	if cfg.Log.Level != "debug" || cfg.UI.Mouse {
		t.Errorf("other settings are kept: %+v %+v", cfg.Log, cfg.UI)
	}
	if !strings.HasPrefix(string(out), "# my config\n") || !strings.Contains(string(out), "[keys]\n# The prefix key(s)") {
		t.Errorf("comments are kept and the documented section added:\n%s", out)
	}
	if _, found := config.ResetKeys([]byte("[log]\nlevel = \"info\"\n")); found {
		t.Error("nothing to reset")
	}
}
