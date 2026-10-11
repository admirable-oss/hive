package vt

// Frame is a change to a screen: whole lines, plus the cursor, title, modes
// and bell count, which are always included because they are small. A
// keyframe replaces the whole screen; any other frame changes only the lines
// it lists.
type Frame struct {
	Keyframe   bool
	Cols, Rows int
	Cursor     Cursor
	Title      string
	Modes      Modes
	Bells      uint32
	Lines      []LineUpdate
	// Clipboard is set when a program copied text since the viewer's
	// previous frame (OSC 52); it is not part of the screen.
	Clipboard *Clipboard
}

// Clipboard is text a program asked the terminal to copy (OSC 52).
type Clipboard struct {
	// Selection names where it goes, as the program said: "c" is the
	// clipboard, "p" the primary selection; empty means the clipboard.
	Selection string
	// Data is the text, base64-encoded as the program sent it.
	Data string
}

// LineUpdate replaces line Y. Cells may be shorter than the screen width;
// the rest of the line is blank.
type LineUpdate struct {
	Y     int
	Cells []Cell
}

// View tracks what one viewer has been sent, so each frame carries only the
// lines that changed since. Its zero value is a viewer that has seen
// nothing, whose first frame is a keyframe.
type View struct {
	started bool
	at      uint64 // terminal version covered by the last frame
	resized uint64
	meta    Screen // cursor, title, modes and bells last sent (no lines)
	clips   uint64 // clipboard sets already passed on
}

// NeedsKeyframe makes the view's next frame a keyframe, for a viewer whose
// copy of the screen can no longer be trusted.
func (v *View) NeedsKeyframe() { v.started = false }

// Frame returns what changed for v since its previous frame, or nil when
// nothing did. Lines are copied, so the frame stays valid after the
// terminal changes again.
func (t *Terminal) Frame(v *View) *Frame {
	snapMeta := Screen{Cols: t.screen.Cols, Rows: t.screen.Rows}
	t.fillMeta(&snapMeta)
	key := !v.started || v.resized != t.resized
	// A viewer gets the clipboard sets made while it watched, not earlier.
	clip := v.started && t.clips > v.clips
	if !key && !clip && v.at == t.version && sameMeta(&v.meta, &snapMeta) {
		return nil
	}
	f := &Frame{
		Keyframe: key,
		Cols:     snapMeta.Cols,
		Rows:     snapMeta.Rows,
		Cursor:   snapMeta.Cursor,
		Title:    snapMeta.Title,
		Modes:    snapMeta.Modes,
		Bells:    snapMeta.Bells,
	}
	if clip {
		c := t.clip
		f.Clipboard = &c
	}
	v.clips = t.clips
	for y, ver := range t.versions {
		if key || ver > v.at {
			f.Lines = append(f.Lines, LineUpdate{Y: y, Cells: append([]Cell(nil), trimBlank(t.screen.Lines[y])...)})
		}
	}
	v.started, v.at, v.resized, v.meta = true, t.version, t.resized, snapMeta
	return f
}

func sameMeta(a, b *Screen) bool {
	return a.Cols == b.Cols && a.Rows == b.Rows && a.Cursor == b.Cursor &&
		a.Title == b.Title && a.Modes == b.Modes && a.Bells == b.Bells
}

// Apply updates s with f. A keyframe, or a frame for another size, resets
// the screen first.
func (s *Screen) Apply(f *Frame) {
	if f.Keyframe || f.Cols != s.Cols || f.Rows != s.Rows {
		s.Reset(f.Cols, f.Rows)
	}
	for _, l := range f.Lines {
		if l.Y < 0 || l.Y >= s.Rows {
			continue
		}
		line := s.Lines[l.Y]
		n := copy(line, l.Cells)
		for x := n; x < len(line); x++ {
			line[x] = Blank
		}
	}
	s.Cursor, s.Title, s.Modes, s.Bells = f.Cursor, f.Title, f.Modes, f.Bells
}

// Keyframe returns a frame that recreates s exactly.
func (s *Screen) Keyframe() *Frame {
	f := &Frame{Keyframe: true, Cols: s.Cols, Rows: s.Rows, Cursor: s.Cursor, Title: s.Title, Modes: s.Modes, Bells: s.Bells}
	for y, l := range s.Lines {
		f.Lines = append(f.Lines, LineUpdate{Y: y, Cells: append([]Cell(nil), trimBlank(l)...)})
	}
	return f
}

// Coalescer merges frames for a consumer slower than their producer: Take
// returns one frame that brings the consumer's screen up to date with every
// frame added since the previous Take (the lines any of them changed, as
// they are now). A consumer that keeps up gets the frames as they came.
type Coalescer struct {
	screen Screen
	dirty  map[int]bool
	key    bool   // a keyframe or a resize since the last Take
	n      int    // frames added since the last Take
	last   *Frame // the latest of them
	clip   *Clipboard
}

// Add records a frame.
func (c *Coalescer) Add(f *Frame) {
	if f.Keyframe || f.Cols != c.screen.Cols || f.Rows != c.screen.Rows {
		c.key = true
		clear(c.dirty)
	}
	c.screen.Apply(f)
	if !c.key {
		if c.dirty == nil {
			c.dirty = map[int]bool{}
		}
		for _, l := range f.Lines {
			c.dirty[l.Y] = true
		}
	}
	if f.Clipboard != nil {
		c.clip = f.Clipboard
	}
	c.n++
	c.last = f
}

// Take returns the merged frame and starts over, or nil when no frame was
// added since the previous Take.
func (c *Coalescer) Take() *Frame {
	defer func() { c.n, c.last, c.key, c.clip = 0, nil, false, nil; clear(c.dirty) }()
	var f *Frame
	switch {
	case c.n == 0:
		return nil
	case c.n == 1:
		return c.last
	case c.key:
		f = c.screen.Keyframe()
	default:
		s := &c.screen
		f = &Frame{Cols: s.Cols, Rows: s.Rows, Cursor: s.Cursor, Title: s.Title, Modes: s.Modes, Bells: s.Bells}
		for y := range s.Lines {
			if c.dirty[y] {
				f.Lines = append(f.Lines, LineUpdate{Y: y, Cells: append([]Cell(nil), trimBlank(s.Lines[y])...)})
			}
		}
	}
	f.Clipboard = c.clip // the latest copy wins, as on a terminal
	return f
}
