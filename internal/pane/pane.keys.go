package pane

import (
	"fmt"
	"strings"

	"github.com/admirable-oss/hive/internal/vt"
)

// namedKeys are the keys send-keys knows by name (case-insensitive). Arrow
// and Home/End keys depend on the application cursor mode, as on a real
// terminal.
var namedKeys = map[string]string{
	"enter": "\r", "return": "\r", "tab": "\t", "space": " ",
	"escape": "\x1b", "esc": "\x1b", "backspace": "\x7f", "bs": "\x7f",
	"delete": "\x1b[3~", "del": "\x1b[3~", "insert": "\x1b[2~",
	"pageup": "\x1b[5~", "pgup": "\x1b[5~", "pagedown": "\x1b[6~", "pgdn": "\x1b[6~",
	"btab": "\x1b[Z", "backtab": "\x1b[Z",
	"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
	"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
	"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
}

var cursorKeys = map[string]byte{"up": 'A', "down": 'B', "right": 'C', "left": 'D', "home": 'H', "end": 'F'}

// EncodeKeys turns key names into the bytes a terminal sends:
//
//	Enter Tab Space Escape Backspace Delete Insert Home End PageUp PageDown
//	Up Down Left Right BTab F1…F12
//	C-x (Ctrl), M-x (Alt/Meta), combinable: C-M-x
//
// Any other single character is sent as itself.
func EncodeKeys(keys []string, modes vt.Modes) ([]byte, error) {
	var out []byte
	for _, k := range keys {
		b, err := encodeKey(k, modes)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func encodeKey(k string, modes vt.Modes) ([]byte, error) {
	meta, ctrl := false, false
	for {
		switch {
		case len(k) > 2 && (strings.HasPrefix(k, "M-") || strings.HasPrefix(k, "A-")):
			meta, k = true, k[2:]
			continue
		case len(k) > 2 && strings.HasPrefix(k, "C-"):
			ctrl, k = true, k[2:]
			continue
		}
		break
	}
	var b []byte
	lower := strings.ToLower(k)
	switch {
	case cursorKeys[lower] != 0:
		if modes&vt.ModeAppCursorKeys != 0 {
			b = []byte{0x1b, 'O', cursorKeys[lower]}
		} else {
			b = []byte{0x1b, '[', cursorKeys[lower]}
		}
	case namedKeys[lower] != "":
		b = []byte(namedKeys[lower])
	case len([]rune(k)) == 1:
		b = []byte(k)
	default:
		return nil, fmt.Errorf("%w: unknown key %q", ErrInvalid, k)
	}
	if ctrl {
		if len(b) != 1 {
			return nil, fmt.Errorf("%w: Ctrl cannot combine with %q", ErrInvalid, k)
		}
		c := b[0]
		switch {
		case c >= 'a' && c <= 'z':
			c -= 'a' - 1
		case c >= '@' && c <= '_':
			c -= '@'
		case c == ' ':
			c = 0
		case c == '?':
			c = 0x7f
		default:
			return nil, fmt.Errorf("%w: no Ctrl form of %q", ErrInvalid, k)
		}
		b = []byte{c}
	}
	if meta {
		b = append([]byte{0x1b}, b...)
	}
	return b, nil
}
