package vt_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/vt"
)

func TestStylesAndColors(t *testing.T) {
	term := newTerm(t, 40, 2, vt.Options{})
	_, _ = term.Write([]byte("\x1b[1;31ma\x1b[0m\x1b[38;5;200;48;2;1;2;3mb\x1b[0m\x1b[4:3;58;5;9mc\x1b[0md"))
	line := term.Snapshot().Lines[0]
	tests := []struct {
		x    int
		want vt.Style
	}{
		{0, vt.Style{Fg: vt.Indexed(1), Attrs: vt.AttrBold}},
		{1, vt.Style{Fg: vt.Indexed(200), Bg: vt.RGB(1, 2, 3)}},
		{2, vt.Style{Underline: 3, UnderlineColor: vt.Indexed(9)}},
		{3, vt.Style{}},
	}
	for _, tt := range tests {
		if got := line[tt.x].Style; got != tt.want {
			t.Errorf("cell %d style = %+v, want %+v", tt.x, got, tt.want)
		}
	}
}

func TestWideCharacterCells(t *testing.T) {
	term := newTerm(t, 10, 1, vt.Options{})
	_, _ = term.Write([]byte("日x"))
	line := term.Snapshot().Lines[0]
	if line[0].Content != "日" || line[0].Width != 2 || line[1].Width != 0 || line[2].Content != "x" {
		t.Fatalf("unexpected cells %+v", line[:3])
	}
}

func TestModesAreTracked(t *testing.T) {
	term := newTerm(t, 10, 2, vt.Options{})
	_, _ = term.Write([]byte("\x1b[?1h\x1b=\x1b[?2004h\x1b[?1000;1006h\x1b[?1004h\x1b[?1049h"))
	want := vt.ModeAppCursorKeys | vt.ModeAppKeypad | vt.ModeBracketedPaste | vt.ModeMouseNormal | vt.ModeMouseSGR | vt.ModeFocusEvents | vt.ModeAltScreen
	if got := term.Snapshot().Modes; got != want {
		t.Fatalf("modes = %b, want %b", got, want)
	}
	_, _ = term.Write([]byte("\x1b[?1000l\x1b>\x1b[?1049l"))
	want &^= vt.ModeMouseNormal | vt.ModeAppKeypad | vt.ModeAltScreen
	if got := term.Snapshot().Modes; got != want {
		t.Fatalf("after reset of some: modes = %b, want %b", got, want)
	}
	_, _ = term.Write([]byte("\x1bc")) // RIS
	if got := term.Snapshot().Modes; got != 0 {
		t.Fatalf("after RIS: modes = %b, want 0", got)
	}
}

func TestCursorTitleAndBells(t *testing.T) {
	term := newTerm(t, 20, 5, vt.Options{})
	_, _ = term.Write([]byte("\x1b]2;agent: thinking\x07\x1b[3;4H\x1b[?25l\x1b[6 q\a\a"))
	s := term.Snapshot()
	if s.Title != "agent: thinking" || s.Bells != 2 {
		t.Fatalf("title %q bells %d", s.Title, s.Bells)
	}
	want := vt.Cursor{X: 3, Y: 2, Hidden: true, Shape: vt.CursorBar}
	if s.Cursor != want {
		t.Fatalf("cursor = %+v, want %+v", s.Cursor, want)
	}
}

