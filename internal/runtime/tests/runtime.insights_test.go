//go:build !windows

package runtime_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/runtime"
	"github.com/admirable-oss/hive/internal/task"
)

// TestInsightsBudgetHoldsTasks: usage reported over the wire takes a
// pausing budget over its limit, which alerts and holds the next task in
// the queue with its reason; a resume over the wire releases it at once.
func TestInsightsBudgetHoldsTasks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sock := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: sock, Shell: []string{"sh"}, Insights: runtime.InsightsConfig{
		Budgets: []insights.Budget{{Name: "daily", Per: insights.PerDay, Limits: insights.Limits{CostUSD: 1}, Action: insights.ActionPause}},
	}})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.StopWithAgents(context.Background()) })
	c := client.NewService(client.Config{SocketPath: sock})
	t.Cleanup(func() { _ = c.Close() })
	alerts := mod.Events.Subscribe(64, "insights.")
	defer alerts.Close()

	if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "dev"}); err != nil {
		t.Fatal(err)
	}
	_, p, err := mod.Panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	cost := 1.5
	var used insights.UsageResult
	if err := c.Call(ctx, "insights.usage", insights.UsageParams{ID: p.ID, CostUSD: &cost, Total: true}, &used); err != nil {
		t.Fatal(err)
	}
	if used.Recorded == nil || used.Agent.Env != "dev" {
		t.Fatalf("the report is recorded for the pane's process: %+v", used)
	}
	for {
		ev, err := alerts.Next(ctx)
		if err != nil {
			t.Fatal("no exceeded alert:", err)
		}
		var a insights.Alert
		if ev.Type == insights.EventAlert && json.Unmarshal(ev.Data, &a) == nil && a.Level == insights.LevelExceeded {
			if !a.Held {
				t.Fatalf("a pausing budget holds: %+v", a)
			}
			break
		}
	}

	// No machine has the label, so a released task fails rather than start
	// an agent.
	plan, err := mod.Tasks.Submit(ctx, task.PlanSpec{Tasks: []task.Spec{
		{ID: "fix", Prompt: "fix it", Kind: "codex", EnvironmentID: "dev", Labels: []string{"nowhere"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fix := func() task.Task {
		got, err := mod.Tasks.Get(ctx, plan.ID)
		if err != nil {
			t.Fatal(err)
		}
		return *got.Task("fix")
	}
	waitFor(ctx, t, func() bool {
		tk := fix()
		return tk.State == task.StateQueued && strings.HasPrefix(tk.Reason, "held by budget daily")
	})
	var st []insights.BudgetStatus
	if err := c.Call(ctx, "insights.resume", insights.ResumeParams{Budget: "daily"}, &st); err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0].Held || !st[0].Resumed {
		t.Fatalf("resumed: %+v", st)
	}
	waitFor(ctx, t, func() bool { return fix().State != task.StateQueued })
	if tk := fix(); strings.HasPrefix(tk.Reason, "held by") {
		t.Fatalf("released, the task is no longer held: %+v", tk)
	}

	if days, _ := filepath.Glob(filepath.Join(filepath.Dir(sock), "insights", "*.jsonl")); len(days) != 1 {
		t.Fatalf("the usage and alerts are kept in the Hive root: %v", days)
	}
}
