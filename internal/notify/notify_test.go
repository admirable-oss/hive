package notify

import (
	"errors"
	"slices"
	"testing"
)

func TestWants(t *testing.T) {
	c := Defaults()
	c.Agents = map[string][]string{"codex": {"blocked"}, "aider": {}}
	for _, tc := range []struct {
		kind, state string
		want        bool
	}{
		{"claude", "blocked", true},
		{"claude", "done", true},
		{"claude", "working", false},
		{"codex", "done", false},
		{"codex", "blocked", true},
		{"aider", "blocked", false},
		{"", "done", false},
	} {
		if got := c.Wants(tc.kind, tc.state); got != tc.want {
			t.Errorf("Wants(%q, %q) = %v", tc.kind, tc.state, got)
		}
	}
}

func TestSequence(t *testing.T) {
	n := Note{Title: "Claude Code; api", Body: "needs you\x1b]evil\x07"}
	if got, want := Sequence(TerminalOSC9, n, false), "\x1b]9;Claude Code; api: needs you]evil\x07"; got != want {
		t.Errorf("osc9 = %q, want %q", got, want)
	}
	if got, want := Sequence(TerminalOSC777, n, true), "\x1b]777;notify;Claude Code, api;needs you]evil\x07\a"; got != want {
		t.Errorf("osc777 = %q, want %q", got, want)
	}
	if got := Sequence(TerminalOff, n, false); got != "" {
		t.Errorf("off = %q", got)
	}
}

func TestTerminalMode(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	if got := TerminalMode(TerminalAuto, env("iTerm.app")); got != TerminalOSC9 {
		t.Errorf("iTerm auto = %s", got)
	}
	if got := TerminalMode(TerminalAuto, env("Apple_Terminal")); got != TerminalOff {
		t.Errorf("Terminal.app auto = %s", got)
	}
	if got := TerminalMode(TerminalOSC777, env("Apple_Terminal")); got != TerminalOSC777 {
		t.Errorf("explicit = %s", got)
	}
}

func TestSystemCommand(t *testing.T) {
	n := Note{Title: `say "hi"`, Body: `a\b`}
	got := SystemCommand("darwin", n, true, nil)
	want := []string{"osascript", "-e", `display notification "a\\b" with title "say \"hi\"" sound name "Glass"`}
	if !slices.Equal(got, want) {
		t.Errorf("darwin = %q", got)
	}
	found := func(string) (string, error) { return "/usr/bin/notify-send", nil }
	if got := SystemCommand("linux", n, false, found); !slices.Equal(got, []string{"notify-send", "--app-name=Hive", n.Title, n.Body}) {
		t.Errorf("linux = %q", got)
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if got := SystemCommand("linux", n, false, missing); got != nil {
		t.Errorf("linux without notify-send = %q", got)
	}
}
