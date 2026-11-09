//go:build !windows

package process_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
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
			svc := process.NewService(store, envs, &fakeRunner{}, nil, process.WithOrphanControl(fake.lookup, fake.terminate),
				process.WithBootTime(func() (time.Time, error) { return started.Add(-time.Hour), nil }))
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

// TestService_RecoverMarksLostProcesses checks which closed-out records
// count as lost (to be restored): those whose shim vanished, and any that
// started before the host last booted, whatever their shim recorded.
func TestService_RecoverMarksLostProcesses(t *testing.T) {
	boot := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		started    time.Time
		adoptErr   error
		wantStatus process.Status
		wantLost   bool
	}{
		{"shim gone in this boot", boot.Add(time.Hour), errors.New("shim gone"), process.StatusKilled, true}, // its orphan is stopped
		{"exited on its own in this boot", boot.Add(time.Hour), exitCode(0), process.StatusExited, false},
		{"killed by a signal in this boot", boot.Add(time.Hour), exitCode(-1), process.StatusKilled, false},
		{"shim recorded an exit before the reboot", boot.Add(-time.Hour), exitCode(-1), process.StatusKilled, true},
		{"shim gone after a reboot", boot.Add(-time.Hour), errors.New("shim gone"), process.StatusFailed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			envs := newEnv(t, root, "env")
			store := process.NewFilesystemStore(root)
			rec := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", PID: 4242, Status: process.StatusRunning, StartedAt: tt.started}
			if err := store.Create(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
			// PID 4242 now belongs to a process that looks like ours: after a
			// reboot it must be left alone all the same.
			fake := &orphanFake{info: map[int]platform.ProcessInfo{4242: {PID: 4242, PGID: 4242, StartTime: tt.started}}}
			svc := process.NewService(store, envs, &fakeRunner{}, nil,
				process.WithAdopter(fakeAdopter{adopt: func(process.Process) (process.Handle, error) { return nil, tt.adoptErr }}),
				process.WithOrphanControl(fake.lookup, fake.terminate),
				process.WithBootTime(func() (time.Time, error) { return boot, nil }))
			if err := svc.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := svc.Get(context.Background(), rec.ID)
			if got.Status != tt.wantStatus {
				t.Fatalf("status %s, want %s", got.Status, tt.wantStatus)
			}
			if got.Lost != tt.wantLost {
				t.Fatalf("lost = %v, want %v", got.Lost, tt.wantLost)
			}
			if tt.started.Before(boot) && len(fake.terminated) > 0 {
				t.Fatalf("signalled pid %v after a reboot", fake.terminated)
			}
		})
	}
}

