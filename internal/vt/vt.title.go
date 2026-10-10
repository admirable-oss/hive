package vt

import (
	"strings"
	"unicode"
)

// maxTitleOSC bounds the OSC bytes held while looking for a title's end;
// a longer sequence passes through to the parser untouched.
const maxTitleOSC = 4096

// titleFilter takes window titles (OSC 0 and 2) and icon names (OSC 1) out
// of the output before x/ansi's parser sees them, and reads them itself.
//
// The parser ends a string sequence at byte 0x9c, the 8-bit String
// Terminator. In UTF-8 output that byte is only ever a continuation byte:
// "✳" is e2 9c b3, so Claude Code's "✳ Claude Code" title came out as the
// lone byte e2. Terminals in UTF-8 mode do not treat a raw 0x9c as ST
// either, and Hive's terminals are UTF-8, so titles are parsed here. Every
// other byte reaches the parser unchanged, other OSC sequences (links,
// colours, clipboard) included.
type titleFilter struct {
	state titleState
	osc   []byte // bytes after ESC ] while collecting
}

type titleState uint8

const (
	titleGround titleState = iota
	titleEsc               // ESC seen
	titleOSC               // inside ESC ] …
	titleOSCEsc            // ESC seen inside an OSC: ST or the end of it
)

const (
	esc = 0x1b
	bel = 0x07
	can = 0x18
	sub = 0x1a
)

// filter returns the bytes of p that go to the parser, appended to dst,
// and calls setTitle for each complete title.
func (f *titleFilter) filter(dst, p []byte, setTitle func(string)) []byte {
	for _, b := range p {
		switch f.state {
		case titleGround:
			if b == esc {
				f.state = titleEsc
				continue
			}
			dst = append(dst, b)
		case titleEsc:
			switch b {
			case ']':
				f.state, f.osc = titleOSC, f.osc[:0]
			case esc:
				dst = append(dst, esc) // the first ESC stands alone; this one may start an OSC
			default:
				dst = append(dst, esc, b)
				f.state = titleGround
			}
		case titleOSC:
			switch b {
			case bel:
				dst = f.finish(dst, []byte{bel}, setTitle)
			case esc:
				f.state = titleOSCEsc
			case can, sub:
				// Cancelled: let the parser see exactly what came.
				dst = append(append(append(dst, esc, ']'), f.osc...), b)
				f.state = titleGround
			default:
				f.osc = append(f.osc, b)
				if len(f.osc) > maxTitleOSC {
					dst = append(append(dst, esc, ']'), f.osc...)
					f.state = titleGround // the parser takes the rest
				}
			}
		case titleOSCEsc:
			if b == '\\' {
				dst = f.finish(dst, []byte{esc, '\\'}, setTitle)
				continue
			}
			// ESC ends the string without ST; the parser sees it as it
			// came, and this byte starts what follows the ESC.
			dst = append(append(dst, esc, ']'), f.osc...)
			f.state = titleEsc
			dst = f.filter(dst, []byte{b}, setTitle)
		}
	}
	return dst
}

// finish handles a complete OSC: a title is taken, anything else is
// passed on with its terminator.
func (f *titleFilter) finish(dst, term []byte, setTitle func(string)) []byte {
	f.state = titleGround
	cmd, payload, ok := strings.Cut(string(f.osc), ";")
	if !ok {
		cmd, payload = string(f.osc), ""
	}
	switch cmd {
	case "0", "2":
		setTitle(cleanTitle(payload))
		return dst
	case "1":
		return dst // the icon name: not shown anywhere
	}
	return append(append(append(dst, esc, ']'), f.osc...), term...)
}

// cleanTitle makes a title safe to show and to send to other terminals:
// valid UTF-8 without control characters.
func cleanTitle(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
