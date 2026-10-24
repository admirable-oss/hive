package persist_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/persist"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func snap(env string) persist.Snapshot {
	return persist.Snapshot{Environments: []persist.Environment{{ID: env, Panes: []persist.Pane{{ID: "p1", Command: []string{"sh"}}}}}}
}

func TestSaveRotatesAndKeepsTheLastFew(t *testing.T) {
	dir := t.TempDir()
	st := persist.NewStore(dir, 3, nil)
	for i := range 6 {
		if err := st.Save(snap(string(rune('a' + i)))); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct modification times
	}
	got, err := st.Load()
	if err != nil || got.Environments[0].ID != "f" || got.Schema != persist.Schema {
		t.Fatalf("latest = %+v, %v", got, err)
	}
	rotated, err := st.Rotated()
	if err != nil || len(rotated) != 3 {
		t.Fatalf("rotated = %v, %v", rotated, err)
	}
	// Newest first: the snapshots before the latest, e then d then c.
	for i, want := range []string{"e", "d", "c"} {
		data, _ := os.ReadFile(rotated[i])
		if !strings.Contains(string(data), `"id": "`+want+`"`) {
			t.Fatalf("rotated[%d] is not %s:\n%s", i, want, data)
		}
	}
	if info, _ := os.Stat(st.Path()); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("session.json mode %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadBacksUpCorruptFilesAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	st := persist.NewStore(dir, 5, nil)
	if err := st.Save(snap("good")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := st.Save(snap("latest")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Path(), []byte(`{"schema": 1, "environments": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil || got.Environments[0].ID != "good" {
		t.Fatalf("fallback = %+v, %v", got, err)
	}
	backups, _ := filepath.Glob(st.Path() + persist.CorruptSuffix + "*")
	if len(backups) != 1 {
		t.Fatalf("corrupt file not backed up: %v", backups)
	}
	if _, err := os.Stat(st.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt file still in place: %v", err)
	}
}

func TestLoadSkipsNewerSchemasAndReportsNone(t *testing.T) {
	dir := t.TempDir()
	st := persist.NewStore(dir, 5, nil)
	if _, err := st.Load(); !errors.Is(err, persist.ErrNoSnapshot) {
		t.Fatalf("empty store: %v", err)
	}
	if err := os.WriteFile(st.Path(), []byte(`{"schema": 99, "environments": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(); !errors.Is(err, persist.ErrNoSnapshot) {
		t.Fatalf("newer schema: %v", err)
	}
	if _, err := os.Stat(st.Path()); err != nil {
		t.Fatalf("a newer Hive's snapshot must be left alone: %v", err)
	}
}

func TestSaverDebouncesAndSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	st := persist.NewStore(dir, 5, nil)
	var collected atomic.Int32
	var env atomic.Value
	env.Store("one")
	sv := persist.NewSaver(st, func(context.Context) (persist.Snapshot, error) {
		collected.Add(1)
		return snap(env.Load().(string)), nil
	}, 300*time.Millisecond, -1, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sv.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A burst of changes makes one snapshot. It is sent at once: spaced
	// out with sleeps, a slow machine's burst outlasts the debounce. The
	// debounce is long too, since a loaded runner can pause the burst
	// itself for tens of milliseconds.
	for range 20 {
		sv.Changed()
	}
	waitFor(t, func() bool { _, err := os.Stat(st.Path()); return err == nil })
	time.Sleep(450 * time.Millisecond)
	if n := collected.Load(); n != 1 {
		t.Fatalf("collected %d times for one burst", n)
	}
	// Nothing changed: no new file (and so no rotation).
	if wrote, err := sv.Save(ctx); wrote || err != nil {
		t.Fatalf("unchanged save wrote %v, %v", wrote, err)
	}
	env.Store("two")
	if wrote, err := sv.Save(ctx); !wrote || err != nil {
		t.Fatalf("changed save wrote %v, %v", wrote, err)
	}
	if rotated, _ := st.Rotated(); len(rotated) != 1 {
		t.Fatalf("rotated = %v", rotated)
	}
}

func TestSaverSavesDuringConstantChange(t *testing.T) {
	st := persist.NewStore(t.TempDir(), 5, nil)
	sv := persist.NewSaver(st, func(context.Context) (persist.Snapshot, error) { return snap("x"), nil }, 30*time.Millisecond, -1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sv.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	stop := time.After(2 * time.Second)
	for {
		sv.Changed()
		if _, err := os.Stat(st.Path()); err == nil {
			return
		}
		select {
		case <-stop:
			t.Fatal("no snapshot while changes kept coming")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestHistory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	h := persist.NewHistory(dir)
	if err := h.Write("p1", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := h.Write("p2", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := h.Write("../x", nil); err == nil {
		t.Fatal("a path in a pane ID must be refused")
	}
	if got, err := h.Read("p1"); err != nil || string(got) != "one" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "p1.txt")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("history mode %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(dir); runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("history dir mode %v", info.Mode().Perm())
	}
	removed, err := h.Prune(func(id string) bool { return id == "p1" }, false)
	if err != nil || len(removed) != 1 || !strings.HasSuffix(removed[0], "p2.txt") {
		t.Fatalf("prune = %v, %v", removed, err)
	}
	if _, err := h.Read("p2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned history still there: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
