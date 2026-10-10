package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/persist"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/runtime"
)

// TestRuntime_GCRemovesOnlyWhatNobodyNeeds: ended processes older than the
// retention go, with their logs; running ones, ones a pane shows, and
// recent ones stay. Shims' leftovers and unreadable files moved aside long
// ago go too.
func TestRuntime_GCRemovesOnlyWhatNobodyNeeds(t *testing.T) {
	path := shortSock(t)
	root := filepath.Dir(path)
	old := time.Now().Add(-48 * time.Hour)
	record := func(id string, status process.Status, ended time.Time) {
		t.Helper()
		dir := filepath.Join(root, "environments", "dev", "processes", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := process.Process{ID: id, EnvironmentID: "dev", Command: "sh", Status: status, StartedAt: ended.Add(-time.Minute), EndedAt: &ended}
		if err := jsonfile.Write(filepath.Join(dir, "process.json"), p); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stdout.log"), make([]byte, 1000), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mod := runtime.NewModule(runtime.Config{SocketPath: path, Retention: runtime.RetentionConfig{ExitedProcesses: 24 * time.Hour}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := mod.Service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.Stop(context.Background()) })
	if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "dev"}); err != nil {
		t.Fatal(err)
	}
	record("00000000000000a1", process.StatusExited, old)                 // goes
	record("00000000000000a2", process.StatusExited, time.Now())          // too recent
	record("00000000000000a3", process.StatusFailed, old.Add(-time.Hour)) // goes
	// A pane shows an ended process: it stays however old.
	_, shown, err := mod.Panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Pane: pane.Spec{Command: []string{"true"}}})
	if err != nil {
		t.Fatal(err)
	}
	// A shim directory without a live process, and an unreadable file
	// moved aside long ago.
	leftover := filepath.Join(root, "run", "00000000000000ff")
	backup := filepath.Join(root, "session.json"+persist.CorruptSuffix+"x")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{leftover, backup} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	running, err := mod.Processes.Start(ctx, process.StartRequest{EnvironmentID: "dev", Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatal(err)
	}

	sessions := client.NewSessions(client.NewService(client.Config{SocketPath: path}))
	dry, err := sessions.GC(ctx, persist.GCRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(dry.Processes)
	if !slices.Equal(dry.Processes, []string{"00000000000000a1", "00000000000000a3"}) || len(dry.Files) != 2 || dry.Bytes < 2000 {
		t.Fatalf("dry run = %+v", dry)
	}
	if _, err := mod.Processes.Get(ctx, "00000000000000a1"); err != nil {
		t.Fatalf("a dry run removed a process: %v", err)
	}

	done, err := sessions.GC(ctx, persist.GCRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Processes) != 2 || len(done.Files) != 2 {
		t.Fatalf("gc = %+v", done)
	}
	for _, id := range []string{"00000000000000a2", shown.ProcessID, running.ID} {
		if _, err := mod.Processes.Get(ctx, id); err != nil {
			t.Errorf("process %s was removed: %v", id, err)
		}
	}
	for _, p := range []string{leftover, backup, filepath.Join(root, "environments", "dev", "processes", "00000000000000a1")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", p, err)
		}
	}
	// OlderThan narrows the retention; a pane's process stays all the same.
	for {
		if p, err := mod.Processes.Get(ctx, shown.ProcessID); err == nil && !p.Active() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	now, err := sessions.GC(ctx, persist.GCRequest{OlderThan: time.Nanosecond, DryRun: true})
	if err != nil || !slices.Contains(now.Processes, "00000000000000a2") || slices.Contains(now.Processes, running.ID) || slices.Contains(now.Processes, shown.ProcessID) {
		t.Fatalf("gc --older-than = %+v, %v", now, err)
	}
	_ = mod.Processes.Stop(ctx, running.ID)
}
