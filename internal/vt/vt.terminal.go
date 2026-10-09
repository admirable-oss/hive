package vt

import (
	"errors"
	"io"

	uv "github.com/charmbracelet/ultraviolet"
	xvt "github.com/charmbracelet/x/vt"
)

// DefaultScrollbackBytes bounds a terminal's scrollback when Options leave
// it unset.
const DefaultScrollbackBytes = 10 << 20

// maxWriteChunk bounds how much is parsed between scrollback drains. The
// emulator's own scrollback (a staging area) must hold every line one chunk
// can scroll off, and a chunk scrolls at most one line per byte.
const maxWriteChunk = 16 << 10

// replyQueue bounds query answers waiting to be written back to the PTY.
const replyQueue = 64

// ErrClosed is returned by Write after Close.
var ErrClosed = errors.New("vt: terminal closed")

// Options configures a Terminal.
type Options struct {
	// Reply receives the terminal's answers to the application's queries
	// (cursor position, device attributes, …), which must be written back to
	// the application's input. It runs on its own goroutine, so Write never
	// waits for it. Nil discards the answers.
	Reply func([]byte)
	// ScrollbackBytes bounds the history of lines that scrolled off the
	// screen. Zero means DefaultScrollbackBytes; negative keeps none.
	ScrollbackBytes int
}

// Terminal is an emulated terminal: write application output in, read
// snapshots, frames and scrollback out. It is not safe for concurrent use;
// the owner serialises access (the PTY session holds a lock).
type Terminal struct {
	emu *xvt.Emulator

	screen   *Screen  // mirror of the emulator's screen in Hive cells
	versions []uint64 // per line: the change counter when it last changed
	version  uint64   // bumped on every change
	resized  uint64   // version at the last resize (forces keyframes)

	cursor Cursor // hidden/shape/blink, tracked from callbacks
	title  string
	modes  Modes
	bells  uint32

	scrollback *Scrollback

	replies chan []byte
	closed  bool
	done    chan struct{}
}

// New returns a cols×rows terminal.
func New(cols, rows int, opts Options) *Terminal {
	cols, rows = ClampSize(cols, rows)
	budget := opts.ScrollbackBytes
	if budget == 0 {
		budget = DefaultScrollbackBytes
	}
	t := &Terminal{
		emu:        xvt.NewEmulator(cols, rows),
		screen:     NewScreen(cols, rows),
		versions:   make([]uint64, rows),
		scrollback: NewScrollback(max(budget, 0)),
		replies:    make(chan []byte, replyQueue),
		done:       make(chan struct{}),
	}
	t.emu.SetScrollbackSize(maxWriteChunk + 1)
	t.emu.SetCallbacks(xvt.Callbacks{
		Bell:             func() { t.bells++ },
		Title:            func(s string) { t.title = s },
		CursorVisibility: func(visible bool) { t.cursor.Hidden = !visible },
		// x/vt passes "steady" here, not "blink" (it calls back with !blink);
		// TestCursorTitleAndBells pins this down in case upstream changes.
		CursorStyle: func(style xvt.CursorStyle, steady bool) {
			t.cursor.Shape, t.cursor.Blink = CursorShape(style), !steady
		},
	})
	t.trackModes(t.emu)
	go t.pumpReplies(opts.Reply)
	t.sync()
	return t
}

// pumpReplies moves query answers from the emulator to reply. The emulator
// writes answers synchronously into a pipe during Write, so the pipe is
// always drained here; answers that cannot be delivered fast enough are
// dropped rather than stalling the terminal.
func (t *Terminal) pumpReplies(reply func([]byte)) {
	deliver := make(chan struct{})
	go func() {
		defer close(deliver)
		for b := range t.replies {
			if reply != nil {
				reply(b)
			}
		}
	}()
	buf := make([]byte, 256)
	for {
		n, err := t.emu.Read(buf)
		if n > 0 {
			select {
			case t.replies <- append([]byte(nil), buf[:n]...):
			default: // the application is not reading its input; drop
			}
		}
		if err != nil {
			break
		}
	}
	close(t.replies)
	<-deliver
	close(t.done)
}

