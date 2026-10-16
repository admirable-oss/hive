//go:build !windows

package daemonctl_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/daemonctl"
)

func TestSpawnWaitsForReadiness(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready")
	// The "daemon" becomes ready after a moment and then keeps running.
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "sleep 0.2; touch " + marker + "; sleep 2"},
		Ready: func(context.Context) error {
			_, err := os.Stat(marker)
			return err
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSpawnReportsEarlyExitWithStderr(t *testing.T) {
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "echo 'bad config at line 3' >&2; exit 1"},
		StderrPath: filepath.Join(t.TempDir(), "daemon.stderr"),
		LogPath:    "/nonexistent/daemon.log",
		Ready:      func(context.Context) error { return errors.New("not up") },
		Timeout:    5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "bad config at line 3") || !strings.Contains(err.Error(), "/nonexistent/daemon.log") {
		t.Fatalf("want the daemon's stderr and log path in the error, got %v", err)
	}
}

func TestSpawnSucceedsWhenAnotherDaemonWonTheRace(t *testing.T) {
	calls := 0
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "exit 1"}, // ours loses and exits at once
		Ready: func(context.Context) error {
			calls++
			if calls == 1 {
				return errors.New("not yet")
			}
			return nil // the other client's daemon is up
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("got %v, want success against the winning daemon", err)
	}
}

func TestSpawnTimesOut(t *testing.T) {
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "sleep 1"},
		Ready:      func(context.Context) error { return errors.New("never") },
		Timeout:    200 * time.Millisecond,
	})
	if !errors.Is(err, daemonctl.ErrStartTimeout) {
		t.Fatalf("got %v, want ErrStartTimeout", err)
	}
}

func TestSpawnedDaemonIsDetached(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ids")
	ready := func(context.Context) error {
		data, err := os.ReadFile(out)
		if err != nil || !strings.Contains(string(data), "\n") {
			return errors.New("not yet")
		}
		return nil
	}
	// ps prints the child's session leader; with Setsid it is the child itself.
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", `echo "$$ $(ps -o sess= -p $$ 2>/dev/null || ps -o sid= -p $$)" > ` + out + ".tmp; mv " + out + ".tmp " + out},
		Ready:      ready,
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(mustRead(t, out)))
	if len(fields) == 2 && fields[1] != "0" && fields[0] != fields[1] {
		t.Fatalf("daemon pid %s runs in session %s; want its own session", fields[0], fields[1])
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
