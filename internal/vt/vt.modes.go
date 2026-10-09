package vt

import (
	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
)

// decModes maps DEC private mode numbers to the modes clients mirror.
var decModes = map[int]Modes{
	1:    ModeAppCursorKeys,
	9:    ModeMouseX10,
	1000: ModeMouseNormal,
	1002: ModeMouseButton,
	1003: ModeMouseAny,
	1004: ModeFocusEvents,
	1005: ModeMouseUTF8,
	1006: ModeMouseSGR,
	1015: ModeMouseURXVT,
	2004: ModeBracketedPaste,
}

// trackModes observes mode changes without replacing the emulator's own
// handling: each handler returns false, so x/vt's default handler still runs.
func (t *Terminal) trackModes(e *xvt.Emulator) {
	set := func(on bool) xvt.CsiHandler {
		return func(params ansi.Params) bool {
			params.ForEach(-1, func(_, mode int, _ bool) {
				if m, ok := decModes[mode]; ok {
					if on {
						t.modes |= m
					} else {
						t.modes &^= m
					}
				}
			})
			return false
		}
	}
	e.RegisterCsiHandler(ansi.Command('?', 0, 'h'), set(true))
	e.RegisterCsiHandler(ansi.Command('?', 0, 'l'), set(false))
	e.RegisterEscHandler('=', func() bool { t.modes |= ModeAppKeypad; return false })
	e.RegisterEscHandler('>', func() bool { t.modes &^= ModeAppKeypad; return false })
	// RIS (ESC c) and DECSTR (CSI ! p) reset the terminal's modes.
	reset := func() { t.modes = 0; t.cursor.Hidden = false; t.cursor.Shape, t.cursor.Blink = CursorBlock, false }
	e.RegisterEscHandler('c', func() bool { reset(); return false })
	e.RegisterCsiHandler(ansi.Command(0, '!', 'p'), func(ansi.Params) bool { reset(); return false })
}
