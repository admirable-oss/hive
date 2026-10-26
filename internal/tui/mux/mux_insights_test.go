//go:build !windows

package mux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/runtime"
)

// TestInsightsShowInTheOverview: a budget's alert raises a toast, and the
// Overview shows today's usage by kind and the budget over its limit.
func TestInsightsShowInTheOverview(t *testing.T) {
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "hive.sock")
	mod := runtime.NewModule(runtime.Config{
		SocketPath: sock, Shell: []string{"/bin/cat"}, InheritEnv: true,
		Insights: runtime.InsightsConfig{Budgets: []insights.Budget{
			{Name: "daily", Per: insights.PerDay, Limits: insights.Limits{CostUSD: 1}, Action: insights.ActionPause},
		}},
	})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = mod.Service.Stop(ctx)
	})
	c := client.NewService(client.Config{SocketPath: sock})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	work := t.TempDir()
	if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: "api", Root: work}); err != nil {
		t.Fatal(err)
	}
	created, err := client.NewWorkspace(c).TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "api", Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	h := startApp(t, c, 150, 50, work, Options{})

	cost, in, out := 1.25, int64(12000), int64(800)
	if _, err := client.NewInsights(c).ReportUsage(ctx, insights.UsageParams{ID: created.Pane.ID, CostUSD: &cost, Input: &in, Output: &out}); err != nil {
		t.Fatal(err)
	}
	h.waitText("budget daily is exceeded")

	h.press("ctrl+b", "O")
	h.waitFor("insights in the Overview", func() bool {
		s := h.screen()
		return strings.Contains(s, "INSIGHTS · today") && strings.Contains(s, "12.8k tokens · $1.25 (report)") &&
			strings.Contains(s, "daily 125% exceeded · holding new tasks")
	})
}
