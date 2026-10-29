package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestM26WasmPlugin: a plugin API 2 WebAssembly plugin runs in the daemon
// on every system, Windows included, and calls Hive through the daemon
// with its capabilities checked as on its socket: what it holds works,
// what it does not is refused.
func TestM26WasmPlugin(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	c := newCLI(t)
	dir := t.TempDir()
	build := exec.Command(gobin, "build", "-o", filepath.Join(dir, "guest.wasm"), "../../internal/plugin/testdata/wasmguest")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the wasm guest: %v\n%s", err, out)
	}
	manifest := `api = 2
id = "wasmy"
version = "1.0.0"
min_hive_version = "1.7.0"

[permissions]
required = ["hive.write"]

[[runtime]]
kind = "wasm"

[[actions]]
id = "probe"
command = ["./guest.wasm", "probe"]
`
	if err := os.WriteFile(filepath.Join(dir, "hive-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	c.must("daemon", "start")
	c.must("plugin", "link", dir)
	if out := c.must("plugin", "info", "wasmy"); !strings.Contains(out, "sandbox: wasm") {
		t.Fatalf("info:\n%s", out)
	}
	out := c.must("plugin", "run", "wasmy", "probe")
	for _, want := range []string{
		"context: wasmy", "write_data: true", "read_host: false",
		`allowed: {"result":`,
		`denied: {"error":{"code":"permission_denied"`, "it needs hive.read",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("probe lacks %q:\n%s", want, out)
		}
	}
}
