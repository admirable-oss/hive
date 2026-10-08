package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
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
	if cfg != config.Defaults() {
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
	cfg, warnings, err := config.Parse(config.Render(want))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("parse: %v, warnings %v", err, warnings)
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestParseOverridesOnlyGivenKeys(t *testing.T) {
	cfg, warnings, err := config.Parse([]byte(`
[log]
level = "WARNING"

[terminal]
history_kb = 256
`))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("parse: %v, warnings %v", err, warnings)
	}
	want := config.Defaults()
	want.Log.Level = "warn"
	want.Terminal.HistoryKB = 256
	if cfg != want {
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
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg != config.Defaults() {
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
	if err != nil || warnings != nil || cfg != config.Defaults() {
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
