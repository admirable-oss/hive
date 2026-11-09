//go:build !windows

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
)

func startWatched(t *testing.T) (string, *Module) {
	t.Helper()
	old := socketCheckInterval
	socketCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { socketCheckInterval = old })
	dir, err := os.MkdirTemp("", "hv") // short: macOS's 104-byte socket limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "hive.sock")
	mod := NewModule(Config{SocketPath: path})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.Stop(context.Background()) })
	return path, mod
}

// A daemon whose socket file is deleted must become reachable again (and so
// stoppable), not linger unreachable with its lock and agents.
func TestServer_RecreatesRemovedSocket(t *testing.T) {
	path, mod := startWatched(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	c := client.NewService(client.Config{SocketPath: path})
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := c.Ping(ctx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon unreachable after its socket was removed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("recreated socket: %v %v", fi, err)
	}
	// It is stoppable, and stopping removes the recreated socket.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Shutdown(ctx, true); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-mod.Service.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket left after stop: %v", err)
	}
}

// A path taken over by another file is not the daemon's to replace.
func TestServer_LeavesAReplacedSocketPathAlone(t *testing.T) {
	path, _ := startWatched(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * socketCheckInterval)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "someone else's" {
		t.Fatalf("the daemon touched a file it does not own: %q %v", data, err)
	}
}
