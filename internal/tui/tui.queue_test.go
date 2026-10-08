package tui_test

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui"
)

func interactiveModel(t *testing.T, fc *fakeClient) tui.Model {
	t.Helper()
	fc.procs = []process.Process{{ID: "p1", EnvironmentID: "env", Status: process.StatusRunning}}
	var m tea.Model = tui.NewModel(fc)
	m, _ = m.Update(tui.RefreshMsg{Connected: true, Processes: fc.procs})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.(tui.Model).Interactive {
		t.Fatal("expected interactive mode")
	}
	return m.(tui.Model)
}

// TestTUI_TypedKeysArriveInOrder types 1000 keys while every delivery takes
// a random amount of time. Sending each key from its own goroutine (the old
// behaviour) reorders them almost immediately under this load.
func TestTUI_TypedKeysArriveInOrder(t *testing.T) {
	fc := &fakeClient{inputDelay: func() { time.Sleep(time.Duration(rand.IntN(200)) * time.Microsecond) }}
	m := interactiveModel(t, fc)

	var want strings.Builder
	var model tea.Model = m
	for i := range 1000 {
		r := rune('a' + i%26)
		want.WriteRune(r)
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model.(tui.Model).Close()

	if got := fc.input("p1"); got != want.String() {
		t.Fatalf("keystrokes arrived out of order or were lost:\n got %q\nwant %q", got, want.String())
	}
	if fc.inputCalls >= 1000 {
		t.Logf("note: no coalescing happened (%d calls)", fc.inputCalls)
	}
}

func TestTUI_InputFailureIsShownInFooter(t *testing.T) {
	fc := &fakeClient{inputErr: errors.New("daemon went away")}
	var model tea.Model = interactiveModel(t, fc)
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model.(tui.Model).Close() // wait for the failed delivery

	model, _ = model.Update(tui.PollTick())
	if view := model.View(); !strings.Contains(view, "keystrokes not delivered") {
		t.Fatalf("footer should report the failed delivery, got:\n%s", view)
	}
}
