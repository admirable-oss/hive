package client_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/vt"
)

// TestFrames_FiftyBusyAgentsAndAThrottledClient is M1's backpressure
// acceptance test: 50 agents flood their terminals while one client reads
// every agent's frames slowly over a single connection. The client receives
// far fewer frames than the agents print lines, and every screen it ends up
// with is exactly the agent's screen.
func TestFrames_FiftyBusyAgentsAndAThrottledClient(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	const agents = 50
	c := startDaemon(t)
	ctx := context.Background()
	if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: "load"}); err != nil {
		t.Fatal(err)
	}
	// Bursts of 20 lines every 50 ms: output keeps flowing for about a
	// second while the client reads slowly.
	script := `i=0; while [ $i -lt 400 ]; do printf '\033[3%dm%s line %d\033[0m\n' $((i%7)) "$AGENT" $i; i=$((i+1)); [ $((i%20)) -eq 0 ] && sleep 0.05; done; printf 'finished %s' "$AGENT"; while :; do sleep 1; done`
	ids := make([]string, agents)
	for i := range agents {
		p, err := c.ProcessStart(ctx, process.StartRequest{
			EnvironmentID: "load", Command: "sh", Args: []string{"-c", "AGENT=a" + fmt.Sprint(i) + "; " + script},
			Terminal: true, Width: 50, Height: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = p.ID
	}

	type result struct {
		screen *vt.Screen
		frames int
	}
	results := make([]result, agents)
	ctxRead, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var stops []func() bool
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()
	for i, id := range ids {
		fs, err := c.TerminalFrames(ctx, client.ViewRequest{ProcessID: id, Width: 50, Height: 10})
		if err != nil {
			t.Fatal(err)
		}
		stops = append(stops, context.AfterFunc(ctxRead, func() { _ = fs.Close() }))
		wg.Go(func() {
			scr := &vt.Screen{}
			for {
				f, err := fs.Next()
				if err != nil {
					if !errors.Is(err, io.EOF) && ctxRead.Err() == nil && !strings.Contains(err.Error(), "closed") {
						t.Errorf("agent %d: %v", i, err)
					}
					break
				}
				scr.Apply(f)
				results[i].frames++
				time.Sleep(30 * time.Millisecond) // a slow client
				if strings.Contains(scr.Text(), "finished a"+fmt.Sprint(i)) {
					// Wait for the screen to settle, then keep reading so
					// trailing frames are applied.
					continue
				}
			}
			results[i].screen = scr
		})
	}

	// Wait until every agent has finished printing, then give the slow
	// client time to catch up and compare.
	deadline := time.Now().Add(60 * time.Second)
	for i, id := range ids {
		for {
			snap, err := c.TerminalSnapshot(ctx, client.SnapshotRequest{ProcessID: id})
			if err == nil && strings.Contains(strings.Join(snap.Lines, "\n"), "finished a"+fmt.Sprint(i)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("agent %d never finished", i)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	time.Sleep(1500 * time.Millisecond)
	cancel()
	wg.Wait()

	totalFrames := 0
	for i, id := range ids {
		snap, err := c.TerminalSnapshot(ctx, client.SnapshotRequest{ProcessID: id})
		if err != nil {
			t.Fatal(err)
		}
		got := results[i].screen
		if got == nil {
			t.Fatalf("agent %d: no screen received", i)
		}
		for y, want := range snap.Lines {
			if line := got.LineText(y); line != want {
				t.Fatalf("agent %d line %d: client has %q, agent shows %q", i, y, line, want)
			}
		}
		totalFrames += results[i].frames
	}
	// 50 agents × 400 lines = 20,000 lines; coalescing must keep the
	// throttled client far below that.
	if totalFrames > 10_000 {
		t.Fatalf("client received %d frames for 20,000 printed lines: no coalescing", totalFrames)
	}
	t.Logf("%d frames delivered for 20,000 printed lines across %d agents", totalFrames, agents)
}
