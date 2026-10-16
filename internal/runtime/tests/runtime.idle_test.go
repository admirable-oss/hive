//go:build !race && !windows

package runtime_test

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/runtime"
)

// cpuTime is this process's user and system CPU time so far.
func cpuTime(t *testing.T) time.Duration {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatal(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// TestRuntime_IdleCPUWith100Panes is M9's idle target: a daemon with 100
// panes whose programs wait for input uses under 1 % of a CPU. (The daemon
// runs in this process, without shims, so its terminals are measured too.)
func TestRuntime_IdleCPUWith100Panes(t *testing.T) {
	if testing.Short() {
		t.Skip("measures for ten seconds")
	}
	path := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: path, Shell: []string{"/bin/cat"}, InheritEnv: true})
	ctx := context.Background()
	if err := mod.Service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = mod.Service.StopWithAgents(stop)
	})
	if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "idle", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	c := client.NewService(client.Config{SocketPath: path})
	defer c.Close()
	ws := client.NewWorkspace(c)
	for range 10 {
		created, err := ws.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "idle"})
		if err != nil {
			t.Fatal(err)
		}
		at := created.Pane.ID
		for range 9 {
			p, err := ws.PaneSplit(ctx, pane.SplitRequest{Pane: at})
			if err != nil {
				t.Fatal(err)
			}
			at = p.ID
		}
	}
	panes, err := ws.PaneList(ctx, "idle", "")
	if err != nil || len(panes) != 100 {
		t.Fatalf("%d panes, %v", len(panes), err)
	}
	time.Sleep(3 * time.Second) // starting up is not idling
	cpu0, wall0 := cpuTime(t), time.Now()
	time.Sleep(10 * time.Second)
	used := float64(cpuTime(t)-cpu0) / float64(time.Since(wall0))
	t.Logf("idle with 100 panes: %.2f%% of a CPU", used*100)
	if used > 0.01 {
		t.Errorf("idle CPU %.2f%% is over 1%%", used*100)
	}
}
