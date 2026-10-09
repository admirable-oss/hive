package process_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
)

// captureRunner records the command it was asked to start. Its processes
// exit at once.
type captureRunner struct{ cmd process.Command }

func (r *captureRunner) Start(_ context.Context, cmd process.Command) (process.Handle, error) {
	r.cmd = cmd
	return &fakeHandle{pid: 1}, nil
}

func envMap(entries []string) map[string]string {
	m := map[string]string{}
	for _, kv := range entries {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestStart_BuildsTheEnvironmentInLayers(t *testing.T) {
	root := t.TempDir()
	envs := environment.NewService(environment.NewFilesystemStore(root))
	ctx := context.Background()
	if _, err := envs.Create(ctx, environment.CreateRequest{ID: "e", Env: map[string]string{"SHARED": "env", "ENV_ONLY": "1"}}); err != nil {
		t.Fatal(err)
	}
	runner := &captureRunner{}
	launch := process.LaunchEnv{
		Base: []string{"PATH=/bin", "SHARED=daemon", "TMUX=/tmp/tmux-1/default,1,0", "TMUX_PANE=%1", "ZELLIJ_SESSION_NAME=z", "HIVE_PANE_ID=stale", "KITTY_WINDOW_ID=3", "TERM=screen"},
		Vars: map[string]string{"HIVE_SOCKET_PATH": "/s.sock", "HIVE_BIN": "/bin/hive"},
	}
	svc := process.NewService(process.NewFilesystemStore(root), envs, runner, nil, process.WithLaunchEnv(launch))
	settleProcesses(t, svc)
	p, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "e", Command: "agent", Env: map[string]string{"SHARED": "request", "HIVE_PANE_ID": "p1"}})
	if err != nil {
		t.Fatal(err)
	}
	got := envMap(runner.cmd.Env)
	want := map[string]string{
		"PATH": "/bin", "SHARED": "request", "ENV_ONLY": "1",
		"HIVE_PANE_ID": "p1", "HIVE_SOCKET_PATH": "/s.sock", "HIVE_BIN": "/bin/hive",
		"HIVE_PROCESS_ID": p.ID, "HIVE_ENV_ID": "e",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"TMUX", "TMUX_PANE", "ZELLIJ_SESSION_NAME", "KITTY_WINDOW_ID", "TERM"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not leak into agents", k)
		}
	}
	if !slices.IsSorted(runner.cmd.Env) {
		t.Error("the environment should be in a stable order")
	}
	if p.Env["SHARED"] != "request" || len(p.Env) != 2 {
		t.Errorf("the record keeps only the requested variables, got %v", p.Env)
	}
}

func TestStart_WorkingDirectory(t *testing.T) {
	root := t.TempDir()
	envs := environment.NewService(environment.NewFilesystemStore(root))
	ctx := context.Background()
	env, err := envs.Create(ctx, environment.CreateRequest{ID: "e"})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Mkdir(filepath.Join(env.Path, "sub"), 0o755)
	other := t.TempDir()
	runner := &captureRunner{}
	svc := process.NewService(process.NewFilesystemStore(root), envs, runner, nil)
	settleProcesses(t, svc)

	for cwd, want := range map[string]string{"": env.Path, "sub": filepath.Join(env.Path, "sub"), other: other} {
		if _, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "e", Command: "x", Cwd: cwd}); err != nil {
			t.Fatalf("cwd %q: %v", cwd, err)
		}
		if runner.cmd.WorkingDir != want {
			t.Errorf("cwd %q ran in %q, want %q", cwd, runner.cmd.WorkingDir, want)
		}
	}
	if _, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "e", Command: "x", Cwd: "missing"}); !errors.Is(err, process.ErrInvalidCwd) {
		t.Fatalf("missing cwd: %v", err)
	}
	if _, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "e", Command: "x", Env: map[string]string{"A-B": "1"}}); !errors.Is(err, process.ErrInvalidEnv) {
		t.Fatalf("bad variable: %v", err)
	}
}

func TestMove_RunningProcessKeepsRunningAndRecordsItsExitInTheNewPlace(t *testing.T) {
	root := t.TempDir()
	envs := environment.NewService(environment.NewFilesystemStore(root))
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := envs.Create(ctx, environment.CreateRequest{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	exit := make(chan struct{})
	runner := &fakeRunner{start: func(context.Context, process.Command) (process.Handle, error) {
		return &fakeHandle{pid: 7, wait: func() error { <-exit; return nil }, kill: func() error { close(exit); return nil }}, nil
	}}
	store := process.NewFilesystemStore(root)
	svc := process.NewService(store, envs, runner, nil)
	p, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "a", Command: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := store.LogPaths(p)
	_ = os.WriteFile(stdout, []byte("before move\n"), 0o600)

	moved, err := svc.Move(ctx, p.ID, "b")
	if err != nil || moved.EnvironmentID != "b" {
		t.Fatalf("move: %+v, %v", moved, err)
	}
	if list, _ := svc.List(ctx, "a"); len(list) != 0 {
		t.Fatal("the process left environment a")
	}
	if logs, _ := svc.Logs(ctx, process.LogsRequest{ID: p.ID}); logs != "before move" {
		t.Fatalf("logs moved with it, got %q", logs)
	}
	if _, err := svc.Move(ctx, p.ID, "missing"); !errors.Is(err, environment.ErrNotFound) {
		t.Fatalf("move to a missing environment: %v", err)
	}

	_ = svc.Stop(ctx, p.ID)
	waitFor(t, func() bool {
		got, err := svc.Get(ctx, p.ID)
		return err == nil && got.Status == process.StatusKilled && got.EnvironmentID == "b"
	})
}
