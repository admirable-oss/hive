package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestSSHCommandLine(t *testing.T) {
	tg := Target{Host: "me@box", Session: "agents", ControlDir: "/h/ssh", SSH: []string{"ssh", "-F", "cfg"}}
	cmd, err := tg.Command(context.Background(), false, "agent", "prompt", "it's done")
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args
	for _, want := range []string{"ControlMaster=auto", "ControlPath=/h/ssh/%C", "ServerAliveInterval=15", "-T", "me@box"} {
		if !slices.Contains(args, want) {
			t.Errorf("args lack %q: %q", want, args)
		}
	}
	if args[0] != "ssh" || args[1] != "-F" || args[2] != "cfg" {
		t.Errorf("the configured ssh comes first: %q", args)
	}
	script := args[len(args)-1]
	if !strings.Contains(script, `exec "$h" --session agents agent prompt 'it'\''s done'`) || !strings.Contains(script, "command -v hive") {
		t.Errorf("remote script = %s", script)
	}
	if args[len(args)-2] != "--" {
		t.Errorf("the host must end the options: %q", args)
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "a b"} {
		if _, err := (Target{Host: bad}).Command(context.Background(), false); !errors.Is(err, ErrInvalidHost) {
			t.Errorf("host %q: %v", bad, err)
		}
	}
	if s := (Target{Host: "h", HivePath: "~/opt/hive"}).hive(); s != `h="$HOME"/opt/hive` {
		t.Errorf("~ path: %s", s)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{"plain-1.2": "plain-1.2", "": "''", "a b": "'a b'", "it's": `'it'\''s'`, "$(x)": "'$(x)'"} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseProbe(t *testing.T) {
	p := parseProbe([]byte("os=Linux\narch=aarch64\npath=/home/me/.local/bin/hive\nversion={\"version\":\"v0.9.0\",\"api_level\":7}\n"))
	if p.OS != "linux" || p.Arch != "arm64" || !p.Installed() || p.Version != "v0.9.0" || p.APILevel != 7 {
		t.Fatalf("probe = %+v", p)
	}
	if p := parseProbe([]byte("os=Darwin\narch=x86_64\n")); p.Installed() || p.OS != "darwin" || p.Arch != "amd64" {
		t.Fatalf("no hive: %+v", p)
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "machines.json"))
	if ms, err := r.List(); err != nil || len(ms) != 0 {
		t.Fatalf("empty = %v, %v", ms, err)
	}
	if _, err := r.Add(Machine{Name: "gpu", Host: "me@gpu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(Machine{Name: "ci", Host: "ci", Session: "agents"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(Machine{Name: "gpu", Host: "x"}); !errors.Is(err, ErrMachineExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := r.Add(Machine{Name: "bad name", Host: "x"}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := r.Update("gpu", func(m *Machine) error { m.Name = "ci"; return nil }); !errors.Is(err, ErrMachineExists) {
		t.Fatalf("rename onto another: %v", err)
	}
	if _, err := r.Update("gpu", func(m *Machine) error { m.Name, m.Disabled = "gpu2", true; return nil }); err != nil {
		t.Fatal(err)
	}
	ms, _ := r.List()
	if len(ms) != 2 || ms[0].Name != "ci" || ms[1].Name != "gpu2" || !ms[1].Disabled || ms[1].AddedAt.IsZero() {
		t.Fatalf("list = %+v", ms)
	}
	if err := r.Remove("ci"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("ci"); !errors.Is(err, ErrMachineNotFound) {
		t.Fatalf("removed: %v", err)
	}
	if info, _ := os.Stat(r.path); info.Mode().Perm() != 0o600 {
		t.Fatalf("machines.json mode %v", info.Mode().Perm())
	}
}

// fakeSSH writes an ssh that ignores its options and runs script.
func fakeSSH(t *testing.T, script string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{path}
}

func TestDialIsASocketToTheRemoteCommand(t *testing.T) {
	// The "remote" end echoes what it reads, as a bridge would relay it.
	tg := Target{Host: "box", SSH: fakeSSH(t, "exec cat")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := tg.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("hello hive\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 11)
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("deadlines must work: %v", err)
	}
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "hello hive\n" {
		t.Fatalf("read %q, %v", buf, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDialExplainsWhySSHFailed(t *testing.T) {
	for _, tt := range []struct {
		script, want string
		is           error
	}{
		{"echo 'ssh: connect to host box port 22: Connection refused' >&2; exit 255", "Connection refused", nil},
		{"echo 'sh: hive: not found' >&2; exit 127", "not found", ErrNotInstalled},
	} {
		tg := Target{Host: "box", SSH: fakeSSH(t, tt.script)}
		conn, err := tg.Dial(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = conn.Read(make([]byte, 1))
		_ = conn.Close()
		if err == nil || !strings.Contains(err.Error(), tt.want) || (tt.is != nil && !errors.Is(err, tt.is)) {
			t.Errorf("%s: read error %v", tt.script, err)
		}
	}
}

func TestShellAndInstall(t *testing.T) {
	home := t.TempDir()
	// The fake runs the script locally with HOME set, like a login there.
	tg := Target{Host: "box", SSH: fakeSSH(t, `for a; do last="$a"; done; HOME=`+home+` exec sh -c "$last"`)}
	ctx := context.Background()
	p, err := tg.Probe(ctx)
	if err != nil || p.OS == "" || p.Installed() {
		t.Fatalf("probe of an empty home = %+v, %v", p, err)
	}
	path, err := tg.Install(ctx, strings.NewReader("#!/bin/sh\necho '{\"version\":\"v9\",\"api_level\":99}'\n"))
	if err != nil || path != filepath.Join(home, ".local", "bin", "hive") {
		t.Fatalf("install = %q, %v", path, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed binary: %v, %v", info, err)
	}
	if p, err := tg.Probe(ctx); err != nil || !p.Installed() || p.Version != "v9" || p.APILevel != 99 {
		t.Fatalf("probe after install = %+v, %v", p, err)
	}
}
