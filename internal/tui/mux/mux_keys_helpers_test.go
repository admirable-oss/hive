//go:build !windows

package mux

import (
	"strings"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

var namedKeys = map[string]rune{
	"enter": uv.KeyEnter, "esc": uv.KeyEscape, "tab": uv.KeyTab, "space": uv.KeySpace,
	"backspace": uv.KeyBackspace, "up": uv.KeyUp, "down": uv.KeyDown, "left": uv.KeyLeft,
	"right": uv.KeyRight, "home": uv.KeyHome, "end": uv.KeyEnd, "pgup": uv.KeyPgUp, "pgdown": uv.KeyPgDown,
}

// key builds the event a terminal reports for a key.
func key(s string) uv.Key {
	var mod uv.KeyMod
	for {
		switch {
		case strings.HasPrefix(s, "ctrl+") && len(s) > 5:
			mod |= uv.ModCtrl
			s = s[5:]
			continue
		case strings.HasPrefix(s, "alt+") && len(s) > 4:
			mod |= uv.ModAlt
			s = s[4:]
			continue
		case strings.HasPrefix(s, "shift+") && len(s) > 6:
			mod |= uv.ModShift
			s = s[6:]
			continue
		}
		break
	}
	if code, ok := namedKeys[s]; ok {
		k := uv.Key{Code: code, Mod: mod}
		if code == uv.KeySpace && mod == 0 {
			k.Text = " "
		}
		return k
	}
	r, _ := utf8.DecodeRuneInString(s)
	if mod&(uv.ModCtrl|uv.ModAlt) != 0 {
		if unicode.IsUpper(r) {
			mod |= uv.ModShift
		}
		return uv.Key{Code: unicode.ToLower(r), Mod: mod}
	}
	if unicode.IsUpper(r) {
		return uv.Key{Code: unicode.ToLower(r), ShiftedCode: r, Mod: uv.ModShift, Text: s}
	}
	return uv.Key{Code: r, Text: s}
}
