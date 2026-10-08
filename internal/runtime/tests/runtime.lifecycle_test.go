package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/runtime"
)

// shortSock returns a socket path short enough for macOS's 104-byte limit,
// which t.TempDir (it embeds the test name) can exceed.
func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "hive.sock")
}

func start(t *testing.T, path string) *runtime.Module {
	t.Helper()
	mod := runtime.NewModule(runtime.Config{SocketPath: path})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = mod.Service.Stop(context.Background()) })
	return mod
}

func TestRuntime_SecondDaemonIsRefused(t *testing.T) {
	path := shortSock(t)
	start(t, path)

	second := runtime.NewModule(runtime.Config{SocketPath: path})
	if err := second.Service.Start(context.Background()); !errors.Is(err, runtime.ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}
	// The first daemon must still own its socket.
	if err := client.NewService(client.Config{SocketPath: path}).Ping(context.Background()); err != nil {
		t.Fatalf("first daemon unreachable after refused start: %v", err)
	}
}

func TestRuntime_StaleSocketIsReplaced(t *testing.T) {
	path := shortSock(t)
	if err := os.WriteFile(path, nil, 0o600); err != nil { // leftover from a crash
		t.Fatal(err)
	}
	start(t, path)
}

func TestRuntime_RemoteShutdownClosesDone(t *testing.T) {
	path := shortSock(t)
	mod := start(t, path)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.NewService(client.Config{SocketPath: path}).Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-mod.Service.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done was not closed after runtime.shutdown")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket should be removed, stat err = %v", err)
	}
}

func TestRuntime_StartRecoversStaleProcesses(t *testing.T) {
	path := shortSock(t)
	root := filepath.Dir(path)

	// Simulate a daemon that crashed while an agent was running.
	procDir := filepath.Join(root, "environments", "dev", "processes", "0123456789abcdef")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := process.Process{ID: "0123456789abcdef", EnvironmentID: "dev", Status: process.StatusRunning}
	if err := jsonfile.Write(filepath.Join(procDir, "process.json"), stale); err != nil {
		t.Fatal(err)
	}

	mod := start(t, path)
	got, err := mod.Processes.Get(context.Background(), stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != process.StatusFailed {
		t.Fatalf("expected stale process to be marked failed, got %s", got.Status)
	}
}

func TestRuntime_PIDFileAndSocketPermissions(t *testing.T) {
	path := shortSock(t)
	mod := start(t, path)
	root := filepath.Dir(path)

	data, err := os.ReadFile(filepath.Join(root, "hive.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid file holds %q, want this process (%d)", got, os.Getpid())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %v, want 0600", perm)
	}

	if err := mod.Service.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "hive.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pid file should be removed on stop, stat err = %v", err)
	}
	// The lock is free again: a new daemon can start on the same root.
	start(t, path)
}

func TestRuntime_ConcurrentStartsElectExactlyOneDaemon(t *testing.T) {
	path := shortSock(t)
	const n = 8
	mods := make([]*runtime.Module, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		mods[i] = runtime.NewModule(runtime.Config{SocketPath: path})
		wg.Go(func() { errs[i] = mods[i].Service.Start(context.Background()) })
	}
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			t.Cleanup(func() { _ = mods[i].Service.Stop(context.Background()) })
		case !errors.Is(err, runtime.ErrAlreadyRunning):
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d daemons started, want exactly one", winners)
	}
	// The winner's socket must still be in place and answering.
	if err := client.NewService(client.Config{SocketPath: path}).Ping(context.Background()); err != nil {
		t.Fatalf("winning daemon unreachable: %v", err)
	}
}
