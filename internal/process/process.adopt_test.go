package process_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/process"
)

// adoptable is a handle that survives the daemon: it can be detached and
// released, like a shim.
type adoptable struct {
	pid      int
	exit     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	released bool
	detached bool
}

func newAdoptable(pid int) *adoptable { return &adoptable{pid: pid, exit: make(chan struct{})} }

func (h *adoptable) PID() int { return h.pid }

func (h *adoptable) Wait() error {
	<-h.exit
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.detached {
		return errors.New("detached")
	}
	return nil
}

func (h *adoptable) Kill() error { h.once.Do(func() { close(h.exit) }); return nil }

func (h *adoptable) Release() {
	h.mu.Lock()
	h.released = true
	h.mu.Unlock()
}

func (h *adoptable) Detach() {
	h.mu.Lock()
	h.detached = true
	h.mu.Unlock()
	h.once.Do(func() { close(h.exit) })
}

func (h *adoptable) wasReleased() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.released
}

type fakeAdopter struct {
	adopt func(process.Process) (process.Handle, error)
}

func (a fakeAdopter) Adopt(_ context.Context, p process.Process) (process.Handle, error) {
	return a.adopt(p)
}

type exitCode int

func (e exitCode) Error() string { return "exited" }
func (e exitCode) ExitCode() int { return int(e) }

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) Publish(typ string, _ any) {
	r.mu.Lock()
	r.events = append(r.events, typ)
	r.mu.Unlock()
}

func (r *recorder) has(typ string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e == typ {
			return true
		}
	}
	return false
}

func TestService_RecoverAdoptsSurvivingProcesses(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	ctx := context.Background()
	alive := process.Process{ID: "00000000000000a1", EnvironmentID: "env", PID: 11, Status: process.StatusRunning, StartedAt: time.Now()}
	finished := process.Process{ID: "00000000000000a2", EnvironmentID: "env", PID: 12, Status: process.StatusRunning, StartedAt: time.Now()}
	killed := process.Process{ID: "00000000000000a3", EnvironmentID: "env", PID: 13, Status: process.StatusRunning, StartedAt: time.Now()}
	for _, p := range []process.Process{alive, finished, killed} {
		if err := store.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	survivor := newAdoptable(11)
	adopter := fakeAdopter{adopt: func(p process.Process) (process.Handle, error) {
		switch p.ID {
		case alive.ID:
			return survivor, nil
		case finished.ID:
			return nil, exitCode(4)
		default:
			return nil, exitCode(-1)
		}
	}}
	events := &recorder{}
	svc := process.NewService(store, envs, &fakeRunner{}, nil, process.WithAdopter(adopter), process.WithEvents(events))
	if err := svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if got, _ := svc.Get(ctx, alive.ID); got.Status != process.StatusRunning {
		t.Fatalf("surviving process: %s, want running", got.Status)
	}
	if got, _ := svc.Get(ctx, finished.ID); got.Status != process.StatusExited || *got.ExitCode != 4 {
		t.Fatalf("finished process: %s %v, want exited 4", got.Status, got.ExitCode)
	}
	if got, _ := svc.Get(ctx, killed.ID); got.Status != process.StatusKilled {
		t.Fatalf("signalled process: %s, want killed", got.Status)
	}
	if !events.has(process.EventRecovered) {
		t.Fatal("recovery should publish process.recovered")
	}

	// The adopted process is supervised like any other: stopping it records
	// the exit and releases its handle.
	if err := svc.Stop(ctx, alive.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		got, _ := svc.Get(ctx, alive.ID)
		return got.Status == process.StatusKilled && survivor.wasReleased()
	})
	if !events.has(process.EventExited) {
		t.Fatal("exit should publish process.exited")
	}
}

func TestService_DetachLeavesProcessesRunning(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	h := newAdoptable(21)
	runner := &fakeRunner{start: func(context.Context, process.Command) (process.Handle, error) { return h, nil }}
	svc := process.NewService(store, envs, runner, nil)
	ctx := context.Background()
	p, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "env", Command: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := svc.Detach(ctx)
	if err != nil || !kept {
		t.Fatalf("Detach = %v, %v; want kept", kept, err)
	}
	// The record still says running, for the next daemon to adopt.
	if got, _ := svc.Get(ctx, p.ID); got.Status != process.StatusRunning {
		t.Fatalf("status after detach = %s, want running", got.Status)
	}
	if h.wasReleased() {
		t.Fatal("a detached process must not be released")
	}
}

func TestService_DetachStopsProcessesThatCannotSurvive(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	exit := make(chan struct{})
	runner := &fakeRunner{start: func(context.Context, process.Command) (process.Handle, error) {
		return &fakeHandle{pid: 5, wait: func() error { <-exit; return nil }, kill: func() error { close(exit); return nil }}, nil
	}}
	svc := process.NewService(process.NewFilesystemStore(root), envs, runner, nil)
	ctx := context.Background()
	p, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "env", Command: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := svc.Detach(ctx)
	if err != nil || kept {
		t.Fatalf("Detach = %v, %v; want stopped", kept, err)
	}
	if got, _ := svc.Get(ctx, p.ID); got.Status != process.StatusKilled {
		t.Fatalf("status = %s, want killed", got.Status)
	}
}

// TestService_DetachStopsInProcessTerminals guards a bug: terminal handles
// forward Detach to their session, so the session (not the handle) decides
// whether the process can outlive the daemon.
func TestService_DetachStopsInProcessTerminals(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	exit := make(chan struct{})
	var once sync.Once
	stopFn := func() { once.Do(func() { close(exit) }) }
	svc := process.NewService(process.NewFilesystemStore(root), envs, &fakeRunner{}, fakeTerminals{exit: exit, stop: stopFn})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "env", Command: "agent", Terminal: true})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := svc.Detach(ctx)
	if err != nil || kept {
		t.Fatalf("Detach = %v, %v; an in-process terminal cannot outlive the daemon", kept, err)
	}
	if got, _ := svc.Get(ctx, p.ID); got.Status != process.StatusKilled {
		t.Fatalf("status = %s, want killed", got.Status)
	}
}
