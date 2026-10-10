package terminal_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// recorder is a Watcher that keeps what it was told.
type recorder struct {
	mu     sync.Mutex
	frames []time.Time
	keys   int
	ended  chan struct{}
}

func (r *recorder) Frame(id string, f *vt.Frame) {
	if id != "busy" {
		panic("frame for " + id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, time.Now())
	if f.Keyframe {
		r.keys++
	}
}

func (r *recorder) Ended(string) { close(r.ended) }

func TestWatcherGetsEveryScreenAtMostOncePerInterval(t *testing.T) {
	rec := &recorder{ended: make(chan struct{})}
	svc := terminal.NewService(terminal.PTYFactory{}, terminal.WithWatcher(rec, 100*time.Millisecond))
	count := func() int { rec.mu.Lock(); defer rec.mu.Unlock(); return len(rec.frames) }

	// Quiet at first, then printing fast.
	_, err := svc.Open(context.Background(), "busy", terminal.Command{
		Path: "/bin/sh", Args: []string{"-c", "sleep 0.3; while :; do echo x; sleep 0.005; done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := count(); n != 1 {
		t.Fatalf("%d frames before any output; want the first screen only", n)
	}
	time.Sleep(700 * time.Millisecond)
	n := count()
	if n < 4 || n > 10 {
		t.Fatalf("%d frames for 0.6s of output at a 100ms interval", n)
	}
	rec.mu.Lock()
	if rec.keys < 1 {
		t.Error("the first frame is a keyframe")
	}
	for i := 2; i < len(rec.frames); i++ {
		if gap := rec.frames[i].Sub(rec.frames[i-1]); gap < 90*time.Millisecond {
			t.Errorf("frames %v apart", gap)
		}
	}
	rec.mu.Unlock()

	_ = svc.Close("busy")
	select {
	case <-rec.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher is told the session ended")
	}
}
