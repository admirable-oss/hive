package mux

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/vt"
)

func TestEncodeKey(t *testing.T) {
	tests := []struct {
		name  string
		key   uv.Key
		modes vt.Modes
		want  string
	}{
		{"text", uv.Key{Code: 'a', Text: "a"}, 0, "a"},
		{"shifted text", uv.Key{Code: 'a', Mod: uv.ModShift, Text: "A"}, 0, "A"},
		{"unicode", uv.Key{Code: 'é', Text: "é"}, 0, "é"},
		{"ctrl+c", uv.Key{Code: 'c', Mod: uv.ModCtrl}, 0, "\x03"},
		{"ctrl+space", uv.Key{Code: uv.KeySpace, Mod: uv.ModCtrl}, 0, "\x00"},
		{"ctrl+]", uv.Key{Code: ']', Mod: uv.ModCtrl}, 0, "\x1d"},
		{"alt+x", uv.Key{Code: 'x', Mod: uv.ModAlt}, 0, "\x1bx"},
		{"alt+X", uv.Key{Code: 'x', Mod: uv.ModAlt | uv.ModShift}, 0, "\x1bX"},
		{"ctrl+alt+a", uv.Key{Code: 'a', Mod: uv.ModAlt | uv.ModCtrl}, 0, "\x1b\x01"},
		{"enter", uv.Key{Code: uv.KeyEnter}, 0, "\r"},
		{"alt+enter", uv.Key{Code: uv.KeyEnter, Mod: uv.ModAlt}, 0, "\x1b\r"},
		{"tab", uv.Key{Code: uv.KeyTab}, 0, "\t"},
		{"shift+tab", uv.Key{Code: uv.KeyTab, Mod: uv.ModShift}, 0, "\x1b[Z"},
		{"backspace", uv.Key{Code: uv.KeyBackspace}, 0, "\x7f"},
		{"ctrl+backspace", uv.Key{Code: uv.KeyBackspace, Mod: uv.ModCtrl}, 0, "\x08"},
		{"esc", uv.Key{Code: uv.KeyEscape}, 0, "\x1b"},
		{"space", uv.Key{Code: uv.KeySpace, Text: " "}, 0, " "},
		{"up", uv.Key{Code: uv.KeyUp}, 0, "\x1b[A"},
		{"up app cursor", uv.Key{Code: uv.KeyUp}, vt.ModeAppCursorKeys, "\x1bOA"},
		{"ctrl+left", uv.Key{Code: uv.KeyLeft, Mod: uv.ModCtrl}, 0, "\x1b[1;5D"},
		{"shift+alt+end", uv.Key{Code: uv.KeyEnd, Mod: uv.ModShift | uv.ModAlt}, 0, "\x1b[1;4F"},
		{"f1", uv.Key{Code: uv.KeyF1}, 0, "\x1bOP"},
		{"ctrl+f1", uv.Key{Code: uv.KeyF1, Mod: uv.ModCtrl}, 0, "\x1b[1;5P"},
		{"f5", uv.Key{Code: uv.KeyF5}, 0, "\x1b[15~"},
		{"delete", uv.Key{Code: uv.KeyDelete}, 0, "\x1b[3~"},
		{"shift+pgup", uv.Key{Code: uv.KeyPgUp, Mod: uv.ModShift}, 0, "\x1b[5;2~"},
		{"keypad 5", uv.Key{Code: uv.KeyKp5}, 0, "5"},
		{"caps lock ignored", uv.Key{Code: uv.KeyUp, Mod: uv.ModCapsLock}, 0, "\x1b[A"},
	}
	for _, tt := range tests {
		if got := string(encodeKey(tt.key, tt.modes)); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	if b := encodeKey(uv.Key{Code: uv.KeyLeftShift}, 0); b != nil {
		t.Errorf("a bare modifier has no encoding: %q", b)
	}
}

func TestEncodeMouse(t *testing.T) {
	left := uv.Mouse{X: 9, Y: 4, Button: uv.MouseLeft}
	sgr := vt.ModeMouseNormal | vt.ModeMouseSGR
	tests := []struct {
		name  string
		kind  mouseKind
		m     uv.Mouse
		modes vt.Modes
		want  string
	}{
		{"not tracking", mousePress, left, 0, ""},
		{"sgr press", mousePress, left, sgr, "\x1b[<0;3;2M"},
		{"sgr release", mouseRelease, left, sgr, "\x1b[<0;3;2m"},
		{"sgr wheel", mouseWheel, uv.Mouse{Button: uv.MouseWheelDown}, sgr, "\x1b[<65;3;2M"},
		{"ctrl click", mousePress, uv.Mouse{Button: uv.MouseRight, Mod: uv.ModCtrl}, sgr, "\x1b[<18;3;2M"},
		{"motion needs 1002/1003", mouseMotion, left, sgr, ""},
		{"drag with 1002", mouseMotion, left, vt.ModeMouseButton | vt.ModeMouseSGR, "\x1b[<32;3;2M"},
		{"hover needs 1003", mouseMotion, uv.Mouse{Button: uv.MouseNone}, vt.ModeMouseButton | vt.ModeMouseSGR, ""},
		{"hover with 1003", mouseMotion, uv.Mouse{Button: uv.MouseNone}, vt.ModeMouseAny | vt.ModeMouseSGR, "\x1b[<35;3;2M"},
		{"x10 press", mousePress, left, vt.ModeMouseNormal, "\x1b[M #\""},
		{"x10 release", mouseRelease, left, vt.ModeMouseNormal, "\x1b[M##\""},
		{"x10-only ignores release", mouseRelease, left, vt.ModeMouseX10, ""},
	}
	for _, tt := range tests {
		// Pane coordinates (2, 1) regardless of the screen position.
		if got := string(encodeMouse(tt.kind, tt.m, 2, 1, tt.modes)); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
