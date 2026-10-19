package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func TestEncodeLine(t *testing.T) {
	bold := hiveapi.CellStyle{Attrs: 1, Fg: hiveapi.Color(1<<24 | 2)} // bold, palette 2
	cells := []hiveapi.Cell{
		{Content: "h", Width: 1, Style: bold},
		{Content: "i", Width: 1, Style: bold},
		{Content: " ", Width: 1},
		{Content: "世", Width: 2},
		{Width: 0},
		{Content: "!", Width: 1, Link: "https://example.com"},
		{Width: 1},
		{Width: 1}, // trailing blanks
	}
	ln := encodeLine(3, cells)
	if ln.Y != 3 || len(ln.Runs) != 3 {
		t.Fatalf("runs %+v", ln.Runs)
	}
	if r := ln.Runs[0]; r.Text != "hi" || r.Width != 2 || r.Fg != "2" || r.Attrs != 1 {
		t.Fatalf("styled run %+v", r)
	}
	if r := ln.Runs[1]; r.Text != " 世" || r.Width != 3 {
		t.Fatalf("a wide character must take two columns: %+v", r)
	}
	if r := ln.Runs[2]; r.Text != "!" || r.Link != "https://example.com" {
		t.Fatalf("link run %+v", r)
	}
	if got := colorString(hiveapi.Color(2<<24 | 0x12ab34)); got != "#12ab34" {
		t.Fatalf("rgb colour %q", got)
	}
	if got := encodeLine(0, []hiveapi.Cell{{Width: 1}, {Width: 1}}); len(got.Runs) != 0 {
		t.Fatalf("a blank line has runs: %+v", got.Runs)
	}
}

func TestScreenUpdate(t *testing.T) {
	s := &hiveapi.TerminalScreen{}
	s.Apply(&hiveapi.Frame{
		Keyframe: true, Cols: 4, Rows: 2, Cursor: hiveapi.Cursor{X: 1, Y: 1, Shape: 2}, Modes: hiveapi.ModeBracketedPaste,
		Lines: []hiveapi.LineUpdate{{Y: 0, Cells: []hiveapi.Cell{{Content: "a", Width: 1}}}, {Y: 1, Cells: []hiveapi.Cell{{Content: "b", Width: 1}}}},
	})
	u := update("p1", 7, s, true, nil)
	if !u.Full || len(u.Lines) != 2 || u.Cursor.Shape != "bar" || !u.Cursor.Visible || u.Modes&ModeBracketedPaste == 0 || u.Seq != 7 {
		t.Fatalf("full update %+v", u)
	}
	u = update("p1", 8, s, false, []int{1, 9})
	if u.Full || len(u.Lines) != 1 || u.Lines[0].Y != 1 {
		t.Fatalf("partial update %+v", u)
	}
	data, _ := json.Marshal(u)
	if !strings.Contains(string(data), `"t":"b"`) {
		t.Fatalf("wire form %s", data)
	}
}

func TestPasteBytes(t *testing.T) {
	if got := pasteBytes("a\nb\r\nc", false); got != "a\rb\rc" {
		t.Fatalf("plain paste %q", got)
	}
	got := pasteBytes("x\x1b[201~rm -rf ~\x1b[20\x1b[201~1~", true)
	if !strings.HasPrefix(got, "\x1b[200~") || !strings.HasSuffix(got, "\x1b[201~") || strings.Count(got, "\x1b[201~") != 1 {
		t.Fatalf("a paste broke out of its brackets: %q", got)
	}
}

func TestParseSettings(t *testing.T) {
	s, err := parseSettings([]byte(`{"notify":{"on":["blocked"],"system":true},"theme":{"name":"nord"},"keys":{"prefix_keys":["ctrl+a"]}}`))
	if err != nil || !s.SystemNotifications || s.Theme != "nord" || len(s.NotifyOn) != 1 || s.Prefix[0] != "ctrl+a" {
		t.Fatalf("settings %+v %v", s, err)
	}
	if s, err := parseSettings([]byte("not json")); err == nil || s.Theme != "auto" {
		t.Fatalf("bad config: %+v %v", s, err)
	}
}

