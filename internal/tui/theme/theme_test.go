package theme_test

import (
	"image/color"
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/admirable-oss/hive/internal/tui/theme"
)

func TestBuiltinsAreComplete(t *testing.T) {
	want := []string{"auto", "catppuccin", "catppuccin-latte", "gruvbox", "nord", "terminal", "tokyo-night"}
	if got := theme.Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for _, name := range want[1:] {
		th, ok := theme.Lookup(name)
		if !ok || th.Name != name {
			t.Fatalf("Lookup(%q) = %+v, %v", name, th, ok)
		}
		// Every colour a chrome element needs is set (terminal leaves Fg and
		// Bg to the terminal on purpose).
		for i, c := range []color.Color{th.Muted, th.Border, th.Accent, th.Selection, th.Success, th.Warning, th.Error, th.Info} {
			if c == nil {
				t.Errorf("%s: colour %d is nil", name, i)
			}
		}
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		dark bool
		want string
		err  bool
	}{
		{"auto", true, "catppuccin", false},
		{"auto", false, "catppuccin-latte", false},
		{"", false, "catppuccin-latte", false},
		{"nord", false, "nord", false},
		{"solarized", true, "catppuccin", true},
		{"custom", true, "catppuccin", true}, // no [theme.custom]
	}
	for _, tt := range tests {
		got, err := theme.Resolve(tt.name, nil, tt.dark)
		if got.Name != tt.want || (err != nil) != tt.err {
			t.Errorf("Resolve(%q, dark=%v) = %s, %v", tt.name, tt.dark, got.Name, err)
		}
	}
}

func TestCustom(t *testing.T) {
	th, err := theme.NewCustom(map[string]string{"base": "nord", "accent": "#f0a", "border": "8", "fg": "default", "info": "200"})
	if err != nil {
		t.Fatal(err)
	}
	nord, _ := theme.Lookup("nord")
	if th.Name != "custom" || th.Dark != nord.Dark || th.Success != nord.Success {
		t.Fatalf("custom theme should start from its base: %+v", th)
	}
	if th.Accent != (color.RGBA{0xff, 0x00, 0xaa, 0xff}) || th.Border != ansi.BrightBlack || th.Fg != nil || th.Info != ansi.IndexedColor(200) {
		t.Fatalf("overrides not applied: accent %v border %v fg %v info %v", th.Accent, th.Border, th.Fg, th.Info)
	}
	got, err := theme.Resolve("custom", &th, false)
	if err != nil || got.Accent != th.Accent {
		t.Fatalf("Resolve(custom) = %+v, %v", got, err)
	}
	for _, bad := range []map[string]string{
		{"base": "nope"},
		{"sparkle": "#fff"},
		{"accent": "#12345"},
		{"accent": "256"},
		{"accent": "red"},
	} {
		if _, err := theme.NewCustom(bad); err == nil {
			t.Errorf("NewCustom(%v) should fail", bad)
		}
	}
}
