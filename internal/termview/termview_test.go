package termview_test

import (
	"testing"

	"github.com/admirable-oss/hive/internal/termview"
	"github.com/admirable-oss/hive/internal/vt"
)

func cells(s string) []vt.Cell {
	out := make([]vt.Cell, 0, len(s))
	for _, r := range s {
		out = append(out, vt.Cell{Content: string(r), Width: 1})
	}
	return out
}

func TestScreenCoalescesFrames(t *testing.T) {
	s := termview.NewScreen("v1")
	if _, ok := s.Take(); ok {
		t.Fatal("nothing to send before the first frame")
	}
	s.Apply(&vt.Frame{Keyframe: true, Cols: 10, Rows: 3, Lines: []vt.LineUpdate{{Y: 0, Cells: cells("hello")}}})
	s.Apply(&vt.Frame{Cols: 10, Rows: 3, Lines: []vt.LineUpdate{{Y: 2, Cells: cells("world")}}})
	u, ok := s.Take()
	if !ok || !u.Full || u.Seq != 1 || u.Pane != "v1" || len(u.Lines) != 3 || termview.LineText(u.Lines[0]) != "hello" || termview.LineText(u.Lines[2]) != "world" {
		t.Fatalf("a keyframe and a change are one full update: %+v", u)
	}
	if _, ok := s.Take(); ok {
		t.Fatal("nothing changed since")
	}
	s.Apply(&vt.Frame{Cols: 10, Rows: 3, Lines: []vt.LineUpdate{{Y: 1, Cells: cells("a")}}})
	s.Apply(&vt.Frame{Cols: 10, Rows: 3, Lines: []vt.LineUpdate{{Y: 1, Cells: cells("ab")}}})
	u, _ = s.Take()
	if u.Full || u.Seq != 2 || len(u.Lines) != 1 || termview.LineText(u.Lines[0]) != "ab" {
		t.Fatalf("two changes of a line are one line, the latest: %+v", u)
	}
	s.Refresh()
	if u, _ = s.Take(); !u.Full || len(u.Lines) != 3 {
		t.Fatalf("a refresh sends every line: %+v", u)
	}
	if got := s.Text(); len(got) != 3 || got[1] != "ab" {
		t.Fatalf("text: %q", got)
	}
	s.End()
	if u, ok = s.Take(); !ok || !u.Ended {
		t.Fatalf("the end is said: %+v", u)
	}
}

func TestEndBeforeAnyFrame(t *testing.T) {
	s := termview.NewScreen("v2")
	s.End()
	if u, ok := s.Take(); !ok || !u.Ended || u.Lines == nil {
		t.Fatalf("an end without a screen: %+v", u)
	}
	if _, ok := s.Take(); ok {
		t.Fatal("said once")
	}
}

func TestEncodeLineRuns(t *testing.T) {
	line := cells("ab  ")
	line[1].Style.Attrs = 1
	l := termview.EncodeLine(4, line)
	if l.Y != 4 || len(l.Runs) != 2 || l.Runs[0].Text != "a" || l.Runs[1].Attrs != 1 || l.Runs[1].Width != 1 {
		t.Fatalf("runs split by style, trailing blanks dropped: %+v", l)
	}
	if got := termview.ColorString(vt.Color(2<<24 | 0x12ab34)); got != "#12ab34" {
		t.Fatalf("colour: %q", got)
	}
}
