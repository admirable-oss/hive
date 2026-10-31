package vt

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
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

// limitCounts caps the count of the CSI sequences x/vt runs one step at a
// time (CHT, CBT, ECH, REP), so one short sequence from a program cannot hold
// the terminal for seconds: x/vt loops the full count even once the cursor
// has stopped moving. Each handler rewrites the count in place and returns
// false, so x/vt's own handler runs with the capped value. The caps keep the
// outcome: a tab or erase never goes past the line, and REP keeps the same
// final screen and cursor, since past the first screen-and-a-line of copies
// every further line of them repeats.
func limitCounts(e *xvt.Emulator) {
	capFirst := func(limit func(n int) int) xvt.CsiHandler {
		return func(params ansi.Params) bool {
			if len(params) == 0 {
				return false
			}
			n := params[0].Param(1)
			if m := limit(n); m < n {
				params[0] = ansi.Param(m) | params[0]&ansi.Param(parser.HasMoreFlag)
			}
			return false
		}
	}
	line := capFirst(func(n int) int { return min(n, e.Width()) })
	e.RegisterCsiHandler('I', line)
	e.RegisterCsiHandler('Z', line)
	e.RegisterCsiHandler('X', line)
	e.RegisterCsiHandler('b', capFirst(func(n int) int {
		// x/vt repeats only a one-cell character, so a row is cols copies.
		cols, rows := e.Width(), e.Height()
		if settled := cols * (rows + 1); n > settled {
			return settled + (n-settled)%cols
		}
		return n
	}))
}
