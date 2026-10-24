//go:build !windows

package mux

import (
	"context"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/client"
)

func TestWorkflowRunsShowInTheOverview(t *testing.T) {
	h := newHarness(t, 150, 40, Options{})
	ctx := context.Background()
	wf := client.NewWorkflows(h.c)
	ok, err := wf.RunFile(ctx, "version = 1\n[[step]]\nid = \"build\"\nrun = [\"true\"]\n", "build.toml", client.RunOptions{Dir: h.dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wf.Wait(ctx, ok.ID, 0); err != nil {
		t.Fatal(err)
	}
	bad, err := wf.RunFile(ctx, "version = 1\n[[step]]\nid = \"test\"\nrun = [\"false\"]\n", "tests.toml", client.RunOptions{Dir: h.dir})
	if err != nil {
		t.Fatal(err)
	}
	h.waitText("workflow tests failed")

	h.press("ctrl+b", "O")
	h.waitFor("the runs in the Overview", func() bool {
		s := h.screen()
		return strings.Contains(s, "WORKFLOWS") && strings.Contains(s, ok.ID+" build") && strings.Contains(s, "1/1 succeeded") &&
			strings.Contains(s, bad.ID+" tests") && strings.Contains(s, "1/1 failed · 1 failed")
	})
}
