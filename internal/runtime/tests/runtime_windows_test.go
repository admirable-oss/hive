//go:build windows

package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/runtime"
)

// TestWindowsRuntime: the daemon serves its socket, refuses a second
// daemon on the same root, runs a terminal agent in a pseudo console that
// the client can type into and read, stops it, and shuts down.
func TestWindowsRuntime(t *testing.T) {
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "hive.sock")
	mod := runtime.NewModule(runtime.Config{SocketPath: sock, InheritEnv: true})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = mod.Service.Stop(context.Background())
		}
	})
	second := runtime.NewModule(runtime.Config{SocketPath: sock})
	if err := second.Service.Start(context.Background()); !errors.Is(err, runtime.ErrAlreadyRunning) {
		t.Fatalf("second daemon: %v", err)
	}

	c := client.NewService(client.Config{SocketPath: sock})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: "dev", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	p, err := c.ProcessStart(ctx, process.StartRequest{EnvironmentID: "dev", Command: "cmd.exe", Args: []string{"/q", "/k", "echo agent-up"}, Terminal: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	screen := func(want string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			snap, err := c.TerminalSnapshot(ctx, client.SnapshotRequest{ProcessID: p.ID})
			if err == nil && strings.Contains(strings.Join(snap.Lines, "\n"), want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("screen never showed %q: %+v %v", want, snap.Lines, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	screen("agent-up")
	if err := c.TerminalInput(ctx, p.ID, []byte("echo typed-%USERNAME:~0,0%windows\r")); err != nil {
		t.Fatal(err)
	}
	screen("typed-windows")
	if err := c.TerminalResize(ctx, p.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := c.ProcessStop(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		got, err := c.ProcessGet(ctx, p.ID)
		if err == nil && !got.Active() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent did not stop: %+v %v", got, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := mod.Service.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	stopped = true
}
