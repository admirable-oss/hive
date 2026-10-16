//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/runtime"
)

// tickAgent prints a numbered line five times a second.
const tickAgent = `i=0; while :; do i=$((i+1)); echo "tick $i"; sleep 0.2; done`

// TestChaos is M9's chaos soak: while agents run here and on a remote
// machine, faults are injected at random (the daemon killed or frozen, a
// shim killed, the remote link cut). After every fault the daemon must
// serve again with every pane in place, and the remote screen stream must
// come back. At the end the daemon must not have grown in goroutines or
// open files. It runs for $HIVE_CHAOS (the release gate is 72h; by default
// 20s).
func TestChaos(t *testing.T) {
	duration := 20 * time.Second
	if d, err := time.ParseDuration(os.Getenv("HIVE_CHAOS")); err == nil {
		duration = d
	}
	seed := uint64(time.Now().UnixNano())
	t.Logf("chaos seed %d (HIVE_CHAOS_SEED replays it)", seed)
	if s, err := fmt.Sscan(os.Getenv("HIVE_CHAOS_SEED"), &seed); s == 1 && err == nil {
		t.Logf("replaying seed %d", seed)
	}
	rng := rand.New(rand.NewPCG(seed, 9))

	c := newCLI(t)
	c.withShell()
	root := c.fakeHosts()
	alpha := c.host(root, "alpha", true)
	withFakeSSHEnv(t, root)

	// Six agents here, two on alpha.
	c.must("env", "create", "dev", "--managed")
	var created client.TabCreated
	decode(t, c.must("--json", "tab", "create", "dev", "--", "sh", "-c", tickAgent), &created)
	for range 5 {
		c.must("pane", "split", created.Pane.ID, "--no-focus", "--", "sh", "-c", tickAgent)
	}
	c.must("--remote", "alpha", "env", "create", "far", "--managed")
	var far client.TabCreated
	decode(t, c.must("--remote", "alpha", "--json", "tab", "create", "far", "--", "sh", "-c", tickAgent), &far)

	// A remote client streams alpha's agent, reconnecting when the link drops.
	target := c.remoteTarget(root, "alpha")
	remote := client.NewService(client.Config{Name: "chaos", Remote: true, Dial: target.Dial})
	defer remote.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var frames, streams atomic.Int64
	var wg sync.WaitGroup
	wg.Go(func() {
		for ctx.Err() == nil {
			sctx, scancel := context.WithTimeout(ctx, 30*time.Second)
			fs, err := remote.TerminalFrames(sctx, client.ViewRequest{ProcessID: far.Pane.ProcessID, Width: 80, Height: 24})
			scancel()
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			streams.Add(1)
			stop := context.AfterFunc(ctx, func() { _ = fs.Close() })
			for {
				if _, err := fs.Next(); err != nil {
					break
				}
				frames.Add(1)
			}
			stop()
			_ = fs.Close()
		}
	})
	defer func() { cancel(); wg.Wait() }()

	healthy := func(what string) {
		t.Helper()
		// Any daemon command starts it again, as the user's next one would.
		c.waitForUpTo(30*time.Second, "the daemon to serve after "+what, func() bool {
			_, _, code := c.run("pane", "list")
			return code == 0
		})
		var panes []pane.Pane
		decode(t, c.must("--json", "pane", "list"), &panes)
		if len(panes) != 6 {
			t.Fatalf("after %s: %d panes, want 6", what, len(panes))
		}
		for _, p := range panes {
			if p.Process != nil && p.Process.Status == "starting" {
				t.Fatalf("after %s: pane %s stuck starting", what, p.ID)
			}
		}
	}
	shimPIDs := func() []int {
		var pids []int
		entries, _ := os.ReadDir(filepath.Join(c.home, "run"))
		for _, e := range entries {
			var st struct {
				ShimPID int `json:"shim_pid"`
				Status  string
			}
			data, err := os.ReadFile(filepath.Join(c.home, "run", e.Name(), "state.json"))
			if err == nil && json.Unmarshal(data, &st) == nil && st.ShimPID > 0 && st.Status == "running" {
				pids = append(pids, st.ShimPID)
			}
		}
		return pids
	}

	healthy("starting")
	faults := map[string]int{}
	for end := time.Now().Add(duration); time.Now().Before(end); {
		switch rng.IntN(4) {
		case 0:
			faults["daemon killed"]++
			if pid := c.pid(); pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				c.waitFor("the daemon to die", func() bool { return syscall.Kill(pid, 0) != nil })
			}
			healthy("a daemon crash")
		case 1:
			if pids := shimPIDs(); len(pids) > 0 {
				faults["shim killed"]++
				_ = syscall.Kill(pids[rng.IntN(len(pids))], syscall.SIGKILL)
				healthy("a shim crash")
			}
		case 2:
			faults["daemon frozen"]++
			if pid := c.pid(); pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGSTOP)
				time.Sleep(2 * time.Second)
				_ = syscall.Kill(pid, syscall.SIGCONT)
			}
			healthy("a frozen daemon")
		case 3:
			faults["remote link cut"]++
			before := streams.Load()
			// What the network dropping does to ssh: the bridge goes away.
			_ = exec.Command("pkill", "-9", "-f", filepath.Join(alpha, ".local", "bin", "hive")+" proxy").Run()
			c.waitForUpTo(30*time.Second, "the remote stream to come back", func() bool { return streams.Load() > before })
		}
		time.Sleep(time.Duration(200+rng.IntN(800)) * time.Millisecond)
	}
	t.Logf("faults: %v; remote stream: %d frames over %d connections", faults, frames.Load(), streams.Load())

	// The daemon that is left must not leak while it is used.
	stats := func() runtime.Stats {
		var st runtime.Stats
		decode(t, c.must("--json", "debug", "stats"), &st)
		return st
	}
	var panes []pane.Pane
	decode(t, c.must("--json", "pane", "list"), &panes)
	live := ""
	for _, p := range panes {
		if p.Process != nil && p.Process.Active() {
			live = p.ProcessID
		}
	}
	use := func() {
		for range 20 {
			if live != "" {
				c.must("terminal", "snapshot", live)
			}
			c.must("pane", "list")
		}
	}
	use()
	time.Sleep(time.Second)
	s0 := stats()
	use()
	time.Sleep(time.Second)
	s1 := stats()
	t.Logf("daemon after chaos: goroutines %d → %d, open files %d → %d", s0.Goroutines, s1.Goroutines, s0.OpenFDs, s1.OpenFDs)
	if s1.Goroutines > s0.Goroutines+10 || s1.OpenFDs > s0.OpenFDs+5 {
		t.Errorf("the daemon leaks: goroutines %d → %d, open files %d → %d", s0.Goroutines, s1.Goroutines, s0.OpenFDs, s1.OpenFDs)
	}
	if out := c.must("--remote", "alpha", "terminal", "snapshot", far.Pane.ProcessID); !strings.Contains(out, "tick") {
		t.Fatalf("alpha's agent:\n%s", out)
	}
}