func agentsWS(agents ...Agent) Workspace {
	return Workspace{Agents: agents, Tabs: []Tab{{ID: "t1", Active: true, Focused: "p-focused"}}}
}

func TestNotifications(t *testing.T) {
	focused := false
	h := New(Config{Focused: func() bool { return focused }})
	a := Agent{ID: "a1", Name: "claude", Kind: "claude", EnvironmentID: "api", PaneID: "p1", State: "working", StateSeq: 1}
	if n := h.notificationsLocked(agentsWS(a)); len(n) != 0 {
		t.Fatalf("the first snapshot notified: %v", n)
	}
	a.State, a.StateSeq, a.Reason = "blocked", 2, "asks to run a command"
	n := h.notificationsLocked(agentsWS(a))
	if len(n) != 1 || n[0].Title != "claude needs you" || n[0].Body != "asks to run a command" || n[0].PaneID != "p1" {
		t.Fatalf("blocked: %+v", n)
	}
	if n := h.notificationsLocked(agentsWS(a)); len(n) != 0 {
		t.Fatalf("notified twice: %v", n)
	}
	a.State, a.StateSeq = "working", 3
	if n := h.notificationsLocked(agentsWS(a)); len(n) != 0 {
		t.Fatalf("working notified: %v", n)
	}
	// Someone is looking at it: no notification.
	focused, a.PaneID, a.State, a.StateSeq = true, "p-focused", "done", 4
	if n := h.notificationsLocked(agentsWS(a)); len(n) != 0 {
		t.Fatalf("notified the pane in front of the user: %v", n)
	}
	// The daemon notifies the system itself: the app does not.
	focused, a.State, a.StateSeq = false, "blocked", 5
	h.settings.SystemNotifications = true
	if n := h.notificationsLocked(agentsWS(a)); len(n) != 0 {
		t.Fatalf("notified alongside the daemon: %v", n)
	}
	// A new agent that starts blocked is announced.
	h.settings.SystemNotifications = false
	b := Agent{ID: "b1", Name: "codex", State: "blocked", StateSeq: 1}
	if n := h.notificationsLocked(agentsWS(a, b)); len(n) != 1 || n[0].AgentID != "b1" {
		t.Fatalf("new blocked agent: %+v", n)
	}
}

func TestValidation(t *testing.T) {
	h := New(Config{})
	for _, err := range []error{
		h.OpenView("w", "", 80, 24), h.OpenView("w", "p", 1, 24), h.OpenView("w", "p", 80, 5000), h.OpenView("", "p", 80, 24),
		h.Input("p\n", "x"), h.FocusPane(strings.Repeat("x", 300)),
		h.ResizePane("p", "sideways", 1), h.ResizeTab("t", 0, 0),
	} {
		if err == nil {
			t.Error("invalid input accepted")
		}
	}
	if _, err := h.Prompt("a1", "   "); err == nil {
		t.Error("an empty prompt is accepted")
	}
	if _, err := h.SplitPane("p", "diagonal"); err == nil {
		t.Error("a bad direction is accepted")
	}
	if err := h.FocusPane("p"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("acting while disconnected: %v", err)
	}
}

func TestWorkspaceLeavesSecretsOut(t *testing.T) {
	snap := hiveapi.Snapshot{
		Environments: []hiveapi.Environment{{ID: "api", Env: map[string]string{"TOKEN": "s3cret"}}},
		Processes:    []hiveapi.Process{{ID: "pr1", Command: "deploy", Args: []string{"--token", "s3cret"}, Status: "running"}},
		Panes:        []hiveapi.Pane{{ID: "p1", ProcessID: "pr1", TabID: "t1"}},
	}
	ws := workspaceOf(snap)
	data, _ := json.Marshal(ws)
	if strings.Contains(string(data), "s3cret") || strings.Contains(string(data), "deploy") {
		t.Fatalf("the view model carries secrets: %s", data)
	}
	if !ws.Panes[0].Running || ws.Environments[0].Name != "api" {
		t.Fatalf("view model %+v", ws)
	}
}
