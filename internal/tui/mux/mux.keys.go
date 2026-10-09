package mux

import (
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/vt"
)

// csiKeys are keys sent as CSI <n> ~ (with ;<mod> when modified).
var csiKeys = map[rune]int{
	uv.KeyInsert: 2, uv.KeyDelete: 3, uv.KeyPgUp: 5, uv.KeyPgDown: 6,
	uv.KeyF5: 15, uv.KeyF6: 17, uv.KeyF7: 18, uv.KeyF8: 19,
	uv.KeyF9: 20, uv.KeyF10: 21, uv.KeyF11: 23, uv.KeyF12: 24,
}

// letterKeys are keys sent as CSI <letter>, or SS3 <letter> in application
// cursor mode (arrows, Home, End) or always (F1-F4) when unmodified.
var letterKeys = map[rune]byte{
	uv.KeyUp: 'A', uv.KeyDown: 'B', uv.KeyRight: 'C', uv.KeyLeft: 'D',
	uv.KeyHome: 'H', uv.KeyEnd: 'F', uv.KeyBegin: 'E',
	uv.KeyF1: 'P', uv.KeyF2: 'Q', uv.KeyF3: 'R', uv.KeyF4: 'S',
}

// keypad keys type their character.
var keypad = map[rune]string{
	uv.KeyKpEnter: "\r", uv.KeyKpEqual: "=", uv.KeyKpMultiply: "*", uv.KeyKpPlus: "+",
	uv.KeyKpComma: ",", uv.KeyKpMinus: "-", uv.KeyKpDecimal: ".", uv.KeyKpDivide: "/",
	uv.KeyKp0: "0", uv.KeyKp1: "1", uv.KeyKp2: "2", uv.KeyKp3: "3", uv.KeyKp4: "4",
	uv.KeyKp5: "5", uv.KeyKp6: "6", uv.KeyKp7: "7", uv.KeyKp8: "8", uv.KeyKp9: "9",
}

// xtermMod is the modifier parameter of xterm's modified-key sequences.
func xtermMod(m uv.KeyMod) int {
	n := 1
	if m.Contains(uv.ModShift) {
		n++
	}
	if m.Contains(uv.ModAlt) {
		n += 2
	}
	if m.Contains(uv.ModCtrl) {
		n += 4
	}
	return n
}

// encodeKey returns the bytes a terminal sends for k to a program in the
// given modes, as xterm does. nil means the key has no encoding.
func encodeKey(k uv.Key, modes vt.Modes) []byte {
	mods := k.Mod &^ (uv.ModCapsLock | uv.ModNumLock | uv.ModScrollLock)
	alt := mods.Contains(uv.ModAlt)
	withAlt := func(b []byte) []byte {
		if alt && b != nil {
			return append([]byte{0x1b}, b...)
		}
		return b
	}

	if l, ok := letterKeys[k.Code]; ok {
		if mods == 0 {
			isF := k.Code >= uv.KeyF1 && k.Code <= uv.KeyF4
			if isF || modes&vt.ModeAppCursorKeys != 0 {
				return []byte{0x1b, 'O', l}
			}
			return []byte{0x1b, '[', l}
		}
		return fmt.Appendf(nil, "\x1b[1;%d%c", xtermMod(mods), l)
	}
	if n, ok := csiKeys[k.Code]; ok {
		if mods == 0 {
			return fmt.Appendf(nil, "\x1b[%d~", n)
		}
		return fmt.Appendf(nil, "\x1b[%d;%d~", n, xtermMod(mods))
	}
	if s, ok := keypad[k.Code]; ok {
		return withAlt([]byte(s))
	}

	ctrl := mods.Contains(uv.ModCtrl)
	switch k.Code {
	case uv.KeyEnter:
		return withAlt([]byte{'\r'})
	case uv.KeyTab:
		if mods.Contains(uv.ModShift) {
			return []byte("\x1b[Z")
		}
		return withAlt([]byte{'\t'})
	case uv.KeyBackspace:
		if ctrl {
			return withAlt([]byte{0x08})
		}
		return withAlt([]byte{0x7f})
	case uv.KeyEscape:
		return withAlt([]byte{0x1b})
	case uv.KeySpace:
		if ctrl {
			return withAlt([]byte{0})
		}
		return withAlt([]byte{' '})
	}

	if ctrl {
		if b, ok := ctrlByte(k.Code); ok {
			return withAlt([]byte{b})
		}
		return nil
	}
	if k.Text != "" {
		return withAlt([]byte(k.Text))
	}
	if k.Code > 0 && k.Code < uv.KeyExtended {
		r := k.Code
		if mods.Contains(uv.ModShift) && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		return withAlt([]byte(string(r)))
	}
	return nil
}

