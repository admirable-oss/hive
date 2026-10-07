package tui

import tea "github.com/charmbracelet/bubbletea"

// keyToBytes converts a BubbleTea key message to raw bytes for a PTY.
func keyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(msg.String())
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyBackspace:
		return []byte("\x7f") // commonly DEL or \b depending on terminal, \x7f is most standard PTY erase
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyEsc:
		return []byte("\x1b")
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyCtrlA:
		return []byte("\x01")
	case tea.KeyCtrlB:
		return []byte("\x02")
	case tea.KeyCtrlC:
		return []byte("\x03")
	case tea.KeyCtrlD:
		return []byte("\x04")
	case tea.KeyCtrlE:
		return []byte("\x05")
	case tea.KeyCtrlF:
		return []byte("\x06")
	case tea.KeyCtrlG:
		return []byte("\x07")
	case tea.KeyCtrlK:
		return []byte("\x0b")
	case tea.KeyCtrlL:
		return []byte("\x0c")
	case tea.KeyCtrlN:
		return []byte("\x0e")
	case tea.KeyCtrlP:
		return []byte("\x10")
	case tea.KeyCtrlR:
		return []byte("\x12")
	case tea.KeyCtrlU:
		return []byte("\x15")
	case tea.KeyCtrlW:
		return []byte("\x17")
	case tea.KeyCtrlZ:
		return []byte("\x1a")
	default:
		if len(msg.Runes) > 0 {
			return []byte(string(msg.Runes))
		}
		if s := msg.String(); len(s) > 0 && len(s) <= 4 {
			return []byte(s)
		}
	}
	return nil
}
