//go:build !windows

package mux

import (
	"context"
	"slices"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
)

// frameBudget is M9's target: p99 frame time with 100 panes, all printing.
const frameBudget = 16 * time.Millisecond

// hundredPanes opens 10 tabs of 10 panes (a 5×2 grid each), every one
// printing a line every 20 ms.
func hundredPanes(tb testing.TB) *harness {
	tb.Helper()
	h := newHarness(tb, 200, 60, Options{Clipboard: ClipboardOff})
	ctx := context.Background()
	no := false
	// The app shares the client: keep its loop going while the panes are
	// made, or its events back up behind these calls.
	done := make(chan struct{})
	var setupErr error
	go func() {
		defer close(done)
		setupErr = makeHundred(ctx, h, &no)
	}()
	for pumping := true; pumping; {
		select {
		case <-done:
			pumping = false
		default:
			h.a.Settle(10 * time.Millisecond)
			select {
			case <-h.a.Wake():
			default:
			}
		}
	}
	if setupErr != nil {
		tb.Fatal(setupErr)
	}
	h.waitFor("100 panes", func() bool { return len(h.a.ws.snap.panes) >= 101 })
	return h
}

func makeHundred(ctx context.Context, h *harness, no *bool) error {
	for range 10 {
		created, err := h.api.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "api", Pane: pane.Spec{Command: busyCommand}})
		if err != nil {
			return err
		}
		col := created.Pane.ID
		for c := range 5 {
			if _, err := h.api.PaneSplit(ctx, pane.SplitRequest{Pane: col, Direction: layout.Down, Focus: no, Spec: pane.Spec{Command: busyCommand}}); err != nil {
				return err
			}
			if c < 4 {
				right, err := h.api.PaneSplit(ctx, pane.SplitRequest{Pane: col, Direction: layout.Right, Ratio: 1 - 1/float64(5-c), Focus: no, Spec: pane.Spec{Command: busyCommand}})
				if err != nil {
					return err
				}
				col = right.ID
			}
		}
	}
	return nil
}

// TestFrameTime100Panes checks M9's frame budget: drawing a tab of 10
// streaming panes while 90 more stream in other tabs. Timing under the
// race detector or -short says nothing, so it only runs in plain runs.
func TestFrameTime100Panes(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("frame time is measured without -race and -short")
	}
	h := hundredPanes(t)
	buf := uv.NewScreenBuffer(h.w, h.h)
	var frames []time.Duration
	end := time.Now().Add(4 * time.Second)
	switched := time.Now()
	for tab := 1; time.Now().Before(end); {
		if time.Since(switched) > 400*time.Millisecond { // every tab gets drawn
			tab = tab%10 + 1
			h.a.selectTab(tab)
			switched = time.Now()
		}
		h.a.Settle(5 * time.Millisecond)
		select {
		case <-h.a.Wake():
		case <-time.After(8 * time.Millisecond):
		}
		start := time.Now()
		h.a.Draw(buf)
		frames = append(frames, time.Since(start))
	}
	frames = frames[min(10, len(frames)):] // the first frames build caches
	p50, p99 := percentile(frames, 0.5), percentile(frames, 0.99)
	t.Logf("%d frames with 100 panes: p50 %v, p99 %v, max %v", len(frames), p50, p99, slices.Max(frames))
	if p99 > frameBudget {
		t.Errorf("p99 frame time %v exceeds %v", p99, frameBudget)
	}
	if n := len(h.a.views); n < 10 {
		t.Errorf("only %d views streaming", n)
	}
}

// TestAFrameDrawsAShownPane draws a frame with a live view through the real
// renderer: a frame holds every view's lock from scene() to drawScene, so
// nothing it calls meanwhile may take one again (that hung the UI before
// the first pane was drawn). The latency tests cover this path too, but not
// under -race or -short.
func TestAFrameDrawsAShownPane(t *testing.T) {
	h := newHarness(t, 80, 24, Options{HideSidebar: true, Clipboard: ClipboardOff})
	h.waitFor("the pane's view", func() bool { return len(h.a.views) == 1 })
	d := newDriver(h)
	d.out.frame(h.a)
	d.out.frame(h.a)
}
