package task_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/task"
)

func TestFilesystemStoreKeepsPlansAcrossVersions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	s := task.NewFilesystemStore(dir)
	if plans, err := s.List(t.Context()); err != nil || len(plans) != 0 {
		t.Fatalf("an empty store: %v %v", plans, err)
	}
	p := &task.Plan{ID: "aaaa1111", State: task.PlanRunning, CreatedAt: time.Now().UTC(), Tasks: []task.Task{{Spec: spec("a"), State: task.StateRunning, AgentID: "p1"}}}
	if err := s.Put(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	// A newer hive's plan is left alone; a broken one is moved aside.
	if err := os.WriteFile(filepath.Join(dir, "bbbb2222.json"), []byte(`{"schema": 99, "id": "bbbb2222"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cccc3333.json"), []byte(`{"schema": 1, "id": `), 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := s.List(t.Context())
	if len(plans) != 1 || plans[0].ID != "aaaa1111" || plans[0].Tasks[0].AgentID != "p1" || plans[0].Schema != 1 {
		t.Fatalf("plans: %+v", plans)
	}
	if !errors.Is(err, task.ErrNewer) || !strings.Contains(err.Error(), "cccc3333.json") {
		t.Fatalf("problems: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bbbb2222.json")); err != nil {
		t.Fatalf("a newer plan is left in place: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "cccc3333.json.corrupt-*")); len(m) != 1 {
		t.Fatalf("the broken plan is moved aside: %v", m)
	}
	if err := s.Delete(t.Context(), "aaaa1111"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(t.Context(), "aaaa1111"); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
}
