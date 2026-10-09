package mux

import (
	"context"
	"io"
	"slices"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
)

// latencyBudget is M3's acceptance target: p99 keystroke latency with 16
// visible panes.
const latencyBudget = 10 * time.Millisecond

// keyGap is the time between measured keystrokes: key auto-repeat speed,
// faster than anyone types. (The daemon sends one frame per view per 8 ms,
// so keys typed back to back would measure that cap, not the UI.)
const keyGap = 30 * time.Millisecond

// busyCommand prints a line every 20 ms: fifteen of them keep the screen
// changing while the latency is measured.
var busyCommand = []string{"/bin/sh", "-c", `i=0; while :; do i=$((i+1)); echo "busy line $i"; sleep 0.02; done`}

// sixteenPanes opens a 4×4 grid: the focused pane runs cat, the other
// fifteen print continuously.
func sixteenPanes(tb testing.TB) *harness {
	tb.Helper()
	h := newHarness(tb, 166, 47, Options{HideSidebar: true, Clipboard: ClipboardOff})
	ctx := context.Background()
	first := h.focused()
	no := false
	split := func(target string, d layout.Direction, ratio float64) string {
		p, err := h.api.PaneSplit(ctx, pane.SplitRequest{Pane: target, Direction: d, Ratio: ratio, Focus: &no, Spec: pane.Spec{Command: busyCommand}})
		if err != nil {
			tb.Fatal(err)
		}
		return p.ID
	}
	cols := []string{first}
	for _, ratio := range []float64{0.75, 2.0 / 3, 0.5} {
		cols = append(cols, split(cols[len(cols)-1], layout.Right, ratio))
	}
	for _, top := range cols {
		at := top
		for _, ratio := range []float64{0.75, 2.0 / 3, 0.5} {
			at = split(at, layout.Down, ratio)
		}
	}
	h.waitFor("16 streaming panes", func() bool {
		return len(h.panes()) == 16 && len(h.a.views) == 16 && h.focused() == first
	})
	h.waitSized()
	return h
}

// driver runs the App as Run does, rendering every frame through the real
// ultraviolet renderer into a discarded terminal.
type driver struct {
	a    *App
	out  *terminalOut
	last time.Time
}

func newDriver(h *harness) *driver {
	scr := uv.NewTerminalScreen(io.Discard, []string{"TERM=xterm-256color"})
	_ = scr.Resize(h.w, h.h)
	out := &terminalOut{scr: scr}
	out.enter(true)
	return &driver{a: h.a, out: out}
}

// frame draws if a frame is due.
func (d *driver) frame() {
	now := time.Now()
	if draw, _ := d.a.frameDue(now, d.last); draw {
		d.out.frame(d.a)
		d.last = now
	}
}

// step waits for the next wake or message, then draws if due. It reports
// whether the view had received a frame after the before'th when the frame
// was drawn.
func (d *driver) step(v *view, before uint64) bool {
	select {
	case m := <-d.a.Msgs():
		d.a.Handle(m)
	case <-d.a.Wake():
		d.a.dirty = true
	case <-time.After(frameInterval):
	}
	got := framesOf(v) > before // read first: the frame below shows it
	d.frame()
	return got
}

// framesOf counts the frames a view applied. The cat pane only changes
// when it echoes, so a new frame is the echo.
func framesOf(v *view) uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.frames
}

func cursorX(v *view) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.screen == nil {
		return -1
	}
	return v.screen.Cursor.X
}

// measureLatency types n keys into the focused pane, keyGap apart, and
// returns for each the time from the key event to the drawn frame that
// shows its echo.
func measureLatency(tb testing.TB, h *harness, n int, timer func(on bool)) []time.Duration {
	tb.Helper()
	d := newDriver(h)
	v := h.a.focusedView()
	if v == nil {
		tb.Fatal("no view for the focused pane")
	}
	out := make([]time.Duration, 0, n)
	key := uv.KeyPressEvent(uv.Key{Code: 'x', Text: "x"})
	enter := uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter})
	typed := 0
	for len(out) < n {
		if typed == 30 {
			// A fresh line, untimed: cat's line buffer is bounded.
			timer(false)
			h.a.HandleEvent(enter)
			deadline := time.Now().Add(2 * time.Second)
			for cursorX(v) != 0 && time.Now().Before(deadline) {
				d.step(v, 0)
			}
			// Let cat's output land before timing the next key.
			for quiet := time.Now().Add(20 * time.Millisecond); time.Now().Before(quiet); {
				d.step(v, 0)
			}
			typed = 0
			timer(true)
		}
		before := framesOf(v)
		start := time.Now()
		h.a.HandleEvent(key)
		d.frame() // input is drawn at once
		deadline := start.Add(2 * time.Second)
		for !d.step(v, before) {
			if time.Now().After(deadline) {
				tb.Fatalf("no echo after 2s (frame %d)", before)
			}
		}
		out = append(out, time.Since(start))
		typed++
		// The other panes keep printing until the next key.
		timer(false)
		for next := start.Add(keyGap); time.Now().Before(next); {
			d.step(v, framesOf(v))
		}
		timer(true)
	}
	return out
}

func percentile(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[min(int(float64(len(s))*p), len(s)-1)]
}

// BenchmarkInputLatency16Panes reports keystroke-to-frame latency with 16
// visible panes, fifteen of them printing (p50/p99/max in ms).
func BenchmarkInputLatency16Panes(b *testing.B) {
	h := sixteenPanes(b)
	measureLatency(b, h, 10, func(bool) {}) // warm up
	b.ResetTimer()
	lat := measureLatency(b, h, b.N, func(on bool) {
		if on {
			b.StartTimer()
		} else {
			b.StopTimer()
		}
	})
	b.StopTimer()
	b.ReportMetric(float64(percentile(lat, 0.5).Microseconds())/1000, "p50-ms")
	b.ReportMetric(float64(percentile(lat, 0.99).Microseconds())/1000, "p99-ms")
	b.ReportMetric(float64(slices.Max(lat).Microseconds())/1000, "max-ms")
}

// TestInputLatency16Panes checks the M3 budget. Timing under the race
// detector or -short says nothing, so it only runs in plain test runs.
func TestInputLatency16Panes(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("latency is measured without -race and -short")
	}
	h := sixteenPanes(t)
	measureLatency(t, h, 10, func(bool) {}) // warm up
	lat := measureLatency(t, h, 150, func(bool) {})
	p50, p99 := percentile(lat, 0.5), percentile(lat, 0.99)
	t.Logf("keystroke to frame with 16 panes: p50 %v, p99 %v, max %v", p50, p99, slices.Max(lat))
	if p99 > latencyBudget {
		t.Errorf("p99 latency %v exceeds %v", p99, latencyBudget)
	}
}
