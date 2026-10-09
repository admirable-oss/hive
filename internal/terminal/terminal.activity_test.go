package terminal_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
)

func TestActivityReportsOutputAtMostOncePerInterval(t *testing.T) {
	var mu sync.Mutex
	var calls []time.Time
	svc := terminal.NewService(terminal.PTYFactory{}, terminal.WithActivity(func(id string) {
		if id != "busy" {
			t.Errorf("activity for %q", id)
		}
		mu.Lock()
		calls = append(calls, time.Now())
		mu.Unlock()
	}, 100*time.Millisecond))
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(calls) }

	// Quiet at first (the first screen is not news), then printing fast.
	_, err := svc.Open(context.Background(), "busy", terminal.Command{
		Path: "/bin/sh", Args: []string{"-c", "sleep 0.3; while :; do echo x; sleep 0.005; done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close("busy") }()

	time.Sleep(200 * time.Millisecond)
	if n := count(); n != 0 {
		t.Fatalf("%d reports before any output", n)
	}
	time.Sleep(700 * time.Millisecond)
	n := count()
	if n < 3 || n > 8 {
		t.Fatalf("%d reports for 0.6s of output at a 100ms interval", n)
	}
	mu.Lock()
	for i := 1; i < len(calls); i++ {
		if gap := calls[i].Sub(calls[i-1]); gap < 90*time.Millisecond {
			t.Errorf("reports %v apart", gap)
		}
	}
	mu.Unlock()
}
