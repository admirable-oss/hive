package integration

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claude(t *testing.T) Integration {
	t.Helper()
	home := t.TempDir()
	in, err := Find(All(home, func(string) string { return "" }), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if in.Path != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("path = %s", in.Path)
	}
	return in
}

func status(t *testing.T, in Integration) Status {
	t.Helper()
	st, err := in.Status()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A user's settings, with a hook of their own on an event Hive uses.
const userSettings = `{
	"model": "opus",
	"hooks": {
		"Stop": [{"hooks": [{"type": "command", "command": "say done"}]}]
	}
}
`

func TestInstallIsIdempotentAndUninstallRestoresTheOriginal(t *testing.T) {
	in := claude(t)
	if err := os.MkdirAll(filepath.Dir(in.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.Path, []byte(userSettings), 0o640); err != nil {
		t.Fatal(err)
	}
	if st := status(t, in); st.State != NotInstalled {
		t.Fatalf("before: %+v", st)
	}

	changed, err := in.Install()
	if err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	st := status(t, in)
	if st.State != Installed || st.Hooks != st.Want || st.Backup == "" {
		t.Fatalf("after install: %+v", st)
	}
	data, _ := os.ReadFile(in.Path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "opus" || !strings.Contains(string(data), "say done") || !strings.Contains(string(data), Marker) {
		t.Fatalf("installed settings lost something:\n%s", data)
	}
	if fi, _ := os.Stat(in.Path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}

	if changed, err := in.Install(); err != nil || changed {
		t.Fatalf("second install: changed=%v err=%v", changed, err)
	}

	changed, err = in.Uninstall()
	if err != nil || !changed {
		t.Fatalf("uninstall: %v %v", changed, err)
	}
	if got, _ := os.ReadFile(in.Path); string(got) != userSettings {
		t.Fatalf("uninstall did not restore the original:\n%s", got)
	}
	if _, err := os.Stat(in.Backup()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("backup left behind: %v", err)
	}
	if changed, err := in.Uninstall(); err != nil || changed {
		t.Fatalf("second uninstall: changed=%v err=%v", changed, err)
	}
}

func TestUninstallKeepsLaterEdits(t *testing.T) {
	in := claude(t)
	if _, err := in.Install(); err != nil { // no settings file yet
		t.Fatal(err)
	}
	if _, err := os.Stat(in.Backup()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a backup of a file that did not exist")
	}
	if _, err := in.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(in.Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the file Hive created is still there")
	}

	// The user edits the file after installing: uninstall keeps the edit.
	if _, err := in.Install(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(in.Path)
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	doc["theme"] = "dark"
	data, _ = json.Marshal(doc)
	if err := os.WriteFile(in.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Uninstall(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(in.Path)
	if strings.Contains(string(data), Marker) || !strings.Contains(string(data), `"theme": "dark"`) || strings.Contains(string(data), "hooks") {
		t.Fatalf("after uninstall:\n%s", data)
	}
}

func TestPartialAndBrokenSettings(t *testing.T) {
	in := claude(t)
	_ = os.MkdirAll(filepath.Dir(in.Path), 0o755)
	stale := `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "hive ` + Marker + ` --state idle"}]}]}}`
	if err := os.WriteFile(in.Path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := status(t, in); st.State != Partial {
		t.Fatalf("stale hook: %+v", st)
	}
	if _, err := in.Install(); err != nil {
		t.Fatal(err)
	}
	if st := status(t, in); st.State != Installed {
		t.Fatalf("install over a stale hook: %+v", st)
	}

	if err := os.WriteFile(in.Path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Install(); err == nil {
		t.Fatal("installed into a broken file")
	}
	if got, _ := os.ReadFile(in.Path); string(got) != "{not json" {
		t.Fatal("a broken file was changed")
	}
}

func TestFind(t *testing.T) {
	all := All("/home/u", func(k string) string {
		if k == "CODEX_HOME" {
			return "/opt/codex"
		}
		return ""
	})
	in, err := Find(all, "codex")
	if err != nil || in.Path != "/opt/codex/hooks.json" {
		t.Fatalf("codex: %+v %v", in, err)
	}
	if _, err := Find(all, "vim"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("vim: %v", err)
	}
}
