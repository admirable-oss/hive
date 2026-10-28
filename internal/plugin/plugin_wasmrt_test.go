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
)

// buildWasmGuest builds testdata/wasmguest for wasip1.
func buildWasmGuest(t *testing.T) []byte {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	out := filepath.Join(t.TempDir(), "guest.wasm")
	cmd := exec.Command(gobin, "build", "-o", out, "./testdata/wasmguest")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the wasm guest: %v\n%s", err, b)
	}
	code, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

const wasmManifest = `api = 2
id = "wasmy"
version = "1.0.0"

[permissions]
required = ["hive.write"]

[[runtime]]
kind = "wasm"

[[actions]]
id = "probe"
command = ["./guest.wasm", "probe"]

[[actions]]
id = "exit"
command = ["./guest.wasm", "exit"]

[[actions]]
id = "spin"
command = ["./guest.wasm", "spin"]
timeout = "1s"
`

// TestWasmPlugin: a Wasm plugin's module runs in the daemon on every
// system: it gets its context, writes only /data, sees no host files,
// and calls Hive within its capabilities only.
func TestWasmPlugin(t *testing.T) {
	code := buildWasmGuest(t)
	var h *Host
	call := func(_ context.Context, id, method string, _ json.RawMessage) (json.RawMessage, error) {
		if !Allowed(method, h.Granted(id)) {
			need, _ := MethodCapability(method)
			return nil, &CallError{Code: "permission_denied", Message: "plugin " + id + " may not call " + method + ": it needs " + need}
		}
		return json.RawMessage(`{"called":"` + method + `"}`), nil
	}
	h, s, _ := startHost(t, wasmManifest, map[string]string{"guest.wasm": string(code)}, HostConfig{Call: call})
	st := statusOf(h, "wasmy")
	if st.Problem != "" || st.Sandbox != SandboxWasm {
		t.Fatalf("status: problem %q, sandbox %q", st.Problem, st.Sandbox)
	}
	res, err := h.Action(context.Background(), "wasmy", "probe", Context{})
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, res.Output)
	}
	for _, want := range []string{
		"context: wasmy", "data: /data", "write_data: true", "read_host: false",
		`allowed: {"result":{"called":"plugin.capabilities"}}`,
		`denied: {"error":{"code":"permission_denied","message":"plugin wasmy may not call pane.list: it needs hive.read"}}`,
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("probe lacks %q:\n%s", want, res.Output)
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.DataDir("wasmy"), "written")); err != nil || string(b) != "x" {
		t.Fatalf("the module's /data is its data directory: %q %v", b, err)
	}

	if res, err := h.Action(context.Background(), "wasmy", "exit", Context{}); err != nil || res.ExitCode != 3 {
		t.Fatalf("exit status: %+v %v", res, err)
	}
	start := time.Now()
	if _, err := h.Action(context.Background(), "wasmy", "spin", Context{}); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("a spinning module stops at its timeout: %v after %s", err, time.Since(start))
	}
}

func TestWasmManifestChecks(t *testing.T) {
	head := "api = 2\nid = \"w\"\nversion = \"1.0.0\"\n"
	wasm := "[[runtime]]\nkind = \"wasm\"\n"
	action := func(cmd string) string { return "[[actions]]\nid = \"a\"\ncommand = [" + cmd + "]\n" }
	if _, err := ParseManifest([]byte(head + wasm + action(`"./m.wasm"`))); err != nil {
		t.Fatal(err)
	}
	for name, manifest := range map[string]string{
		"api 1":             strings.Replace(head, "api = 2", "api = 1", 1) + wasm + action(`"./m.wasm"`),
		"a native command":  head + wasm + action(`"./run.sh"`),
		"outside":           head + wasm + action(`"../m.wasm"`),
		"isolation":         head + "isolation = \"strict\"\n" + wasm + action(`"./m.wasm"`),
		"undeclared module": head + action(`"./m.wasm"`),
		"network hosts":     head + "[permissions]\nrequired = [\"network\"]\n[permissions.network]\nhosts = [\"a.example:443\"]\n" + wasm + action(`"./m.wasm"`),
	} {
		if _, err := ParseManifest([]byte(manifest)); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !errors.Is(err, ErrInvalidManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
