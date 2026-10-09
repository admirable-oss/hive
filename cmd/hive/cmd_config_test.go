package main

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/config"
)

// runIn runs the CLI in-process with getenv and returns stdout, stderr and
// the exit code.
func runIn(getenv func(string) string, args ...string) (string, string, int) {
	var out, errOut bytes.Buffer
	code := execute(args, &out, &errOut, getenv)
	return out.String(), errOut.String(), code
}

func TestConfigValidateChecksBindingsAndTheme(t *testing.T) {
	getenv := testEnv(t)
	cfg := getenv("HIVE_CONFIG")
	doc := "[theme]\nname = \"solarized\"\n[keys.prefix]\nsplit_sideways = \"|\"\nzoom = \"ctrl+nope+z\"\n"
	if err := os.WriteFile(cfg, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := runIn(getenv, "config", "validate")
	if code != exitError {
		t.Fatalf("exit %d, want %d; output %q", code, exitError, out)
	}
	for _, want := range []string{`unknown theme "solarized"`, `unknown action "split_sideways"`, "keys.prefix.zoom"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate output lacks %q:\n%s", want, out)
		}
	}
}

func TestConfigResetKeys(t *testing.T) {
	getenv := testEnv(t)
	cfg := getenv("HIVE_CONFIG")
	doc := "[daemon]\nautostart = false\n\n[keys]\nprefix_keys = [\"ctrl+a\"]\n\n[keys.prefix]\nzoom = []\n"
	if err := os.WriteFile(cfg, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runIn(getenv, "config", "reset-keys")
	if code != 0 || !strings.Contains(out, "reset the key bindings") {
		t.Fatalf("exit %d: %q %q", code, out, errOut)
	}
	got, warnings, err := config.Load(cfg)
	if err != nil || len(warnings) > 0 || !reflect.DeepEqual(got.Keys, config.Defaults().Keys) || got.Daemon.Autostart {
		data, _ := os.ReadFile(cfg)
		t.Fatalf("reset file (%v %v):\n%s", err, warnings, data)
	}
	if bak, _ := os.ReadFile(cfg + ".bak"); string(bak) != doc {
		t.Fatalf("backup:\n%s", bak)
	}
	if out, _, _ := runIn(getenv, "config", "reset-keys"); !strings.Contains(out, "already uses the default key bindings") {
		t.Fatalf("second run: %q", out)
	}
	if bak, _ := os.ReadFile(cfg + ".bak"); string(bak) != doc {
		t.Fatal("a second run must keep the backup of the user's bindings")
	}
}
