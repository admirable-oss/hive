//go:build !windows

package mux

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/tui/keymap"
)

// TestCommandsStatusAndMetadata: a key command opens a pane running the
// user's command, the status segments fill the tab bar's right end, and
// tokens and a title reported on an environment and a pane show in the
// sidebar until they are cleared.
func TestCommandsStatusAndMetadata(t *testing.T) {
	km, warnings := keymap.New(keymap.Overrides{Commands: []keymap.Command{{Mode: keymap.ModePrefix, Keys: []string{"g"}, Title: "notes"}}})
	if len(warnings) != 0 {
		t.Fatal(warnings)
	}
	h := newHarness(t, 120, 24, Options{
		Keymap:   km,
		Commands: []Command{{Type: "pane", Command: "echo FROM-THE-COMMAND; exec cat", Title: "notes"}},
		StatusRight: []StatusSegment{
			{Type: "text", Text: "★hive"},
			{Type: "command", Command: "echo status-ok; echo second line", Interval: time.Hour},
		},
	})
	h.waitText("★hive")
	h.waitText("status-ok")
	if strings.Contains(h.screen(), "second line") {
		t.Fatal("only a command's first line shows")
	}

	before := len(h.a.ws.snap.panes)
	h.a.do(keymap.CommandAction(0))
	h.waitFor("the command's pane", func() bool { return len(h.a.ws.snap.panes) == before+1 })
	h.waitText("FROM-THE-COMMAND")

	ctx := context.Background()
	plugins := client.NewPlugins(h.c)
	var pane string // the first pane, which has no name of its own
	for _, p := range h.a.ws.snap.panes {
		if p.Name == "" {
			pane = p.ID
		}
	}
	title := "reviewer"
	if _, err := plugins.ReportMetadata(ctx, metadata.Report{Target: metadata.Target{Environment: "api"}, Tokens: map[string]string{"pr": "PR#12✓"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := plugins.ReportMetadata(ctx, metadata.Report{Target: metadata.Target{Pane: pane}, Title: &title, Tokens: map[string]string{"tests": "40/40"}}); err != nil {
		t.Fatal(err)
	}
	h.waitText("PR#12✓")
	h.waitText("40/40")
	h.waitText("reviewer")
	if _, err := plugins.ReportMetadata(ctx, metadata.Report{Target: metadata.Target{Environment: "api"}, Clear: true, Tokens: map[string]string{"pr": ""}}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the token to go", func() bool { return !strings.Contains(h.screen(), "PR#12✓") })
}

func TestStrftime(t *testing.T) {
	at := time.Date(2026, 10, 11, 9, 5, 7, 0, time.UTC)
	if got := strftime(at, "%a %d %b %Y %H:%M:%S %% %q"); got != "Sun 11 Oct 2026 09:05:07 % %q" {
		t.Fatalf("strftime = %q", got)
	}
}
