// Package theme holds the multiplexer's colour schemes. A Theme names the
// colours of the chrome (borders, sidebar, tab bar, overlays); pane content
// keeps the colours its program chose.
//
// Built-in themes are hive (amber on graphite, the desktop app's colours),
// hive-light, catppuccin (mocha), catppuccin-latte, tokyo-night, gruvbox,
// nord and terminal, which uses the terminal's own palette so Hive matches
// whatever scheme the terminal has. "auto" picks hive or hive-light from the
// terminal's background colour.
package theme

import (
	"fmt"
	"image/color"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Theme is a set of named colours. A nil colour is the terminal's default
// foreground or background.
type Theme struct {
	Name string
	Dark bool

	Fg     color.Color // chrome text
	Bg     color.Color // chrome background (sidebar, tab bar)
	Muted  color.Color // secondary text, inactive tabs
	Border color.Color // inactive pane borders
	Accent color.Color // active pane border, active tab, selection marks
	// Selection is the background of selected text (copy mode, pickers).
	Selection color.Color
	// Status colours: agents running, waiting, failed; toasts.
	Success color.Color
	Warning color.Color
	Error   color.Color
	Info    color.Color
}

// Names of the special themes.
const (
	Auto     = "auto"
	Terminal = "terminal"
	Custom   = "custom"
)

// DefaultDark and DefaultLight are what "auto" picks.
const (
	DefaultDark  = "hive"
	DefaultLight = "hive-light"
)

func hex(s string) color.Color {
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil {
		panic("theme: bad colour " + s)
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

var builtin = map[string]Theme{
	// hive and hive-light are the desktop app's tokens (desktop/frontend/
	// src/index.css) in sRGB: graphite chrome, the bee's amber for what is
	// focused, and the same colours for working, blocked, done and failed.
	"hive": {
		Name: "hive", Dark: true,
		Fg: hex("#eeeef1"), Bg: hex("#111114"), Muted: hex("#9d9da6"), Border: hex("#37373a"),
		Accent: hex("#f5c25f"), Selection: hex("#433825"),
		Success: hex("#5dc879"), Warning: hex("#f98f3a"), Error: hex("#f75d59"), Info: hex("#60aaf3"),
	},
	"hive-light": {
		Name: "hive-light", Dark: false,
		Fg: hex("#18181b"), Bg: hex("#f6f6f8"), Muted: hex("#5d5d65"), Border: hex("#d4d4d8"),
		Accent: hex("#a8650b"), Selection: hex("#efdfc4"),
		Success: hex("#05893e"), Warning: hex("#cf5604"), Error: hex("#d40c1a"), Info: hex("#2171cc"),
	},
	"catppuccin": {
		Name: "catppuccin", Dark: true,
		Fg: hex("#cdd6f4"), Bg: hex("#181825"), Muted: hex("#7f849c"), Border: hex("#45475a"),
		Accent: hex("#f9e2af"), Selection: hex("#45475a"),
		Success: hex("#a6e3a1"), Warning: hex("#fab387"), Error: hex("#f38ba8"), Info: hex("#89b4fa"),
	},
	"catppuccin-latte": {
		Name: "catppuccin-latte", Dark: false,
		Fg: hex("#4c4f69"), Bg: hex("#e6e9ef"), Muted: hex("#8c8fa1"), Border: hex("#bcc0cc"),
		Accent: hex("#df8e1d"), Selection: hex("#ccd0da"),
		Success: hex("#40a02b"), Warning: hex("#fe640b"), Error: hex("#d20f39"), Info: hex("#1e66f5"),
	},
	"tokyo-night": {
		Name: "tokyo-night", Dark: true,
		Fg: hex("#c0caf5"), Bg: hex("#16161e"), Muted: hex("#565f89"), Border: hex("#3b4261"),
		Accent: hex("#7aa2f7"), Selection: hex("#283457"),
		Success: hex("#9ece6a"), Warning: hex("#e0af68"), Error: hex("#f7768e"), Info: hex("#7dcfff"),
	},
	"gruvbox": {
		Name: "gruvbox", Dark: true,
		Fg: hex("#ebdbb2"), Bg: hex("#1d2021"), Muted: hex("#928374"), Border: hex("#504945"),
		Accent: hex("#fabd2f"), Selection: hex("#504945"),
		Success: hex("#b8bb26"), Warning: hex("#fe8019"), Error: hex("#fb4934"), Info: hex("#83a598"),
	},
	"nord": {
		Name: "nord", Dark: true,
		Fg: hex("#eceff4"), Bg: hex("#2e3440"), Muted: hex("#7b88a1"), Border: hex("#4c566a"),
		Accent: hex("#88c0d0"), Selection: hex("#434c5e"),
		Success: hex("#a3be8c"), Warning: hex("#ebcb8b"), Error: hex("#bf616a"), Info: hex("#81a1c1"),
	},
	// terminal uses the 16 ANSI colours, so it follows the terminal's scheme.
	Terminal: {
		Name: Terminal, Dark: true,
		Fg: nil, Bg: nil, Muted: ansi.BrightBlack, Border: ansi.BrightBlack,
		Accent: ansi.Yellow, Selection: ansi.BrightBlack,
		Success: ansi.Green, Warning: ansi.Yellow, Error: ansi.Red, Info: ansi.Blue,
	},
}

// Names lists the built-in themes and auto, sorted.
func Names() []string {
	names := append(slices.Collect(maps.Keys(builtin)), Auto)
	slices.Sort(names)
	return names
}

// Lookup returns a built-in theme.
func Lookup(name string) (Theme, bool) {
	t, ok := builtin[name]
	return t, ok
}

// Resolve returns the theme to use for name: a built-in theme, the custom
// one, or for auto the light or dark default depending on dark. Unknown
// names fall back to the default dark theme with an error describing why.
func Resolve(name string, custom *Theme, dark bool) (Theme, error) {
	switch name {
	case "", Auto:
		if dark {
			return builtin[DefaultDark], nil
		}
		return builtin[DefaultLight], nil
	case Custom:
		if custom == nil {
			return builtin[DefaultDark], fmt.Errorf("theme %q needs a [theme.custom] table", Custom)
		}
		return *custom, nil
	}
	if t, ok := builtin[name]; ok {
		return t, nil
	}
	return builtin[DefaultDark], fmt.Errorf("unknown theme %q (want one of %s)", name, strings.Join(append(Names(), Custom), ", "))
}

// Keys are the colour names a [theme.custom] table may set.
var Keys = []string{"base", "fg", "bg", "muted", "border", "accent", "selection", "success", "warning", "error", "info"}

// NewCustom builds a theme from a [theme.custom] table: "base" names the
// built-in theme it starts from (default catppuccin), and every other key
// overrides one colour, as "#rrggbb", an ANSI index 0-255, or "default".
func NewCustom(values map[string]string) (Theme, error) {
	baseName := values["base"]
	if baseName == "" {
		// Not DefaultDark: a custom theme written before hive became the
		// default keeps the colours it was written against.
		baseName = "catppuccin"
	}
	t, ok := builtin[baseName]
	if !ok {
		return Theme{}, fmt.Errorf("theme.custom: unknown base %q", baseName)
	}
	t.Name = Custom
	fields := map[string]*color.Color{
		"fg": &t.Fg, "bg": &t.Bg, "muted": &t.Muted, "border": &t.Border, "accent": &t.Accent,
		"selection": &t.Selection, "success": &t.Success, "warning": &t.Warning, "error": &t.Error, "info": &t.Info,
	}
	for _, k := range slices.Sorted(maps.Keys(values)) {
		if k == "base" {
			continue
		}
		dst, ok := fields[k]
		if !ok {
			return Theme{}, fmt.Errorf("theme.custom: unknown colour %q (want one of %s)", k, strings.Join(Keys, ", "))
		}
		c, err := ParseColor(values[k])
		if err != nil {
			return Theme{}, fmt.Errorf("theme.custom.%s: %w", k, err)
		}
		*dst = c
	}
	return t, nil
}

// ParseColor reads "#rrggbb", "#rgb", an ANSI index 0-255, or "default"
// (nil, the terminal's own colour).
func ParseColor(s string) (color.Color, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch {
	case s == "default" || s == "":
		return nil, nil
	case strings.HasPrefix(s, "#"):
		h := s[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) != 6 {
			return nil, fmt.Errorf("colour %q: want #rrggbb", s)
		}
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("colour %q: want #rrggbb", s)
		}
		return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 255 {
		return nil, fmt.Errorf("colour %q: want #rrggbb, 0-255 or default", s)
	}
	if n < 16 {
		return ansi.BasicColor(n), nil
	}
	return ansi.IndexedColor(n), nil
}
