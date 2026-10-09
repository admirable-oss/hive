package main

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
)

// oldDaemon answers on the session's socket like a daemon built before API
// levels existed: its status has no api_level.
type oldDaemon struct {
	ln       net.Listener
	shutdown atomic.Bool // runtime.shutdown arrived
	kept     atomic.Bool // …asking to keep the agents
}

func startOldDaemon(t *testing.T, c *cli, survive bool, running int) *oldDaemon {
	t.Helper()
	ln, err := net.Listen("unix", platform.SocketPath(c.home, "hive.sock"))
	if err != nil {
		t.Fatal(err)
	}
	d := &oldDaemon{ln: ln}
	r := protocol.NewRouter()
	r.MustRegister("runtime.ping", protocol.Method(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }))
	r.MustRegister("runtime.status", protocol.Method(func(context.Context, struct{}) (map[string]any, error) {
		return map[string]any{"status": "running", "pid": os.Getpid(), "version": "dev", "protocol_version": "2", "agents_survive_restart": survive}, nil
	}))
	r.MustRegister("environment.list", protocol.Method(func(context.Context, struct{}) ([]environment.Environment, error) {
		return []environment.Environment{{ID: "old"}}, nil
	}))
	r.MustRegister("process.list", protocol.Method(func(context.Context, struct{}) ([]process.Process, error) {
		out := make([]process.Process, running)
		for i := range out {
			out[i] = process.Process{ID: "p", EnvironmentID: "old", Status: process.StatusRunning}
		}
		return out, nil
	}))
	r.MustRegister("runtime.shutdown", protocol.Method(func(_ context.Context, p struct {
		StopAgents bool `json:"stop_agents"`
	},
	) (struct{}, error) {
		d.shutdown.Store(true)
		d.kept.Store(!p.StopAgents)
		go func() { _ = ln.Close() }() // after the reply: the socket goes, as a daemon's does
		return struct{}{}, nil
	}))
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = protocol.ServeConn(context.Background(), conn, r, protocol.ServerInfo{Version: "dev", Capabilities: []string{"frames/1"}})
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return d
}

func TestE2E_AnOlderDaemonIsReplacedWhenNoAgentCanBeLost(t *testing.T) {
	c := newCLI(t)
	old := startOldDaemon(t, c, true, 3) // its agents outlive it (shims)
	out, errOut, code := c.run("env", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if !old.shutdown.Load() || !old.kept.Load() {
		t.Fatalf("the old daemon should be stopped, keeping its agents (shutdown %v, kept %v)", old.shutdown.Load(), old.kept.Load())
	}
	if !strings.Contains(errOut, "is older than this hive; replacing it") {
		t.Fatalf("the replacement is announced on stderr: %q", errOut)
	}
	st := c.must("--json", "status")
	if !strings.Contains(st, `"api_level": `+strconv.Itoa(protocol.APILevel)) {
		t.Fatalf("a current daemon runs now: %s", st)
	}
}

func TestE2E_AnOlderDaemonWithAgentsAtRiskIsLeftAlone(t *testing.T) {
	c := newCLI(t)
	old := startOldDaemon(t, c, false, 2) // no shims: a restart would stop them
	_, errOut, code := c.run("env", "list")
	if code == 0 || !strings.Contains(errOut, "would stop its 2 running agent(s)") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if old.shutdown.Load() {
		t.Fatal("a daemon whose agents would die must not be stopped")
	}
}

func TestIsGoRunBinary(t *testing.T) {
	for path, want := range map[string]bool{
		"/var/folders/zs/x/T/go-build2691452430/b001/exe/hive": true,
		"/tmp/go-build123/b001/exe/hive":                       true,
		"/usr/local/bin/hive":                                  false,
		"/home/me/go-build/exe-not/hive":                       false,
	} {
		if got := isGoRunBinary(path); got != want {
			t.Errorf("isGoRunBinary(%q) = %v", path, got)
		}
	}
}

func TestStableExecutableCopiesGoRunBinaries(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir() + "/go-build42/b001/exe"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := dir + "/hive"
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := a.stableExecutable(exe)
	if err != nil || !strings.HasPrefix(got, a.base+"/bin/hive-dev-") {
		t.Fatalf("stableExecutable = %q, %v", got, err)
	}
	if again, _ := a.stableExecutable(exe); again != got {
		t.Fatal("the same binary maps to the same copy")
	}
	if err := os.RemoveAll(dir); err != nil { // go run cleans up
		t.Fatal(err)
	}
	if info, err := os.Stat(got); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("the copy outlives the build directory and is executable: %v", err)
	}
	if same, _ := a.stableExecutable("/usr/local/bin/hive"); same != "/usr/local/bin/hive" {
		t.Fatal("installed binaries are used as they are")
	}
}
