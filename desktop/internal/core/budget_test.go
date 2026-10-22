//go:build !race && !windows

package core

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// cpuTime is the process's user and system time so far.
func cpuTime(t *testing.T) time.Duration {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatal(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// TestIdleBudgetWith16Panes holds the app's Go side to M18's budgets with
// 16 panes on screen and nothing happening: under 1 % of a core, and a
// small heap. (The memory budget of 250 MB is the whole app, webview
// included; the Go side is held to 64 MB of heap here.) The daemon is a
// process of its own, so this measures the app alone.
func TestIdleBudgetWith16Panes(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a daemon and 16 shells")
	}
	home := testHome(t)
	rec := &recorder{}
	h := New(Config{Home: home, Hive: hiveBin, Emitter: rec})
	h.Start()
	defer h.Close()
	waitFor(t, "a connection", 30*time.Second, func() bool { return h.Connection().State == StateConnected })
	if err := h.CreateEnvironment("idle", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var panes []string
	for i := range 16 {
		p, err := h.NewTab("idle")
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the pane in the workspace", 10*time.Second, func() bool {
			_, ok := paneByID(h.Workspace(), p)
			return ok
		})
		if err := h.OpenView(fmt.Sprintf("w%d", i), p, 80, 24); err != nil {
			t.Fatal(err)
		}
		panes = append(panes, p)
	}
	for _, p := range panes {
		waitFor(t, "every shell's prompt", 30*time.Second, func() bool { return strings.TrimSpace(rec.screen(p)) != "" })
	}
	// Let prompts, title updates and the first workspace events settle.
	time.Sleep(3 * time.Second)

	runtime.GC()
	before := cpuTime(t)
	start := time.Now()
	time.Sleep(10 * time.Second)
	used := cpuTime(t) - before
	share := float64(used) / float64(time.Since(start))
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	t.Logf("idle with 16 panes: %.2f %% of a core, heap in use %.1f MB, %d goroutines",
		share*100, float64(ms.HeapInuse)/(1<<20), runtime.NumGoroutine())
	if share > 0.01 {
		t.Errorf("idle CPU is %.2f %% of a core, over the 1 %% budget", share*100)
	}
	if ms.HeapInuse > 64<<20 {
		t.Errorf("heap in use is %.1f MB, over the 64 MB budget for the Go side", float64(ms.HeapInuse)/(1<<20))
	}
}