// Write feeds application output to the terminal.
func (t *Terminal) Write(p []byte) (int, error) {
	if t.closed {
		return 0, ErrClosed
	}
	total := len(p)
	for len(p) > 0 {
		n := min(len(p), maxWriteChunk)
		_, _ = t.emu.Write(p[:n])
		t.drainScrollback()
		p = p[n:]
	}
	t.sync()
	return total, nil
}

// Resize changes the terminal size. Content is kept where it fits.
func (t *Terminal) Resize(cols, rows int) {
	cols, rows = ClampSize(cols, rows)
	if cols == t.screen.Cols && rows == t.screen.Rows {
		return
	}
	t.emu.Resize(cols, rows)
	t.drainScrollback()
	t.sync()
}

// Size returns the terminal's columns and rows.
func (t *Terminal) Size() (cols, rows int) { return t.screen.Cols, t.screen.Rows }

// Snapshot returns a copy of the current screen.
func (t *Terminal) Snapshot() *Screen {
	s := t.screen.Clone()
	t.fillMeta(s)
	return s
}

// Scrollback returns the lines that scrolled off the top of the screen.
func (t *Terminal) Scrollback() *Scrollback { return t.scrollback }

// Close stops the terminal. It is safe to call more than once.
func (t *Terminal) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	// End the reply reader by closing the pipe it reads, and only then mark
	// the emulator closed: x/vt's Close sets a flag its Read checks without
	// a lock, so doing it while the reader runs would be a data race.
	if pw, ok := t.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
		<-t.done
		_ = t.emu.Close()
		return nil
	}
	_ = t.emu.Close()
	<-t.done
	return nil
}

func (t *Terminal) fillMeta(s *Screen) {
	pos := t.emu.CursorPosition()
	s.Cursor = t.cursor
	s.Cursor.X, s.Cursor.Y = clamp(pos.X, 0, s.Cols-1), clamp(pos.Y, 0, s.Rows-1)
	s.Title = t.title
	s.Modes = t.modes &^ ModeAltScreen
	if t.emu.IsAltScreen() {
		s.Modes |= ModeAltScreen
	}
	s.Bells = t.bells
}

// sync brings the mirror up to date with the emulator, recording which lines
// changed. Comparing every cell is cheap next to parsing, and it does not
// depend on the emulator's internal damage tracking.
func (t *Terminal) sync() {
	cols, rows := t.emu.Width(), t.emu.Height()
	if cols != t.screen.Cols || rows != t.screen.Rows {
		t.screen.Reset(cols, rows)
		t.versions = make([]uint64, rows)
		t.version++
		t.resized = t.version
	}
	for y := range rows {
		if convertLine(t.screen.Lines[y], func(x int) *uv.Cell { return t.emu.CellAt(x, y) }) {
			t.version++
			t.versions[y] = t.version
		}
	}
}

// drainScrollback moves lines the emulator scrolled off into Hive's own
// byte-budgeted store, keeping the emulator's copy (112-byte cells) empty.
func (t *Terminal) drainScrollback() {
	sb := t.emu.Scrollback()
	if sb == nil || sb.Len() == 0 {
		return
	}
	for _, line := range sb.Lines() {
		cells := make([]Cell, len(line))
		convertLine(cells, func(x int) *uv.Cell { return &line[x] })
		t.scrollback.Push(cells)
	}
	sb.Clear()
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

var _ io.Writer = (*Terminal)(nil)

// Version returns the terminal's change counter: it increases whenever a
// line changes or the terminal is resized.
func (t *Terminal) Version() uint64 { return t.version }

// ChangedLines returns the screen lines that changed after version since
// (every line after a resize), with their current text.
func (t *Terminal) ChangedLines(since uint64) []string {
	var out []string
	all := t.resized > since
	for y, v := range t.versions {
		if all || v > since {
			out = append(out, LineText(t.screen.Lines[y]))
		}
	}
	return out
}
