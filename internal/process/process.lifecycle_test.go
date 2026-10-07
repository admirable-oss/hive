package process_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
)

func newEnv(t *testing.T, root, id string) environment.Service {
	t.Helper()
	envs := environment.NewService(environment.NewFilesystemStore(root))
	if _, err := envs.Create(context.Background(), id); err != nil {
		t.Fatalf("create env: %v", err)
	}
	return envs
}

func TestStore_TailReadsOnlyTheEnd(t *testing.T) {
	root := t.TempDir()
	newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	p := process.Process{ID: "0123456789abcdef", EnvironmentID: "env"}
	if err := store.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}

	var log strings.Builder
	for i := range 5000 { // well past one 8 KiB read block
		fmt.Fprintf(&log, "line %d\n", i)
	}
	stdout, _ := store.LogPaths(p)
	if err := os.WriteFile(stdout, []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := store.Tail(context.Background(), p, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := "line 4997\nline 4998\nline 4999"; got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
	if got, _ := store.Tail(context.Background(), p, 10_000); strings.Count(got, "\n") != 4999 {
		t.Fatalf("asking for more lines than exist should return the whole log")
	}
}

func TestStore_ListIsSortedByStartTime(t *testing.T) {
	root := t.TempDir()
	newEnv(t, root, "a")
	newEnv(t, root, "b")
	store := process.NewFilesystemStore(root)
	base := time.Now()
	// IDs sort opposite to start time, so directory order would be wrong.
	for i, p := range []process.Process{
		{ID: "ffffffffffffff01", EnvironmentID: "a", StartedAt: base},
		{ID: "eeeeeeeeeeeeee02", EnvironmentID: "b", StartedAt: base.Add(time.Second)},
		{ID: "dddddddddddddd03", EnvironmentID: "a", StartedAt: base.Add(2 * time.Second)},
	} {
		if err := store.Create(context.Background(), p); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	all, err := store.List(context.Background(), "")
	if err != nil || len(all) != 3 {
		t.Fatalf("list all: %v (%d)", err, len(all))
	}
	for i, want := range []string{"ffffffffffffff01", "eeeeeeeeeeeeee02", "dddddddddddddd03"} {
		if all[i].ID != want {
			t.Fatalf("position %d: got %s, want %s", i, all[i].ID, want)
		}
	}
	if inA, _ := store.List(context.Background(), "a"); len(inA) != 2 {
		t.Fatalf("expected 2 processes in env a, got %d", len(inA))
	}
}

func TestService_RecoverClosesOutStaleRecords(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	stale := process.Process{ID: "0123456789abcdef", EnvironmentID: "env", Status: process.StatusRunning}
	if err := store.Create(context.Background(), stale); err != nil {
		t.Fatal(err)
	}

	svc := process.NewService(store, envs, &fakeRunner{}, nil)
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	got, _ := svc.Get(context.Background(), stale.ID)
	if got.Status != process.StatusFailed || got.EndedAt == nil {
		t.Fatalf("expected stale record to be failed with an end time, got %+v", got)
	}
}

func TestService_StopEnvironmentStopsOnlyThatEnvironment(t *testing.T) {
	root := t.TempDir()
	envs := newEnv(t, root, "a")
	if _, err := envs.Create(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}

	// Each fake process runs until killed.
	runner := &fakeRunner{start: func(context.Context, process.Command) (process.Handle, error) {
		exit := make(chan struct{})
		return &fakeHandle{
			pid:  300,
			wait: func() error { <-exit; return nil },
			kill: func() error { close(exit); return nil },
		}, nil
	}}
	svc := process.NewService(process.NewFilesystemStore(root), envs, runner, nil)
	ctx := context.Background()

	inA, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "a", Command: "sleep"})
	if err != nil {
		t.Fatal(err)
	}
	inB, err := svc.Start(ctx, process.StartRequest{EnvironmentID: "b", Command: "sleep"})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.StopEnvironment(ctx, "a"); err != nil {
		t.Fatalf("stop environment: %v", err)
	}
	if got, _ := svc.Get(ctx, inA.ID); got.Status != process.StatusKilled {
		t.Fatalf("process in a: expected killed, got %s", got.Status)
	}
	if got, _ := svc.Get(ctx, inB.ID); got.Status != process.StatusRunning {
		t.Fatalf("process in b: expected running, got %s", got.Status)
	}
	_ = svc.StopAll(ctx)
}
