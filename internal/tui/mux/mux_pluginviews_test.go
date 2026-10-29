//go:build !windows

package mux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
)

const viewPlugin = `api = 2
id = "queue"
name = "Queue"
version = "1.0.0"

[permissions]
required = ["hive.write"]

[[actions]]
id = "approve"
command = ["sh", "-c", "echo approved"]

[[contributes.views]]
id = "review"
title = "Review queue"
`

// TestViewPane: a pane showing a plugin's view draws the document the
// plugin set, in cells, with no process; clicking its button runs the
// plugin's action, and tab then enter does too.
func TestViewPane(t *testing.T) {
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, plugin.ManifestFile), []byte(viewPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plugin.NewStore(filepath.Join(dir, "plugins")).Link(context.Background(), src, "dev"); err != nil {
		t.Fatal(err)
	}
	c := startDaemonIn(t, dir)
	ctx := context.Background()
	work := t.TempDir()
	if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: "api", Root: work}); err != nil {
		t.Fatal(err)
	}
	res, err := client.NewWorkspace(c).TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "api", Name: "main", Pane: pane.Spec{View: "queue/review"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pane.View != "queue/review" || res.Pane.ProcessID != "" {
		t.Fatalf("view pane: %+v", res.Pane)
	}
	if _, err := client.NewWorkspace(c).TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "api", Pane: pane.Spec{View: "queue/nope"}}); err == nil {
		t.Fatal("a pane opened a view no plugin declares")
	}
	h := startApp(t, c, 100, 24, work, Options{})
	h.waitText("nothing yet")

	doc := plugin.ViewDoc{Root: plugin.ViewNode{Kind: plugin.NodeColumn, Children: []plugin.ViewNode{
		{Kind: plugin.NodeText, Text: "2 pull requests wait", Style: "bold"},
		{Kind: plugin.NodeButton, ID: "ok", Text: "Approve", Action: "approve"},
	}}}
	if _, err := client.NewPlugins(c).SetView(ctx, plugin.ViewUpdate{Plugin: "queue", View: "review", Doc: &doc}); err != nil {
		t.Fatal(err)
	}
	h.waitText("[ Approve ]")
	scr := h.screen()
	if !strings.Contains(scr, "2 pull requests wait") || !strings.Contains(scr, "Review queue") {
		t.Fatalf("view pane:\n%s", scr)
	}

	approved := func() bool {
		for _, ts := range h.a.ui.toasts {
			if strings.Contains(ts.text, "approved") {
				return true
			}
		}
		return false
	}
	x, y := screenPos(t, scr, "[ Approve ]")
	h.click(x+2, y, 0)
	h.waitFor("the click's action", approved)

	h.a.ui.toasts = nil
	h.press("tab", "enter")
	h.waitFor("enter on the selected button", approved)
}

// screenPos finds text on the screen.
func screenPos(t *testing.T, scr, text string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(scr, "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return len([]rune(line[:i])), y
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, scr)
	return 0, 0
}
