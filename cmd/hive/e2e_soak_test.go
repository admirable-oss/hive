//go:build !windows

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
)

// TestM7Soak is M7's acceptance: 5 remote machines and 40 agents, every
// agent's screen streamed to this client as the UI would, run without
// growth in latency or memory. It runs for $HIVE_SOAK (the release gate is
// 8h; by default 20s, enough to catch a leak that grows per frame).
func TestM7Soak(t *testing.T) {
	duration := 20 * time.Second
	if d, err := time.ParseDuration(os.Getenv("HIVE_SOAK")); err == nil {
		duration = d
	}
	c := newCLI(t)
	root := c.fakeHosts()
	withFakeSSHEnv(t, root)
	const machines, perMachine = 5, 8

	type machine struct {
		name   string
		dir    string
		client client.Client
		agents []string
	}
	var ms []*machine
	var wire atomic.Int64
	for i := range machines {
		m := &machine{name: fmt.Sprintf("m%d", i)}
		m.dir = c.host(root, m.name, true)
		c.must("machine", "add", m.name, m.name)
		c.must("--machine", m.name, "env", "create", "dev", "--managed")
		for j := range perMachine {
			script := busyAgent
			if j%2 == 1 { // half of them idle, as most agents are
				script = `printf 'idle agent\r\n> '; while :; do sleep 5; done`
			}
			var started struct {
				ID string `json:"id"`
			}
			decode(t, c.must("--machine", m.name, "--json", "ps", "start", "-t", "dev", "--", "sh", "-c", script), &started)
			m.agents = append(m.agents, started.ID)
		}
		target := c.remoteTarget(root, m.name)
		m.client = client.NewService(client.Config{
			Name: "soak", Remote: true, FrameBudget: 48 << 10,
			Dial: func(ctx context.Context) (net.Conn, error) {
				conn, err := target.Dial(ctx)
				if err != nil {
					return nil, err
				}
				return countingConn{Conn: conn, n: &wire}, nil
			},
		})
		ms = append(ms, m)
	}
	defer func() {
		for _, m := range ms {
			_ = m.client.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	var streams sync.WaitGroup
	var frames atomic.Int64
	for _, m := range ms {
		for _, id := range m.agents {
			streams.Go(func() {
				fs, err := m.client.TerminalFrames(ctx, client.ViewRequest{ProcessID: id, Width: 120, Height: 30})
				if err != nil {
					t.Errorf("stream %s on %s: %v", id, m.name, err)
					return
				}
				defer fs.Close()
				stop := context.AfterFunc(ctx, func() { _ = fs.Close() })
				defer stop()
				for {
					if _, err := fs.Next(); err != nil {
						return
					}
					frames.Add(1)
				}
			})
		}
	}

	type sample struct {
		latency    time.Duration // the slowest machine's agent list
		rss        int64         // every remote daemon's, in KiB
		goroutines int           // this client's
	}
	var samples []sample
	interval := max(duration/40, time.Second)
	for end := time.Now().Add(duration); time.Now().Before(end); time.Sleep(interval) {
		var s sample
		for _, m := range ms {
			start := time.Now()
			cctx, ccancel := context.WithTimeout(ctx, 30*time.Second)
			res, err := client.NewAgents(m.client).List(cctx, true)
			ccancel()
			if err != nil || len(res.Agents) != perMachine {
				t.Fatalf("agent list on %s: %d agents, %v", m.name, len(res.Agents), err)
			}
			s.latency = max(s.latency, time.Since(start))
			s.rss += daemonRSS(t, m.dir)
		}
		s.goroutines = runtime.NumGoroutine()
		samples = append(samples, s)
	}
	cancel()
	streams.Wait()

	// Compare the last third with the first third (after a warm-up sample).
	third := max(len(samples)/3, 1)
	early, late := samples[1:1+third], samples[len(samples)-third:]
	median := func(ss []sample, f func(sample) float64) float64 {
		v := make([]float64, len(ss))
		for i, s := range ss {
			v[i] = f(s)
		}
		slices.Sort(v)
		return v[len(v)/2]
	}
	lat := func(s sample) float64 { return float64(s.latency.Milliseconds()) }
	rss := func(s sample) float64 { return float64(s.rss) }
	gor := func(s sample) float64 { return float64(s.goroutines) }
	t.Logf("%d samples over %s; %d frames, %.1f MB on the wire; latency %.0f → %.0f ms; daemons' RSS %.0f → %.0f MiB; goroutines %.0f → %.0f",
		len(samples), duration, frames.Load(), float64(wire.Load())/1e6,
		median(early, lat), median(late, lat), median(early, rss)/1024, median(late, rss)/1024, median(early, gor), median(late, gor))
	if l0, l1 := median(early, lat), median(late, lat); l1 > 2*l0+50 {
		t.Errorf("latency grew from %.0f to %.0f ms", l0, l1)
	}
	if r0, r1 := median(early, rss), median(late, rss); r1 > 1.5*r0+50*1024 {
		t.Errorf("daemons' memory grew from %.0f to %.0f MiB", r0/1024, r1/1024)
	}
	if g0, g1 := median(early, gor), median(late, gor); g1 > g0+20 {
		t.Errorf("client goroutines grew from %.0f to %.0f", g0, g1)
	}
	if frames.Load() < int64(machines*perMachine/2)*int64(duration.Seconds()) {
		t.Errorf("only %d frames in %s: streams stalled", frames.Load(), duration)
	}
}

// daemonRSS is the resident memory of a fake host's daemon, in KiB.
func daemonRSS(t *testing.T, dir string) int64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".hive", "hive.pid"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strings.TrimSpace(string(data))).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
