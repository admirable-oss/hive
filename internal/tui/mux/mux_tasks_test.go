//go:build !windows

package mux

import (
	"context"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/task"
)

func TestPlansShowInTheOverviewAndApprovalsNotify(t *testing.T) {
	h := newHarness(t, 140, 40, Options{HideSidebar: true})
	ctx := context.Background()
	tasks := client.NewTasks(h.c)
	p, err := tasks.Run(ctx, task.PlanSpec{Name: "ship", Tasks: []task.Spec{
		{ID: "deploy", Prompt: "deploy it", Kind: "claude", EnvironmentID: "api", Approve: true},
		{ID: "announce", Prompt: "tell everyone", Kind: "claude", EnvironmentID: "api", Needs: []string{"deploy"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tasks.Cancel(context.Background(), p.ID, "") })
	h.waitText("waits for approval") // a toast

	h.press("ctrl+b", "O")
	h.waitFor("the plan in the Overview", func() bool {
		s := h.screen()
		return strings.Contains(s, "PLANS") && strings.Contains(s, p.ID+" ship") && strings.Contains(s, "waits: deploy")
	})
	if !strings.Contains(h.screen(), "1 plans running") {
		t.Fatalf("the title counts running plans:\n%s", h.screen())
	}

	// Its state follows events: refused, it fails, and nothing waits.
	if _, err := tasks.Approve(ctx, p.ID, "deploy", client.ApproveOptions{Deny: true}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the plan to show it failed", func() bool {
		s := h.screen()
		return strings.Contains(s, "✗ "+p.ID) && !strings.Contains(s, "waits:")
	})
}
