package keymap_test

import (
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/tui/keymap"
)

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"ctrl+b": "ctrl+b", "Ctrl+B": "ctrl+shift+b", "C-b": "ctrl+b", "c-b": "c-b",
		"alt+ctrl+x": "ctrl+alt+x", "M-x": "alt+x", "option+x": "alt+x",
		"shift+a": "A", "A": "A", "?": "?", "%": "%", " ": "space", "Space": "space",
		"Escape": "esc", "return": "enter", "PageUp": "pgup", "shift+tab": "shift+tab",
		"ctrl++": "ctrl++", "+": "+", "F12": "f12", "alt+left": "alt+left",
	}
	for in, want := range tests {
		got, err := keymap.Normalize(in)
		if in == "c-b" {
			// lower-case c- is not the tmux spelling: it is an unknown key name.
			if err == nil {
				t.Errorf("Normalize(%q) = %q, want an error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "hyperspace+x", "ctrl+", "ctrl+banana"} {
		if got, err := keymap.Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) = %q, want an error", bad, got)
		}
	}
}

// keys as ultraviolet reports them.
var (
	ctrlB    = uv.Key{Code: 'b', Mod: uv.ModCtrl}
	ctrlA    = uv.Key{Code: 'a', Mod: uv.ModCtrl}
	percent  = uv.Key{Code: '%', Text: "%"}
	question = uv.Key{Code: '/', Mod: uv.ModShift, Text: "?"}
	upperO   = uv.Key{Code: 'o', Mod: uv.ModShift, Text: "O"}
	esc      = uv.Key{Code: uv.KeyEscape}
	altLeft  = uv.Key{Code: uv.KeyLeft, Mod: uv.ModAlt}
	space    = uv.Key{Code: uv.KeySpace, Text: " "}
	digit3   = uv.Key{Code: '3', Text: "3"}
)

func TestDefaultBindings(t *testing.T) {
	km := keymap.Default()
	if p, ok := km.IsPrefix(ctrlB); !ok || p != "ctrl+b" {
		t.Fatalf("ctrl+b should be the prefix: %q %v", p, ok)
	}
	if _, ok := km.IsPrefix(ctrlA); ok {
		t.Fatal("ctrl+a is not a default prefix")
	}
	tests := []struct {
		mode keymap.Mode
		key  uv.Key
		want keymap.Action
	}{
		{keymap.ModePrefix, percent, keymap.SplitRight},
		{keymap.ModePrefix, question, keymap.Help},
		{keymap.ModePrefix, upperO, keymap.Overview},
		{keymap.ModePrefix, ctrlB, keymap.SendPrefix},
		{keymap.ModePrefix, altLeft, keymap.ResizeLeft},
		{keymap.ModePrefix, space, keymap.EnterNavigate},
		{keymap.ModePrefix, digit3, keymap.TabAction(3)},
		{keymap.ModeNavigate, esc, keymap.ExitMode},
		{keymap.ModeCopy, question, keymap.CopySearchBack},
		{keymap.ModeCopy, ctrlB, keymap.CopyPageUp},
	}
	for _, tt := range tests {
		got, ok := km.Lookup(tt.mode, tt.key)
		if !ok || got != tt.want {
			t.Errorf("%s %v = %q, %v; want %q", tt.mode, tt.key, got, ok, tt.want)
		}
	}
	if _, ok := km.Lookup(keymap.ModeTerminal, percent); ok {
		t.Fatal("terminal mode has no default bindings: keys go to the pane")
	}
	if n, ok := keymap.TabAction(3).TabNumber(); !ok || n != 3 {
		t.Fatal("TabNumber")
	}
}

func TestOverrides(t *testing.T) {
	km, warnings := keymap.New(keymap.Overrides{
		Prefix: []string{"ctrl+a", "ctrl+b"},
		Modes: map[keymap.Mode]map[keymap.Action][]string{
			keymap.ModePrefix:   {keymap.SplitRight: {"v"}, keymap.Zoom: {}},
			keymap.ModeTerminal: {keymap.FocusLeft: {"alt+h"}, keymap.FocusRight: {"ctrl+a"}},
		},
	})
	// ctrl+a cannot be a terminal binding: it is a prefix.
	if len(warnings) != 1 || !strings.Contains(warnings[0], "prefix key") {
		t.Fatalf("warnings = %q", warnings)
	}
	if !slices.Equal(km.Prefixes(), []string{"ctrl+a", "ctrl+b"}) {
		t.Fatalf("prefixes = %v", km.Prefixes())
	}
	if a, _ := km.Lookup(keymap.ModePrefix, uv.Key{Code: 'v', Text: "v"}); a != keymap.SplitRight {
		t.Fatalf("v = %q, want split-right (an override replaces the built-in keys)", a)
	}
	if a, ok := km.Lookup(keymap.ModePrefix, percent); ok {
		t.Fatalf("%% should be unbound once split_right is overridden, got %q", a)
	}
	if _, ok := km.Lookup(keymap.ModePrefix, uv.Key{Code: 'z', Text: "z"}); ok {
		t.Fatal("zoom = [] unbinds it")
	}
	if a, _ := km.Lookup(keymap.ModeTerminal, uv.Key{Code: 'h', Mod: uv.ModAlt}); a != keymap.FocusLeft {
		t.Fatalf("alt+h in terminal mode = %q", a)
	}
	// Each prefix, pressed twice, sends itself.
	if a, _ := km.Lookup(keymap.ModePrefix, ctrlA); a != keymap.SendPrefix {
		t.Fatalf("ctrl+a ctrl+a = %q", a)
	}
}

func TestOverrideProblemsAreWarnings(t *testing.T) {
	_, warnings := keymap.New(keymap.Overrides{
		Prefix: []string{"ctrl+nope"},
		Modes: map[keymap.Mode]map[keymap.Action][]string{
			"insert":            {keymap.Zoom: {"z"}},
			keymap.ModePrefix:   {keymap.CopyYank: {"y"}, "fly": {"f"}, keymap.Help: {"ctrl+banana"}, keymap.NewTab: {"t"}, keymap.CloseTab: {"t"}},
			keymap.ModeCopy:     {keymap.Zoom: {"z"}},
			keymap.ModeResize:   {},
			keymap.ModeNavigate: {},
		},
	})
	want := []string{"keys.insert", "action not available", "unknown action", "unknown key name", "bound to both", "no usable prefix", "keys.prefix"}
	joined := strings.Join(warnings, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("warnings miss %q:\n%s", w, joined)
		}
	}
}

func TestEntriesAndFilter(t *testing.T) {
	km := keymap.Default()
	entries := km.Entries()
	if len(entries) < 60 {
		t.Fatalf("only %d help entries", len(entries))
	}
	for _, e := range entries {
		if e.Description == "" || len(e.Keys) == 0 {
			t.Fatalf("incomplete entry %+v", e)
		}
	}
	got := keymap.Filter(entries, "prefix split")
	if len(got) != 2 {
		t.Fatalf("filter 'prefix split' = %+v", got)
	}
	if len(keymap.Filter(entries, "")) != len(entries) {
		t.Fatal("an empty filter keeps everything")
	}
	if a, err := keymap.ParseAction("split_right"); err != nil || a != keymap.SplitRight {
		t.Fatalf("ParseAction = %q, %v", a, err)
	}
}
