package process_test

import (
	"context"
	"sync"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
)

func newEnv(t *testing.T, root, id string) environment.Service {
	t.Helper()
	envs := environment.NewService(environment.NewFilesystemStore(root))
	if _, err := envs.Create(context.Background(), environment.CreateRequest{ID: id}); err != nil {
		t.Fatalf("create env: %v", err)
	}
	return envs
}

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
