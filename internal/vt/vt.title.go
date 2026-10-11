package vt

import (
	"bytes"
	"encoding/base64"
	"strings"
	"unicode"
)

// maxTitleOSC bounds the OSC bytes held while looking for a title's end;
// a longer sequence passes through to the parser untouched. A clipboard
// set (OSC 52) may be up to maxClipboardOSC.
const (
	maxTitleOSC     = 4096
	maxClipboardOSC = 1 << 20
)

// titleFilter takes window titles (OSC 0 and 2), icon names (OSC 1) and
// clipboard sets (OSC 52) out of the output before x/ansi's parser sees
// them, and reads them itself.
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

// oscHandler receives the OSC sequences the filter takes.
type oscHandler interface {
	title(string)
	clipboard(Clipboard)
}

// filter returns the bytes of p that go to the parser, appended to dst,
// and passes on each complete title and clipboard set.
func (f *titleFilter) filter(dst, p []byte, on oscHandler) []byte {
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
				dst = f.finish(dst, []byte{bel}, on)
			case esc:
				f.state = titleOSCEsc
			case can, sub:
				// Cancelled: let the parser see exactly what came.
				dst = append(append(append(dst, esc, ']'), f.osc...), b)
				f.state = titleGround
			default:
				f.osc = append(f.osc, b)
				if len(f.osc) > maxTitleOSC && (len(f.osc) > maxClipboardOSC || !bytes.HasPrefix(f.osc, []byte("52;"))) {
					dst = append(append(dst, esc, ']'), f.osc...)
					f.state = titleGround // the parser takes the rest
				}
			}
		case titleOSCEsc:
			if b == '\\' {
				dst = f.finish(dst, []byte{esc, '\\'}, on)
				continue
			}
			// ESC ends the string without ST; the parser sees it as it
			// came, and this byte starts what follows the ESC.
			dst = append(append(dst, esc, ']'), f.osc...)
			f.state = titleEsc
			dst = f.filter(dst, []byte{b}, on)
		}
	}
	return dst
}

// finish handles a complete OSC: a title is taken, anything else is
// passed on with its terminator.
func (f *titleFilter) finish(dst, term []byte, on oscHandler) []byte {
	f.state = titleGround
	cmd, payload, ok := strings.Cut(string(f.osc), ";")
	if !ok {
		cmd, payload = string(f.osc), ""
	}
	switch cmd {
	case "0", "2":
		on.title(cleanTitle(payload))
		return dst
	case "52":
		// A program copies text: set the clipboard (where the user's
		// terminal allows it). Queries ("?") are not answered: a program
		// must not read what the user copied.
		sel, data, _ := strings.Cut(payload, ";")
		if c, ok := cleanClipboard(sel, data); ok {
			on.clipboard(c)
		}
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

// cleanClipboard checks an OSC 52 set: the selections are letters and
// digits (c, p, s, 0-7; empty means the clipboard), and the data is base64.
func cleanClipboard(sel, data string) (Clipboard, bool) {
	selection := func(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }
	if data == "?" || len(sel) > 16 || strings.ContainsFunc(sel, func(r rune) bool { return !selection(r) }) {
		return Clipboard{}, false
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return Clipboard{}, false
	}
	return Clipboard{Selection: sel, Data: data}, true
}
