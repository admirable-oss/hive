package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui"
)

func TestKeyToBytes(t *testing.T) {
	tests := []struct {
		key  tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("héllo")}, "héllo"},
		{tea.KeyMsg{Type: tea.KeySpace}, " "},
		{tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{tea.KeyMsg{Type: tea.KeyTab}, "\t"},
		{tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{tea.KeyMsg{Type: tea.KeyCtrlBackslash}, "\x1c"},
		{tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true}, "\x1bb"},
	}
	for _, tt := range tests {
		if got := string(tui.KeyToBytes(tt.key)); got != tt.want {
			t.Errorf("%v: got %q, want %q", tt.key, got, tt.want)
		}
	}
}

// A new agent appearing above the selected one must not move the selection.
func TestTUI_SelectionFollowsProcessAcrossRefresh(t *testing.T) {
	m := tui.NewModel(&fakeClient{})
	first := []process.Process{
		{ID: "b", Command: "codex", Status: process.StatusRunning},
		{ID: "c", Command: "claude", Status: process.StatusRunning},
	}
	updated, _ := m.Update(tui.RefreshMsg{Connected: true, Processes: first})
	updated, _ = updated.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyDown})  // select "c"
	updated, _ = updated.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyEnter}) // take control of "c"

	withNew := append([]process.Process{{ID: "a", Command: "new", Status: process.StatusRunning}}, first...)
	updated, _ = updated.(tui.Model).Update(tui.RefreshMsg{Connected: true, Processes: withNew})
	m = updated.(tui.Model)

	if cur := m.CurrentProcess(); cur == nil || cur.ID != "c" {
		t.Fatalf("selection moved to %+v, want process c", cur)
	}
	if !m.Interactive {
		t.Fatal("interactive mode should survive an unrelated refresh")
	}

	// Once the selected agent stops running, interactive mode ends.
	withNew[2].Status = process.StatusExited
	updated, _ = m.Update(tui.RefreshMsg{Connected: true, Processes: withNew})
	if updated.(tui.Model).Interactive {
		t.Fatal("interactive mode should end when the agent exits")
	}
}
