package vt_test

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

// randomOutput produces application-like output: text, colours, cursor
// motion, erases, scrolling, wide characters, titles and mode changes.
func randomOutput(r *rand.Rand, n int) []byte {
	pieces := []func() string{
		func() string { return "word" + fmt.Sprint(r.IntN(1000)) + " " },
		func() string { return "\r\n" },
		func() string { return fmt.Sprintf("\x1b[%d;%dH", 1+r.IntN(12), 1+r.IntN(40)) },
		func() string { return fmt.Sprintf("\x1b[38;5;%dm", r.IntN(256)) },
		func() string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r.IntN(256), r.IntN(256), r.IntN(256)) },
		func() string { return "\x1b[0m" },
		func() string { return "\x1b[1;4m" },
		func() string { return "\x1b[K" },
		func() string { return "\x1b[2J" },
		func() string { return "\x1b[3;8r\x1b[8;1H\n\x1b[r" },
		func() string { return "日本語🐝" },
		func() string { return "\x1b[2L" },
		func() string { return "\x1b]2;title " + fmt.Sprint(r.IntN(9)) + "\x07" },
		func() string { return "\x1b[?2004h" },
		func() string { return "\x1b[?25l" },
		func() string { return "\x1b[?25h" },
		func() string { return "\a" },
		func() string { return "\x1b[?1049h" },
		func() string { return "\x1b[?1049l" },
	}
	var b strings.Builder
	for range n {
		b.WriteString(pieces[r.IntN(len(pieces))]())
	}
	return []byte(b.String())
}

func assertSameScreen(t *testing.T, got, want *vt.Screen) {
	t.Helper()
	if got.Cols != want.Cols || got.Rows != want.Rows {
		t.Fatalf("size %dx%d, want %dx%d", got.Cols, got.Rows, want.Cols, want.Rows)
	}
	for y := range want.Rows {
		for x := range want.Cols {
			if got.Lines[y][x] != want.Lines[y][x] {
				t.Fatalf("cell (%d,%d) = %+v, want %+v\ngot:\n%s\nwant:\n%s", x, y, got.Lines[y][x], want.Lines[y][x], got.Text(), want.Text())
			}
		}
	}
	if got.Cursor != want.Cursor || got.Title != want.Title || got.Modes != want.Modes || got.Bells != want.Bells {
		t.Fatalf("meta differs: got cursor %+v title %q modes %b bells %d; want %+v %q %b %d",
			got.Cursor, got.Title, got.Modes, got.Bells, want.Cursor, want.Title, want.Modes, want.Bells)
	}
}

// TestFramesReproduceTheScreen is the frame pipeline's core property: a
// client applying every frame (encoded and decoded) ends with exactly the
// terminal's screen, however output and frames interleave.
func TestFramesReproduceTheScreen(t *testing.T) {
	for seed := range uint64(30) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, 7))
			term := newTerm(t, 40, 12, vt.Options{})
			client := &vt.Screen{}
			var view vt.View
			var wire bytes.Buffer
			fr := vt.NewFrameReader(&wire)
			for step := range 60 {
				_, _ = term.Write(randomOutput(r, 1+r.IntN(20)))
				if step%17 == 16 {
					term.Resize(30+r.IntN(20), 8+r.IntN(8))
				}
				// A slow client skips frames: changes accumulate in the view.
				if r.IntN(3) != 0 {
					continue
				}
				if f := term.Frame(&view); f != nil {
					wire.Write(vt.AppendFrame(nil, f))
					got, err := fr.Next()
					if err != nil {
						t.Fatal(err)
					}
					client.Apply(got)
				}
			}
			if f := term.Frame(&view); f != nil {
				client.Apply(f)
			}
			assertSameScreen(t, client, term.Snapshot())
			if term.Frame(&view) != nil {
				t.Fatal("no frame may be produced when nothing changed")
			}
		})
	}
}

