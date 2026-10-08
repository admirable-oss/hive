package process_test

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/process"
)

// orphanFake records which process groups the service tried to stop.
type orphanFake struct {
	mu         sync.Mutex
	info       map[int]platform.ProcessInfo
	terminated []int
}

func (f *orphanFake) lookup(pid int) (platform.ProcessInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.info[pid]
	if !ok {
		return platform.ProcessInfo{}, platform.ErrNoProcess
	}
	return info, nil
}

func (f *orphanFake) terminate(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminated = append(f.terminated, pid)
	return nil
}

func TestService_RecoverReapsOnlyProvableOrphans(t *testing.T) {
	started := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		info       *platform.ProcessInfo // nil: no process with that PID
		wantKilled bool
	}{
		{"still running", &platform.ProcessInfo{PID: 4242, PGID: 4242, StartTime: started.Add(50 * time.Millisecond)}, true},
		{"coarse clock slightly early", &platform.ProcessInfo{PID: 4242, PGID: 4242, StartTime: started.Add(-time.Second)}, true},
		{"already gone", nil, false},
		{"pid reused inside another group", &platform.ProcessInfo{PID: 4242, PGID: 1, StartTime: started}, false},
		{"pid reused later (after reboot)", &platform.ProcessInfo{PID: 4242, PGID: 4242, StartTime: started.Add(time.Hour)}, false},
		{"pid older than the record", &platform.ProcessInfo{PID: 4242, PGID: 4242, StartTime: started.Add(-time.Hour)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			envs := newEnv(t, root, "env")
			store := process.NewFilesystemStore(root)
			stale := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", PID: 4242, Status: process.StatusRunning, StartedAt: started}
			if err := store.Create(context.Background(), stale); err != nil {
				t.Fatal(err)
			}
			fake := &orphanFake{info: map[int]platform.ProcessInfo{}}
			if tt.info != nil {
				fake.info[4242] = *tt.info
			}
			svc := process.NewService(store, envs, &fakeRunner{}, nil, process.WithOrphanControl(fake.lookup, fake.terminate))
			if err := svc.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}

			got, _ := svc.Get(context.Background(), stale.ID)
			wantStatus, wantTerminated := process.StatusFailed, 0
			if tt.wantKilled {
				wantStatus, wantTerminated = process.StatusKilled, 1
			}
			if got.Status != wantStatus || got.EndedAt == nil {
				t.Fatalf("status %s (ended %v), want %s", got.Status, got.EndedAt, wantStatus)
			}
			if len(fake.terminated) != wantTerminated {
				t.Fatalf("terminated %v, want %d call(s)", fake.terminated, wantTerminated)
			}
		})
	}
}

func TestService_StopOnStaleRecordReapsOrphan(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	now := time.Now()
	stale := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", PID: 777, Status: process.StatusRunning, StartedAt: now}
	if err := store.Create(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	fake := &orphanFake{info: map[int]platform.ProcessInfo{777: {PID: 777, PGID: 777, StartTime: now}}}
	svc := process.NewService(store, envs, &fakeRunner{}, nil, process.WithOrphanControl(fake.lookup, fake.terminate))

	if err := svc.Stop(context.Background(), stale.ID); err != nil {
		t.Fatal(err)
	}
	if len(fake.terminated) != 1 || fake.terminated[0] != 777 {
		t.Fatalf("expected group 777 to be stopped, got %v", fake.terminated)
	}
	if got, _ := svc.Get(context.Background(), stale.ID); got.Status != process.StatusKilled {
		t.Fatalf("status %s, want killed", got.Status)
	}
}

// TestService_RecoverStopsARealOrphan simulates a daemon crash with a real
// plain process: its record says running and its group is still alive.
func TestService_RecoverStopsARealOrphan(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)

	startedAt := time.Now()
	cmd := exec.Command("sh", "-c", "sleep 30 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); <-reaped })

	orphan := process.Process{ID: "00000000000000aa", EnvironmentID: "env", PID: pid, Status: process.StatusRunning, StartedAt: startedAt}
	if err := store.Create(context.Background(), orphan); err != nil {
		t.Fatal(err)
	}
	svc := process.NewService(store, envs, &fakeRunner{}, nil)
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for pgroup.Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("orphaned group %d still alive after recovery", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got, _ := svc.Get(context.Background(), orphan.ID); got.Status != process.StatusKilled {
		t.Fatalf("status %s, want killed", got.Status)
	}
}
