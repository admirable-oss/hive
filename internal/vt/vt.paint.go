package vt

import (
	"bytes"
	"io"
	"strconv"
	"unicode/utf8"
)

// Painter draws frames on a real terminal, with the screen at its top-left
// corner. It keeps a copy of what is displayed and writes each frame as one
// batch, inside a synchronized update where the terminal supports it, so a
// client never sees a half-drawn frame.
type Painter struct {
	w       io.Writer
	screen  *Screen
	started bool
	buf     bytes.Buffer
}

// NewPainter returns a painter writing to w.
func NewPainter(w io.Writer) *Painter { return &Painter{w: w, screen: &Screen{}} }

// Paint applies f and draws the result.
func (p *Painter) Paint(f *Frame) error {
	prev := Screen{Modes: p.screen.Modes, Title: p.screen.Title, Bells: p.screen.Bells, Cursor: p.screen.Cursor}
	full := !p.started || f.Keyframe || f.Cols != p.screen.Cols || f.Rows != p.screen.Rows
	p.screen.Apply(f)

	b := &p.buf
	b.Reset()
	b.WriteString("\x1b[?2026h\x1b[?25l")
	if full {
		b.WriteString("\x1b[0m\x1b[H\x1b[2J")
		for y := range p.screen.Rows {
			p.writeLine(y)
		}
	} else {
		for _, l := range f.Lines {
			if l.Y >= 0 && l.Y < p.screen.Rows {
				p.writeLine(l.Y)
			}
		}
	}
	b.WriteString("\x1b[0m")
	if !p.started {
		prev.Modes = 0
		prev.Title = ""
		prev.Bells = p.screen.Bells // never ring for bells from before attach
	}
	b.WriteString(modeChanges(prev.Modes, p.screen.Modes))
	if p.screen.Title != prev.Title {
		b.WriteString("\x1b]2;" + sanitizeTitle(p.screen.Title) + "\x07")
	}
	if p.screen.Bells != prev.Bells {
		b.WriteByte('\a')
	}
	c := p.screen.Cursor
	b.WriteString("\x1b[" + strconv.Itoa(c.Y+1) + ";" + strconv.Itoa(c.X+1) + "H")
	if !p.started || c.Shape != prev.Cursor.Shape || c.Blink != prev.Cursor.Blink {
		b.WriteString(cursorShape(c))
	}
	if !c.Hidden {
		b.WriteString("\x1b[?25h")
	}
	b.WriteString("\x1b[?2026l")
	p.started = true
	_, err := p.w.Write(b.Bytes())
	return err
}

