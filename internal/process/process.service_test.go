package process_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
)

type fakeHandle struct {
	pid  int
	wait func() error
	kill func() error
}

func (h *fakeHandle) PID() int { return h.pid }
func (h *fakeHandle) Wait() error {
	if h.wait != nil {
		return h.wait()
	}
	return nil
}

func (h *fakeHandle) Kill() error {
	if h.kill != nil {
		return h.kill()
	}
	return nil
}

type testExitError struct {
	code int
}

func (e testExitError) Error() string { return "exit status" }
func (e testExitError) ExitCode() int { return e.code }

type fakeRunner struct {
	mu    sync.Mutex
	start func(ctx context.Context, cmd process.Command) (process.Handle, error)
	cmds  []process.Command
}

func (r *fakeRunner) Start(ctx context.Context, cmd process.Command) (process.Handle, error) {
	r.mu.Lock()
	r.cmds = append(r.cmds, cmd)
	r.mu.Unlock()
	if r.start != nil {
		return r.start(ctx, cmd)
	}
	return &fakeHandle{pid: 1234}, nil
}

func TestProcessService(t *testing.T) {
	tempDir := t.TempDir()
	envStore := environment.NewFilesystemStore(tempDir)
	envSvc := environment.NewService(envStore)

	env, err := envSvc.Create(context.Background(), environment.CreateRequest{ID: "testenv"})
	if err != nil {
		t.Fatalf("failed to create env: %v", err)
	}

	store := process.NewFilesystemStore(tempDir)
	runner := &fakeRunner{}
	svc := process.NewService(store, envSvc, runner, nil)
	ctx := context.Background()

	t.Run("start process", func(t *testing.T) {
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			return &fakeHandle{pid: 100}, nil
		}
		p, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "echo",
			Args:          []string{"hello"},
		})
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if p.ID == "" {
			t.Errorf("expected non-empty process ID")
		}
		if p.PID != 100 {
			t.Errorf("expected PID 100, got %d", p.PID)
		}
	})

	t.Run("process receives environment working directory", func(t *testing.T) {
		var receivedDir string
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			receivedDir = cmd.WorkingDir
			return &fakeHandle{pid: 101}, nil
		}
		_, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "pwd",
		})
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if receivedDir != env.Path {
			t.Errorf("expected working dir %q, got %q", env.Path, receivedDir)
		}
	})

	t.Run("process receives arguments", func(t *testing.T) {
		var receivedArgs []string
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			receivedArgs = cmd.Args
			return &fakeHandle{pid: 102}, nil
		}
		_, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "echo",
			Args:          []string{"arg1", "arg2"},
		})
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if len(receivedArgs) != 2 || receivedArgs[0] != "arg1" || receivedArgs[1] != "arg2" {
			t.Errorf("unexpected args: %v", receivedArgs)
		}
	})

	t.Run("process appears in store", func(t *testing.T) {
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			return &fakeHandle{pid: 103}, nil
		}
		p, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "echo",
		})
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		stored, err := store.Get(ctx, p.ID)
		if err != nil {
			t.Fatalf("store.Get failed: %v", err)
		}
		if stored.ID != p.ID {
			t.Errorf("expected stored ID %s, got %s", p.ID, stored.ID)
		}
	})

	t.Run("process transitions running to exited", func(t *testing.T) {
		waitCalled := make(chan struct{})
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			return &fakeHandle{
				pid: 104,
				wait: func() error {
					close(waitCalled)
					return nil
				},
			}, nil
		}
		p, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "echo",
		})
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		<-waitCalled
		time.Sleep(20 * time.Millisecond)

		got, err := svc.Get(ctx, p.ID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status != process.StatusExited {
			t.Errorf("expected status %s, got %s", process.StatusExited, got.Status)
		}
	})

	t.Run("exit code recorded", func(t *testing.T) {
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			return &fakeHandle{
				pid: 105,
				wait: func() error {
					return testExitError{code: 1}
				},
			}, nil
		}
		p, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "exit",
			Args:          []string{"1"},
		})
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)

		got, err := svc.Get(ctx, p.ID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.ExitCode == nil {
			t.Errorf("expected exit code to be recorded")
		}
		if got.Status != process.StatusExited {
			t.Errorf("expected status %s, got %s", process.StatusExited, got.Status)
		}
	})

	t.Run("stop running process", func(t *testing.T) {
		killed := false
		waitCh := make(chan struct{})
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			return &fakeHandle{
				pid: 106,
				wait: func() error {
					<-waitCh
					return errors.New("signal: killed")
				},
				kill: func() error {
					killed = true
					close(waitCh)
					return nil
				},
			}, nil
		}

		p, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "sleep",
		})
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		err = svc.Stop(ctx, p.ID)
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}

		time.Sleep(20 * time.Millisecond)
		if !killed {
			t.Errorf("expected kill to be called")
		}

		got, err := svc.Get(ctx, p.ID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status != process.StatusKilled {
			t.Errorf("expected status %s, got %s", process.StatusKilled, got.Status)
		}
	})

	t.Run("unknown process", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if !errors.Is(err, process.ErrNotFound) {
			t.Errorf("expected ErrNotFound for Get, got %v", err)
		}
		err = svc.Stop(ctx, "nonexistent")
		if !errors.Is(err, process.ErrNotFound) {
			t.Errorf("expected ErrNotFound for Stop, got %v", err)
		}
	})

	t.Run("unknown environment", func(t *testing.T) {
		_, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "nonexistent",
			Command:       "echo",
		})
		if err == nil {
			t.Errorf("expected error for unknown environment")
		}
	})

	t.Run("failed process start", func(t *testing.T) {
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			return nil, errors.New("binary not found")
		}
		p, err := svc.Start(ctx, process.StartRequest{
			EnvironmentID: "testenv",
			Command:       "nonexistent-binary",
		})
		if err == nil {
			t.Errorf("expected error on failed process start")
		}
		if p.Status != process.StatusFailed {
			t.Errorf("expected status %s, got %s", process.StatusFailed, p.Status)
		}
	})

	t.Run("concurrent process starts", func(t *testing.T) {
		runner.start = func(ctx context.Context, cmd process.Command) (process.Handle, error) {
			return &fakeHandle{pid: 200}, nil
		}

		var wg sync.WaitGroup
		errs := make(chan error, 10)
		ids := make(chan string, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				proc, err := svc.Start(ctx, process.StartRequest{
					EnvironmentID: "testenv",
					Command:       "echo",
				})
				if err != nil {
					errs <- err
					return
				}
				ids <- proc.ID
			}()
		}
		wg.Wait()
		close(errs)
		close(ids)

		for err := range errs {
			t.Errorf("concurrent start error: %v", err)
		}

		seen := make(map[string]bool)
		for id := range ids {
			if seen[id] {
				t.Errorf("duplicate process ID generated: %s", id)
			}
			seen[id] = true
		}
		if len(seen) != 10 {
			t.Errorf("expected 10 unique processes, got %d", len(seen))
		}
	})
}