func TestFrameCarriesOnlyChangedLines(t *testing.T) {
	term := newTerm(t, 20, 10, vt.Options{})
	var v vt.View
	_ = term.Frame(&v)
	_, _ = term.Write([]byte("\x1b[5;1Hchanged"))
	f := term.Frame(&v)
	if f == nil || f.Keyframe || len(f.Lines) != 1 || f.Lines[0].Y != 4 {
		t.Fatalf("frame = %+v, want one line update for line 4", f)
	}
	v.NeedsKeyframe()
	if f := term.Frame(&v); f == nil || !f.Keyframe || len(f.Lines) != 10 {
		t.Fatal("NeedsKeyframe must produce a full keyframe")
	}
}

// TestPainterReproducesTheScreen paints frames into a second emulator, as a
// real terminal would receive them, and checks it shows the same screen.
func TestPainterReproducesTheScreen(t *testing.T) {
	for seed := range uint64(20) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, 99))
			src := newTerm(t, 40, 12, vt.Options{})
			outer := newTerm(t, 40, 12, vt.Options{})
			p := vt.NewPainter(outer)
			var v vt.View
			for range 40 {
				_, _ = src.Write(randomOutput(r, 1+r.IntN(15)))
				if f := src.Frame(&v); f != nil {
					if err := p.Paint(f); err != nil {
						t.Fatal(err)
					}
				}
			}
			want, got := src.Snapshot(), outer.Snapshot()
			for y := range want.Rows {
				for x := range want.Cols {
					if got.Lines[y][x] != want.Lines[y][x] {
						t.Fatalf("cell (%d,%d) painted as %+v, want %+v\npainted:\n%s\nsource:\n%s", x, y, got.Lines[y][x], want.Lines[y][x], got.Text(), want.Text())
					}
				}
			}
			wantCursor := want.Cursor
			gotCursor := got.Cursor
			if gotCursor.X != wantCursor.X || gotCursor.Y != wantCursor.Y || gotCursor.Hidden != wantCursor.Hidden || gotCursor.Shape != wantCursor.Shape {
				t.Fatalf("cursor painted as %+v, want %+v", gotCursor, wantCursor)
			}
			if got.Title != want.Title || got.Modes&^vt.ModeAltScreen != want.Modes&^vt.ModeAltScreen {
				t.Fatalf("title/modes painted as %q %b, want %q %b", got.Title, got.Modes, want.Title, want.Modes)
			}
		})
	}
}

func TestPainterRestoreUndoesModes(t *testing.T) {
	src := newTerm(t, 20, 4, vt.Options{})
	outer := newTerm(t, 20, 4, vt.Options{})
	p := vt.NewPainter(outer)
	_, _ = src.Write([]byte("\x1b[?1h\x1b[?2004h\x1b[?1002;1006h\x1b[?25l"))
	var v vt.View
	_ = p.Paint(src.Frame(&v))
	if outer.Snapshot().Modes == 0 {
		t.Fatal("painter should mirror the modes")
	}
	_, _ = io.WriteString(outer, p.Restore())
	s := outer.Snapshot()
	if s.Modes != 0 || s.Cursor.Hidden {
		t.Fatalf("after Restore: modes %b hidden %v", s.Modes, s.Cursor.Hidden)
	}
}

func TestLineANSI(t *testing.T) {
	term := newTerm(t, 20, 1, vt.Options{})
	_, _ = term.Write([]byte("\x1b[31mred\x1b[0m plain   "))
	got := vt.LineANSI(term.Snapshot().Lines[0])
	if got != "\x1b[0;31mred\x1b[0m plain" {
		t.Fatalf("LineANSI = %q", got)
	}
}

func TestKeyframeRecreatesTheScreen(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 5))
	term := newTerm(t, 30, 8, vt.Options{})
	_, _ = term.Write(randomOutput(r, 40))
	want := term.Snapshot()
	f, err := vt.DecodeFrame(vt.AppendFrame(nil, want.Keyframe())[4:])
	if err != nil {
		t.Fatal(err)
	}
	got := &vt.Screen{}
	got.Apply(f)
	assertSameScreen(t, got, want)
}
