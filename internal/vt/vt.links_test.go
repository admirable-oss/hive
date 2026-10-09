package vt_test

import (
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

const linked = "see \x1b]8;id=1;https://example.com/docs\x07the docs\x1b]8;;\x07 now"

func linkAt(s *vt.Screen, y, x int) string { return s.Lines[y][x].Link }

func TestHyperlinksReachTheCells(t *testing.T) {
	term := newTerm(t, 30, 2, vt.Options{})
	_, _ = term.Write([]byte(linked))
	s := term.Snapshot()
	if got := linkAt(s, 0, 4); got != "https://example.com/docs" {
		t.Fatalf("link on the linked text = %q", got)
	}
	if linkAt(s, 0, 3) != "" || linkAt(s, 0, 12) != "" {
		t.Fatal("cells around the link have none")
	}
}

func TestFramesCarryLinksOnlyWhenAsked(t *testing.T) {
	term := newTerm(t, 30, 2, vt.Options{})
	_, _ = term.Write([]byte(linked))
	var v vt.View
	f := term.Frame(&v)

	with, err := vt.DecodeFrame(vt.AppendFrameWith(nil, f, true)[4:])
	if err != nil {
		t.Fatal(err)
	}
	s := &vt.Screen{}
	s.Apply(with)
	if linkAt(s, 0, 4) != "https://example.com/docs" || linkAt(s, 0, 3) != "" {
		t.Fatalf("frames/2 lost the link: %+v", s.Lines[0][:6])
	}

	without, err := vt.DecodeFrame(vt.AppendFrame(nil, f)[4:])
	if err != nil {
		t.Fatal(err)
	}
	s = &vt.Screen{}
	s.Apply(without)
	if linkAt(s, 0, 4) != "" || s.LineText(0) != "see the docs now" {
		t.Fatalf("frames/1 has no links but the same text: %q", s.LineText(0))
	}
}

func TestUnsafeLinksAreDropped(t *testing.T) {
	for _, url := range []string{"http://x/\x1b]52;c;evil", "http://x/\x07", "http://" + strings.Repeat("a", vt.MaxLinkLen)} {
		if vt.CleanLink(url) != "" {
			t.Errorf("CleanLink(%q) kept it", url)
		}
	}
	if vt.CleanLink("file:///tmp/a b") != "file:///tmp/a b" {
		t.Error("a plain link is kept")
	}
	// A frame claiming a longer link is corrupt.
	f := &vt.Frame{Cols: 5, Rows: 1, Lines: []vt.LineUpdate{{Y: 0, Cells: []vt.Cell{{Content: "x", Width: 1, Link: "ok"}}}}}
	enc := string(vt.AppendFrameWith(nil, f, true)[4:])
	enc = strings.Replace(enc, "\x02ok", "\xff\x7f"+strings.Repeat("a", 16383), 1)
	if _, err := vt.DecodeFrame([]byte(enc)); err == nil {
		t.Fatal("an over-long link must be rejected")
	}
}

func TestPainterAndLineANSIWriteLinks(t *testing.T) {
	term := newTerm(t, 30, 1, vt.Options{})
	_, _ = term.Write([]byte(linked))
	s := term.Snapshot()
	line := vt.LineANSI(s.Lines[0])
	if !strings.Contains(line, "\x1b]8;;https://example.com/docs\x07the docs\x1b]8;;\x07") {
		t.Fatalf("LineANSI = %q", line)
	}
	var out strings.Builder
	var v vt.View
	if err := vt.NewPainter(&out).Paint(term.Frame(&v)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b]8;;https://example.com/docs\x07") || !strings.Contains(out.String(), "\x1b]8;;\x07") {
		t.Fatalf("the painter does not open and close the link: %q", out.String())
	}
}

func TestScrollbackKeepsLinks(t *testing.T) {
	term := newTerm(t, 30, 1, vt.Options{ScrollbackBytes: 1 << 16})
	_, _ = term.Write([]byte(linked + "\r\nnext"))
	lines, _ := term.Scrollback().Since(0)
	if len(lines) != 1 || lines[0][4].Link != "https://example.com/docs" {
		t.Fatalf("scrollback = %+v", lines)
	}
}