// writeLine draws line y: cursor to its start, cells with styles, then an
// erase to the end of the line in the default background.
func (p *Painter) writeLine(y int) {
	b := &p.buf
	b.WriteString("\x1b[" + strconv.Itoa(y+1) + "H")
	line := trimBlank(p.screen.Lines[y])
	var cur Style
	for x, c := range line {
		if c.Width == 0 {
			continue
		}
		if c.Style != cur {
			b.WriteString(sgr(c.Style))
			cur = c.Style
		}
		if c.Content == "" {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
		// Terminals disagree about the width of some characters (emoji,
		// ambiguous-width). Moving to the next column explicitly keeps one
		// disagreement from shifting the rest of the line.
		if c.Width != 1 || !isASCII(c.Content) {
			b.WriteString("\x1b[" + strconv.Itoa(x+int(c.Width)+1) + "G")
		}
	}
	b.WriteString("\x1b[0m")
	// Erase the rest of the line, unless the line is full: after writing the
	// last column the terminal waits to wrap, and an erase there would wipe
	// that last cell.
	if len(line) < p.screen.Cols {
		b.WriteString("\x1b[K")
	}
}

// Restore returns the sequences that undo every mode the painter set, for a
// client detaching from the screen.
func (p *Painter) Restore() string {
	return "\x1b[0m" + modeChanges(p.screen.Modes, 0) + "\x1b[0 q\x1b[?25h"
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// LineANSI renders cells as text with SGR styling, ending with a reset. It
// is for drawing a line inside another UI (the dashboard).
func LineANSI(cells []Cell) string {
	var b bytes.Buffer
	var cur Style
	for _, c := range trimBlank(cells) {
		if c.Width == 0 {
			continue
		}
		if c.Style != cur {
			b.WriteString(sgr(c.Style))
			cur = c.Style
		}
		if c.Content == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Content)
		}
	}
	if cur != (Style{}) {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// sgr returns the sequence that sets s from a reset state.
func sgr(s Style) string {
	b := []byte("\x1b[0")
	attrs := []struct {
		a    Attr
		code string
	}{
		{AttrBold, "1"},
		{AttrFaint, "2"},
		{AttrItalic, "3"},
		{AttrBlink, "5"},
		{AttrRapidBlink, "6"},
		{AttrReverse, "7"},
		{AttrConceal, "8"},
		{AttrStrikethrough, "9"},
	}
	for _, at := range attrs {
		if s.Attrs&at.a != 0 {
			b = append(b, ';')
			b = append(b, at.code...)
		}
	}
	switch {
	case s.Underline == 1:
		b = append(b, ";4"...)
	case s.Underline > 1:
		b = append(b, ";4:"...)
		b = strconv.AppendInt(b, int64(s.Underline), 10)
	}
	b = appendColor(b, s.Fg, 30, 90, 38)
	b = appendColor(b, s.Bg, 40, 100, 48)
	if !s.UnderlineColor.IsDefault() {
		b = appendColor(b, s.UnderlineColor, -1, -1, 58)
	}
	return string(append(b, 'm'))
}

// appendColor appends ";<code>" for c. Basic colours use base (0–7) and
// bright (8–15) codes when given; everything else uses the extended form.
func appendColor(b []byte, c Color, base, bright, ext int) []byte {
	if i, ok := c.Index(); ok {
		switch {
		case i < 8 && base >= 0:
			return strconv.AppendInt(append(b, ';'), int64(base)+int64(i), 10)
		case i < 16 && bright >= 0:
			return strconv.AppendInt(append(b, ';'), int64(bright)+int64(i-8), 10)
		}
		b = strconv.AppendInt(append(b, ';'), int64(ext), 10)
		return strconv.AppendInt(append(b, ";5;"...), int64(i), 10)
	}
	if r, g, bl, ok := c.RGB(); ok {
		b = strconv.AppendInt(append(b, ';'), int64(ext), 10)
		b = strconv.AppendInt(append(b, ";2;"...), int64(r), 10)
		b = strconv.AppendInt(append(b, ';'), int64(g), 10)
		return strconv.AppendInt(append(b, ';'), int64(bl), 10)
	}
	return b
}

// modeSeq lists, for each mode, the sequences that turn it on and off.
var modeSeq = []struct {
	m       Modes
	on, off string
}{
	{ModeAppCursorKeys, "\x1b[?1h", "\x1b[?1l"},
	{ModeAppKeypad, "\x1b=", "\x1b>"},
	{ModeBracketedPaste, "\x1b[?2004h", "\x1b[?2004l"},
	{ModeFocusEvents, "\x1b[?1004h", "\x1b[?1004l"},
	{ModeMouseX10, "\x1b[?9h", "\x1b[?9l"},
	{ModeMouseNormal, "\x1b[?1000h", "\x1b[?1000l"},
	{ModeMouseButton, "\x1b[?1002h", "\x1b[?1002l"},
	{ModeMouseAny, "\x1b[?1003h", "\x1b[?1003l"},
	{ModeMouseUTF8, "\x1b[?1005h", "\x1b[?1005l"},
	{ModeMouseSGR, "\x1b[?1006h", "\x1b[?1006l"},
	{ModeMouseURXVT, "\x1b[?1015h", "\x1b[?1015l"},
}

// modeChanges returns the sequences that move a terminal from modes from to
// modes to. The alternate screen is not mirrored: a client draws the screen
// itself, wherever it likes.
func modeChanges(from, to Modes) string {
	var b bytes.Buffer
	for _, ms := range modeSeq {
		switch {
		case to&ms.m != 0 && from&ms.m == 0:
			b.WriteString(ms.on)
		case to&ms.m == 0 && from&ms.m != 0:
			b.WriteString(ms.off)
		}
	}
	return b.String()
}

func cursorShape(c Cursor) string {
	n := 2 * (int(c.Shape) + 1) // steady block 2, underline 4, bar 6
	if c.Blink {
		n--
	}
	return "\x1b[" + strconv.Itoa(n) + " q"
}

// sanitizeTitle drops control characters so a title cannot inject sequences.
func sanitizeTitle(s string) string {
	b := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) {
			b = append(b, r)
		}
	}
	return string(b)
}

// ResetSequence turns off every mode a Painter can set and restores the
// default cursor, for a client that does not know which were set (it is
// painted by a remote painter).
func ResetSequence() string {
	all := Modes(0)
	for _, ms := range modeSeq {
		all |= ms.m
	}
	return "\x1b[0m" + modeChanges(all, 0) + "\x1b[0 q\x1b[?25h"
}
