//go:build !windows

package plugin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// hostileProbe tries what a plugin API 2 plugin may not do unless granted,
// and prints name=ok or name=denied for each.
const hostileProbe = `#!/bin/sh
check() { label=$1; shift; if "$@" >/dev/null 2>&1; then echo "$label=ok"; else echo "$label=denied"; fi; }
check read_secret sh -c 'read -r line < "$1"' _ %[1]q
check write_outside sh -c 'echo x > "$1/written"' _ %[2]q
check write_own sh -c 'echo x > "$HIVE_PLUGIN_DATA/written"'
check exec_listed uname
check exec_unlisted %[6]s
check net_listed curl -sf --max-time 5 http://127.0.0.1:%[3]d/
check net_unlisted curl -sf --max-time 5 http://127.0.0.1:%[4]d/
check net_direct curl -sf --max-time 5 --noproxy '*' http://127.0.0.1:%[3]d/
check daemon_socket curl -sf --max-time 5 --noproxy '*' --unix-socket %[5]q http://hive/
`

func probeResults(out string) map[string]string {
	got := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}
	return got
}

// TestAPI2SandboxConfinesByDefault: a plugin API 2 plugin, sandboxed by
// default, cannot read your files, write outside its directories, run a
// program it does not list, reach a host it does not list (through its
// proxy or around it) or reach the daemon's socket; with the grants, it
// can.
func TestAPI2SandboxConfinesByDefault(t *testing.T) {
	if !SandboxAvailable() {
		t.Skip("no sandbox here: " + sandboxTool())
	}
	var listed []os.FileInfo
	for _, tool := range []string{"curl", "uname"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skip(tool + " not installed")
		}
		if fi, err := os.Stat(p); err == nil {
			listed = append(listed, fi)
		}
	}
	// A program that is not the same file as a listed one: a multi-call
	// coreutils (uutils on Ubuntu 26.04, hard links to one binary) is
	// uname and whoami at once, and Landlock allows files, not names.
	unlisted := ""
	for _, tool := range []string{"whoami", "find", "grep", "sed", "tar"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		fi, err := os.Stat(p)
		if err == nil && !slices.ContainsFunc(listed, func(l os.FileInfo) bool { return os.SameFile(l, fi) }) {
			unlisted = tool
			break
		}
	}
	if unlisted == "" {
		t.Skip("no program here apart from the listed ones")
	}
	// What the plugin must not touch: a secret in a home-like directory,
	// and a directory to write into.
	home := t.TempDir()
	secret := filepath.Join(home, ".ssh", "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("listed")) }))
	defer ok.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("unlisted")) }))
	defer other.Close()
	port := func(s *httptest.Server) int { return s.Listener.Addr().(*net.TCPAddr).Port }
	// The daemon's main socket, which no plugin may reach.
	sockDir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	daemonSock := filepath.Join(sockDir, "hive.sock")
	dl, err := net.Listen("unix", daemonSock)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Close()
	go func() {
		_ = http.Serve(dl, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("daemon")) }))
	}()

	probe := fmt.Sprintf(hostileProbe, secret, outside, port(ok), port(other), daemonSock, unlisted)
	manifest := func(extra string) string {
		return fmt.Sprintf(`api = 2
id = "hostile"
version = "1.0.0"

[permissions]
required = ["network", "process.exec"%s]
[permissions.network]
hosts = ["127.0.0.1:%d"]
[permissions.process]
exec = ["uname", "curl"]

[[actions]]
id = "probe"
command = ["./probe.sh"]
`, extra, port(ok))
	}
	run := func(t *testing.T, m string) map[string]string {
		t.Helper()
		h, _, _ := startHost(t, m, map[string]string{"probe.sh": probe}, HostConfig{SocketPath: daemonSock})
		if st := statusOf(h, "hostile"); st.Problem != "" || st.Sandbox == SandboxNone {
			t.Fatalf("status: problem %q, sandbox %q, warnings %v", st.Problem, st.Sandbox, st.Health.Warnings)
		}
		res, err := h.Action(context.Background(), "hostile", "probe", Context{})
		if err != nil {
			t.Fatalf("probe: %v\n%s", err, res.Output)
		}
		t.Logf("probe:\n%s", res.Output)
		return probeResults(res.Output)
	}

	got := run(t, manifest(""))
	for check, want := range map[string]string{
		"read_secret": "denied", "write_outside": "denied", "write_own": "ok",
		"exec_listed": "ok", "exec_unlisted": "denied",
		"net_listed": "ok", "net_unlisted": "denied", "net_direct": "denied",
		"daemon_socket": "denied",
	} {
		if got[check] != want {
			t.Errorf("confined: %s = %q, want %s", check, got[check], want)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "written")); err == nil {
		t.Error("a confined plugin wrote outside its directories")
	}

	// Granted reads, writes and any program: allowed. The network stays
	// limited to its hosts, and the daemon's socket out of reach.
	got = run(t, strings.Replace(manifest(`, "filesystem.read", "filesystem.write"`), `exec = ["uname", "curl"]`, `exec = []`, 1))
	for check, want := range map[string]string{
		"read_secret": "ok", "write_outside": "ok", "exec_unlisted": "ok",
		"net_listed": "ok", "net_unlisted": "denied", "daemon_socket": "denied",
	} {
		if got[check] != want {
			t.Errorf("granted: %s = %q, want %s", check, got[check], want)
		}
	}
}
