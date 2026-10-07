package tui

import tea "github.com/charmbracelet/bubbletea"

// escapeSeqs are the VT sequences for keys that have no single-byte form.
var escapeSeqs = map[tea.KeyType]string{
	tea.KeyUp:       "\x1b[A",
	tea.KeyDown:     "\x1b[B",
	tea.KeyRight:    "\x1b[C",
	tea.KeyLeft:     "\x1b[D",
	tea.KeyHome:     "\x1b[H",
	tea.KeyEnd:      "\x1b[F",
	tea.KeyPgUp:     "\x1b[5~",
	tea.KeyPgDown:   "\x1b[6~",
	tea.KeyDelete:   "\x1b[3~",
	tea.KeyShiftTab: "\x1b[Z",
}

// keyToBytes converts a key press into the bytes a terminal would send to the
// program running in it.
func keyToBytes(msg tea.KeyMsg) []byte {
	var b []byte
	switch t := msg.Type; {
	case t == tea.KeyRunes:
		b = []byte(string(msg.Runes))
	case t == tea.KeySpace:
		b = []byte{' '}
	case t >= 0 && t < 32, t == 127:
		// Bubble Tea numbers control keys by their ASCII code (KeyCtrlA is 1,
		// KeyEnter is '\r', KeyEsc is 27, KeyBackspace is 127), so the key
		// type already is the byte to send.
		b = []byte{byte(t)}
	default:
		b = []byte(escapeSeqs[t])
	}
	if msg.Alt && len(b) > 0 {
		b = append([]byte{0x1b}, b...) // Alt/Meta is ESC-prefixed
	}
	return b
}