// ctrlByte is the control character for ctrl+r.
func ctrlByte(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r - 'a' + 1), true
	case r >= 'A' && r <= 'Z':
		return byte(r - 'A' + 1), true
	case r == '@' || r == '2' || r == ' ':
		return 0, true
	case r == '[' || r == '3':
		return 0x1b, true
	case r == '\\' || r == '4':
		return 0x1c, true
	case r == ']' || r == '5':
		return 0x1d, true
	case r == '^' || r == '6':
		return 0x1e, true
	case r == '_' || r == '7' || r == '-' || r == '/':
		return 0x1f, true
	case r == '?' || r == '8':
		return 0x7f, true
	}
	return 0, false
}

// mouseTracking reports whether a program asked for mouse events.
func mouseTracking(m vt.Modes) bool {
	return m&(vt.ModeMouseX10|vt.ModeMouseNormal|vt.ModeMouseButton|vt.ModeMouseAny) != 0
}

// mouseKind is what happened to the mouse.
type mouseKind uint8

const (
	mousePress mouseKind = iota
	mouseRelease
	mouseMotion
	mouseWheel
)

// encodeMouse returns the report of a mouse event at cell (x, y) of a pane
// for a program in the given modes, or nil when the program did not ask for
// this kind of event. Reports use SGR encoding when enabled, else the
// legacy X10 form (coordinates up to 223).
func encodeMouse(kind mouseKind, m uv.Mouse, x, y int, modes vt.Modes) []byte {
	if !mouseTracking(modes) {
		return nil
	}
	switch kind {
	case mouseMotion:
		dragging := m.Button != uv.MouseNone
		if modes&vt.ModeMouseAny == 0 && (modes&vt.ModeMouseButton == 0 || !dragging) {
			return nil
		}
	case mouseRelease:
		if modes&vt.ModeMouseX10 != 0 && modes&(vt.ModeMouseNormal|vt.ModeMouseButton|vt.ModeMouseAny) == 0 {
			return nil // X10 reports presses only
		}
	}
	var code int
	switch m.Button {
	case uv.MouseLeft:
		code = 0
	case uv.MouseMiddle:
		code = 1
	case uv.MouseRight:
		code = 2
	case uv.MouseNone:
		code = 3
	case uv.MouseWheelUp:
		code = 64
	case uv.MouseWheelDown:
		code = 65
	case uv.MouseWheelLeft:
		code = 66
	case uv.MouseWheelRight:
		code = 67
	default:
		return nil
	}
	if m.Mod.Contains(uv.ModShift) {
		code += 4
	}
	if m.Mod.Contains(uv.ModAlt) {
		code += 8
	}
	if m.Mod.Contains(uv.ModCtrl) {
		code += 16
	}
	if kind == mouseMotion {
		code += 32
	}
	if modes&vt.ModeMouseSGR != 0 {
		final := 'M'
		if kind == mouseRelease {
			final = 'm'
		}
		return fmt.Appendf(nil, "\x1b[<%d;%d;%d%c", code, x+1, y+1, final)
	}
	if kind == mouseRelease {
		code = 3 | code&^3 // legacy releases do not say which button
	}
	if x > 222 || y > 222 {
		return nil
	}
	return []byte{0x1b, '[', 'M', byte(32 + code), byte(33 + x), byte(33 + y)}
}
