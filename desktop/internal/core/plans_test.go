package core

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// TestHubShowsPlansAndApprovals: a plan shows in the workspace with the
// task that waits for approval, which notifies once; refusing it from the
// app ends the plan.
func TestHubShowsPlansAndApprovals(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a daemon")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the daemon's sockets")
	}
	home := testHome(t)
	var (
		mu    sync.Mutex
		notes []Notification
	)
	h := New(Config{Home: home, Hive: hiveBin, Emitter: &recorder{}, Notify: func(n Notification) {
		mu.Lock()
		defer mu.Unlock()
		notes = append(notes, n)
	}})
	h.Start()
	defer h.Close()
	waitFor(t, "a connection", 30*time.Second, func() bool { return h.Connection().State == StateConnected })
	if err := h.CreateEnvironment("work", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first workspace", 10*time.Second, func() bool { return len(h.Workspace().Environments) == 1 })

	c, err := hiveapi.Connect(hiveapi.Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err := c.RunPlan(context.Background(), hiveapi.PlanSpec{Name: "ship", Tasks: []hiveapi.TaskSpec{
		{ID: "deploy", Prompt: "deploy it", Kind: "claude", EnvironmentID: "work", Approve: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the plan, waiting for approval", 10*time.Second, func() bool {
		ws := h.Workspace()
		i := slices.IndexFunc(ws.Plans, func(x Plan) bool { return x.ID == p.ID })
		return i >= 0 && ws.Plans[i].Name == "ship" && len(ws.Plans[i].Waiting) == 1 && ws.Plans[i].Waiting[0].State == "approval"
	})
	waitFor(t, "the approval's notification", 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notes) == 1 && notes[0].Body == "deploy waits for your approval"
	})

	if err := h.ApproveTask(p.ID, "deploy", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the plan to fail", 10*time.Second, func() bool {
		ws := h.Workspace()
		i := slices.IndexFunc(ws.Plans, func(x Plan) bool { return x.ID == p.ID })
		return i >= 0 && ws.Plans[i].State == "failed" && len(ws.Plans[i].Waiting) == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 1 {
		t.Fatalf("notified %d times: %+v", len(notes), notes)
	}
}