func TestService_RemoveOnlyEndedProcesses(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	ctx := context.Background()
	running := process.Process{ID: "00000000000000a1", EnvironmentID: "env", Status: process.StatusRunning, StartedAt: time.Now()}
	ended := process.Process{ID: "00000000000000a2", EnvironmentID: "env", Status: process.StatusExited, StartedAt: time.Now()}
	for _, p := range []process.Process{running, ended} {
		if err := store.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	svc := process.NewService(store, envs, &fakeRunner{}, nil)
	if err := svc.Remove(ctx, running.ID); !errors.Is(err, process.ErrActive) {
		t.Fatalf("removing a running process: %v", err)
	}
	if err := svc.Remove(ctx, ended.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, ended.ID); !errors.Is(err, process.ErrNotFound) {
		t.Fatalf("removed process still found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "env", "processes", ended.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("its directory is still there: %v", err)
	}
}

// TestService_ShimDeathReapsItsAgent covers a shim dying under a live
// daemon: supervision ends without an exit status while the agent may still
// run. Its group is stopped only when it provably is the recorded agent.
func TestService_ShimDeathReapsItsAgent(t *testing.T) {
	started := time.Now()
	tests := []struct {
		name       string
		info       *platform.ProcessInfo
		wantStatus process.Status
		wantKilled bool
	}{
		{"agent outlived its shim", &platform.ProcessInfo{PID: 4242, PGID: 4242, StartTime: started}, process.StatusKilled, true},
		{"agent died with its shim", nil, process.StatusFailed, false},
		{"pid reused by another process", &platform.ProcessInfo{PID: 4242, PGID: 1, StartTime: started}, process.StatusFailed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			envs := newEnv(t, root, "env")
			store := process.NewFilesystemStore(root)
			rec := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", PID: 4242, Status: process.StatusRunning, StartedAt: started}
			if err := store.Create(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
			fake := &orphanFake{info: map[int]platform.ProcessInfo{}}
			if tt.info != nil {
				fake.info[4242] = *tt.info
			}
			// Adopted, then the shim's connection breaks with no exit status.
			shimDied := make(chan struct{})
			handle := &fakeHandle{pid: 4242, wait: func() error { <-shimDied; return errors.New("shim gone") }}
			svc := process.NewService(store, envs, &fakeRunner{}, nil,
				process.WithAdopter(fakeAdopter{adopt: func(process.Process) (process.Handle, error) { return handle, nil }}),
				process.WithOrphanControl(fake.lookup, fake.terminate),
				process.WithBootTime(func() (time.Time, error) { return started.Add(-time.Hour), nil }))
			if err := svc.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			close(shimDied)

			var got process.Process
			deadline := time.Now().Add(5 * time.Second)
			for {
				got, _ = svc.Get(context.Background(), rec.ID)
				if !got.Active() || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status %s, want %s", got.Status, tt.wantStatus)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if killed := len(fake.terminated) == 1 && fake.terminated[0] == 4242; killed != tt.wantKilled {
				t.Fatalf("terminated %v, want killed=%v", fake.terminated, tt.wantKilled)
			}
		})
	}
}

// A recovered agent whose shim is alive but slow to answer (a frozen or
// overloaded machine) is kept and re-attached once it answers: never killed.
func TestService_UnresponsiveShimIsRetriedNotReaped(t *testing.T) {
	t.Cleanup(process.SetAdoptRetry(10*time.Millisecond, 20*time.Millisecond))
	started := time.Now()
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	rec := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", PID: 4242, Status: process.StatusRunning, StartedAt: started}
	if err := store.Create(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	fake := &orphanFake{info: map[int]platform.ProcessInfo{4242: {PID: 4242, PGID: 4242, StartTime: started}}}
	var mu sync.Mutex
	attempts := 0
	exit := make(chan struct{})
	handle := &fakeHandle{pid: 4242, wait: func() error { <-exit; return nil }}
	svc := process.NewService(store, envs, &fakeRunner{}, nil,
		process.WithAdopter(fakeAdopter{adopt: func(process.Process) (process.Handle, error) {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts < 4 {
				return nil, fmt.Errorf("%w: frozen", terminal.ErrUnresponsive)
			}
			return handle, nil
		}}),
		process.WithOrphanControl(fake.lookup, fake.terminate),
		process.WithBootTime(func() (time.Time, error) { return started.Add(-time.Hour), nil }))
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(context.Background(), rec.ID); got.Status != process.StatusRunning {
		t.Fatalf("status %s while its shim does not answer, want running", got.Status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := attempts
		mu.Unlock()
		if n >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d adoption attempts", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Re-attached: it is supervised again, and its exit is recorded.
	close(exit)
	for {
		got, _ := svc.Get(context.Background(), rec.ID)
		if got.Status == process.StatusExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status %s, want exited once supervised again", got.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.terminated) != 0 {
		t.Fatalf("signalled %v: a live agent was killed", fake.terminated)
	}
}

// `hive stop` while a shim never answered stops its agent the provable way,
// and the retry ends.
func TestService_StopAllEndsUnresponsiveRetries(t *testing.T) {
	t.Cleanup(process.SetAdoptRetry(10*time.Millisecond, 20*time.Millisecond))
	started := time.Now()
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	rec := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", PID: 4242, Status: process.StatusRunning, StartedAt: started}
	if err := store.Create(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	fake := &orphanFake{info: map[int]platform.ProcessInfo{4242: {PID: 4242, PGID: 4242, StartTime: started}}}
	svc := process.NewService(store, envs, &fakeRunner{}, nil,
		process.WithAdopter(fakeAdopter{adopt: func(process.Process) (process.Handle, error) {
			return nil, terminal.ErrUnresponsive
		}}),
		process.WithOrphanControl(fake.lookup, fake.terminate),
		process.WithBootTime(func() (time.Time, error) { return started.Add(-time.Hour), nil }))
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := svc.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(context.Background(), rec.ID); got.Status != process.StatusKilled {
		t.Fatalf("status %s, want killed", got.Status)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.terminated) != 1 || fake.terminated[0] != 4242 {
		t.Fatalf("terminated %v, want [4242]", fake.terminated)
	}
}

// A daemon that died between launching a process and recording it leaves
// it "starting" with no PID; re-attaching it completes the record.
func TestService_AdoptCompletesAnUnrecordedStart(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	rec := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", Status: process.StatusStarting, StartedAt: time.Now()}
	if err := store.Create(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	exit := make(chan struct{})
	defer close(exit)
	handle := &fakeHandle{pid: 777, wait: func() error { <-exit; return nil }}
	svc := process.NewService(store, envs, &fakeRunner{}, nil,
		process.WithAdopter(fakeAdopter{adopt: func(process.Process) (process.Handle, error) { return handle, nil }}))
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(context.Background(), rec.ID)
	if got.Status != process.StatusRunning || got.PID != 777 {
		t.Fatalf("status %s pid %d, want running 777", got.Status, got.PID)
	}
}