func TestQueriesAreAnsweredWithoutBlocking(t *testing.T) {
	var (
		mu      sync.Mutex
		replies strings.Builder
		release = make(chan struct{})
	)
	term := newTerm(t, 80, 24, vt.Options{Reply: func(b []byte) {
		<-release // a PTY whose input nobody reads
		mu.Lock()
		replies.Write(b)
		mu.Unlock()
	}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more queries than the reply queue holds.
		for range 500 {
			_, _ = term.Write([]byte("\x1b[3;7H\x1b[6n"))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked on undelivered query replies")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := replies.String()
		mu.Unlock()
		if strings.HasPrefix(got, "\x1b[3;7R") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cursor position report was not delivered")
}

func TestScrollbackKeepsLinesWithinBudget(t *testing.T) {
	term := newTerm(t, 20, 5, vt.Options{ScrollbackBytes: 600})
	for i := range 200 {
		_, _ = term.Write([]byte("line " + itoa(i) + "\r\n"))
	}
	sb := term.Scrollback()
	if sb.Pushed() != 196 { // 200 lines + the empty last one, 5 rows visible
		t.Fatalf("pushed %d lines, want 196", sb.Pushed())
	}
	if sb.Bytes() > 600 || sb.Len() == 0 {
		t.Fatalf("scrollback holds %d lines in %d bytes; budget 600", sb.Len(), sb.Bytes())
	}
	tail := sb.Tail(2)
	if got := lineString(tail[1]); got != "line 195" {
		t.Fatalf("newest scrollback line = %q, want %q", got, "line 195")
	}
	if got := lineString(tail[0]); got != "line 194" {
		t.Fatalf("second newest = %q", got)
	}
}

func TestScrollbackCanBeDisabled(t *testing.T) {
	term := newTerm(t, 20, 2, vt.Options{ScrollbackBytes: -1})
	_, _ = term.Write([]byte(strings.Repeat("x\r\n", 50)))
	if term.Scrollback().Len() != 0 || term.Scrollback().Pushed() != 49 {
		t.Fatalf("len %d pushed %d", term.Scrollback().Len(), term.Scrollback().Pushed())
	}
}

func TestResizeKeepsContentAndForcesKeyframe(t *testing.T) {
	term := newTerm(t, 20, 4, vt.Options{})
	_, _ = term.Write([]byte("hello"))
	var v vt.View
	if f := term.Frame(&v); f == nil || !f.Keyframe {
		t.Fatal("first frame must be a keyframe")
	}
	term.Resize(30, 6)
	f := term.Frame(&v)
	if f == nil || !f.Keyframe || f.Cols != 30 || f.Rows != 6 {
		t.Fatalf("frame after resize = %+v, want a 30x6 keyframe", f)
	}
	if got := term.Snapshot().LineText(0); got != "hello" {
		t.Fatalf("content after resize = %q", got)
	}
}

func TestWriteAfterClose(t *testing.T) {
	term := vt.New(10, 2, vt.Options{})
	_ = term.Close()
	_ = term.Close()
	if _, err := term.Write([]byte("x")); err == nil {
		t.Fatal("write after close must fail")
	}
}

func lineString(cells []vt.Cell) string {
	s := vt.NewScreen(len(cells), 1)
	copy(s.Lines[0], cells)
	return s.LineText(0)
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + func() string {
		const digits = "0123456789"
		if i == 0 {
			return "0"
		}
		var b []byte
		for ; i > 0; i /= 10 {
			b = append([]byte{digits[i%10]}, b...)
		}
		return string(b)
	}())
}

func TestChangedLinesAndScrollbackSince(t *testing.T) {
	term := newTerm(t, 20, 3, vt.Options{})
	_, _ = term.Write([]byte("a\r\nb\r\nc"))
	v := term.Version()
	pushed := term.Scrollback().Pushed()
	_, _ = term.Write([]byte("\r\nd\r\ne"))
	changed := term.ChangedLines(v)
	if strings.Join(changed, ",") != "c,d,e" {
		t.Fatalf("changed lines = %q", changed)
	}
	lines, now := term.Scrollback().Since(pushed)
	if len(lines) != 2 || now != pushed+2 {
		t.Fatalf("scrolled off since: %d lines, count %d", len(lines), now)
	}
	if got := vt.LineText(lines[0]) + vt.LineText(lines[1]); got != "ab" {
		t.Fatalf("scrolled lines = %q", got)
	}
	if more, _ := term.Scrollback().Since(now); len(more) != 0 {
		t.Fatal("nothing new since now")
	}
	before := term.Version()
	term.Resize(30, 3)
	if len(term.ChangedLines(before)) != 3 {
		t.Fatal("after a resize every line counts as changed")
	}
}

func TestUnwrapJoinsFullWidthLines(t *testing.T) {
	term := newTerm(t, 10, 5, vt.Options{})
	_, _ = term.Write([]byte(strings.Repeat("x", 10) + "yy\r\nshort"))
	s := term.Snapshot()
	got := vt.Unwrap(s.Lines[:3], 10)
	if len(got) != 2 || got[0] != strings.Repeat("x", 10)+"yy" || got[1] != "short" {
		t.Fatalf("unwrapped = %q", got)
	}
}
