package runtime_test

import (
	"context"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/runtime"
)

// TestRuntime_NoLeaksAcrossClients connects and disconnects many clients,
// each opening a screen stream and an event stream: the daemon must end up
// with the goroutines and open files it started with.
func TestRuntime_NoLeaksAcrossClients(t *testing.T) {
	path := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: path, Shell: []string{"/bin/cat"}, InheritEnv: true})
	ctx := context.Background()
	if err := mod.Service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.StopWithAgents(context.Background()) })
	if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "dev", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	_, p, err := mod.Panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev"})
	if err != nil {
		t.Fatal(err)
	}

	cycle := func() {
		c := client.NewService(client.Config{SocketPath: path, Name: "leak"})
		defer c.Close()
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := c.Status(cctx); err != nil {
			t.Fatal(err)
		}
		fs, err := c.TerminalFrames(cctx, client.ViewRequest{ProcessID: p.ProcessID, Width: 80, Height: 24})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fs.Next(); err != nil {
			t.Fatal(err)
		}
		es, err := c.Events(cctx, "pane.")
		if err != nil {
			t.Fatal(err)
		}
		_ = es.Close()
		_ = fs.Close()
	}
	settle := func() (int, int) {
		var g, f int
		for range 50 { // goroutines finish shortly after their connection
			time.Sleep(20 * time.Millisecond)
			g = goruntime.NumGoroutine()
			f, _ = platform.OpenFDs()
		}
		return g, f
	}
	for range 5 {
		cycle() // warm up pools and lazily started workers
	}
	g0, f0 := settle()
	for range 100 {
		cycle()
	}
	g1, f1 := settle()
	t.Logf("after 100 clients: goroutines %d → %d, open files %d → %d", g0, g1, f0, f1)
	if g1 > g0+5 {
		t.Errorf("goroutines grew from %d to %d", g0, g1)
	}
	if f1 > f0+3 {
		t.Errorf("open files grew from %d to %d", f0, f1)
	}
}
