package vt_test

import (
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

func TestTitlesWithAnyUTF8(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b]0;✳ Claude Code\x07":       "✳ Claude Code", // e2 9c b3: 0x9c is not ST in UTF-8
		"\x1b]2;◐ working\x1b\\":         "◐ working",
		"\x1b]0;a\x01b\x07":              "ab", // control characters dropped
		"\x1b]2;bad \xff byte\x07":       "bad � byte",
		"\x1b]1;icon only\x07":           "",
		"x\x1b]0;first\x07y\x1b]2;two\a": "two",
	} {
		term := vt.New(40, 3, vt.Options{})
		_, _ = term.Write([]byte(in))
		if got := term.Snapshot().Title; got != want {
			t.Errorf("%q: title %q, want %q", in, got, want)
		}
		term.Close()
	}
}

func TestTitleSplitAcrossWrites(t *testing.T) {
	seq := "ab\x1b]0;✳ split title\x07cd"
	for cut := range len(seq) + 1 {
		term := vt.New(40, 3, vt.Options{})
		_, _ = term.Write([]byte(seq[:cut]))
		_, _ = term.Write([]byte(seq[cut:]))
		s := term.Snapshot()
		if s.Title != "✳ split title" || s.LineText(0) != "abcd" {
			t.Fatalf("cut at %d: title %q, line %q", cut, s.Title, s.LineText(0))
		}
		term.Close()
	}
}

func TestOtherSequencesPassThrough(t *testing.T) {
	term := vt.New(60, 3, vt.Options{})
	defer term.Close()
	// A hyperlink (OSC 8), colours (SGR), an ESC-ESC pair and a
	// cancelled OSC must all still reach the parser.
	_, _ = term.Write([]byte("\x1b]8;;https://x.dev\x1b\\link\x1b]8;;\x07 \x1b[1mbold\x1b[0m\x1b\x1b[2m!\x1b]0;gone\x18ok"))
	s := term.Snapshot()
	if s.Lines[0][0].Link != "https://x.dev" {
		t.Errorf("the link lost: %+v", s.Lines[0][0])
	}
	if got := s.LineText(0); !strings.HasPrefix(got, "link bold!") || !strings.HasSuffix(got, "ok") {
		t.Errorf("line %q", got)
	}
	if s.Lines[0][5].Style.Attrs&vt.AttrBold == 0 {
		t.Error("SGR after the link still applies")
	}
	if s.Title != "" {
		t.Errorf("a cancelled title is not set: %q", s.Title)
	}
}

func TestLongOSCPassesThrough(t *testing.T) {
	term := vt.New(40, 3, vt.Options{})
	defer term.Close()
	_, _ = term.Write([]byte("\x1b]0;" + strings.Repeat("x", 5000) + "\x07after"))
	if got := term.Snapshot().LineText(0); got != "after" {
		t.Fatalf("output after an over-long title: %q", got)
	}
}

// FuzzTitleFilter checks that splitting output anywhere never changes the
// title, nor, for ASCII output, the screen. (Non-ASCII screens are not
// compared: the emulator does not join a combining mark that arrives in a
// later write to the character before it, a known limitation separate
// from titles.)
func FuzzTitleFilter(f *testing.F) {
	for _, s := range []string{"\x1b]0;✳ t\x07x", "\x1b]8;;u\x1b\\l\x1b]8;;\x07", "\x1b\x1b]2;a\x1b[1m", "\x1b]0;cut\x18y", "plain"} {
		f.Add([]byte(s), uint16(3))
	}
	f.Fuzz(func(t *testing.T, data []byte, cut uint16) {
		at := int(cut) % (len(data) + 1)
		whole, split := vt.New(30, 4, vt.Options{}), vt.New(30, 4, vt.Options{})
		defer whole.Close()
		defer split.Close()
		_, _ = whole.Write(data)
		_, _ = split.Write(data[:at])
		_, _ = split.Write(data[at:])
		a, b := whole.Snapshot(), split.Snapshot()
		if a.Title != b.Title || (isASCII(data) && a.Text() != b.Text()) {
			t.Fatalf("split at %d differs: %q/%q vs %q/%q", at, a.Title, a.Text(), b.Title, b.Text())
		}
	})
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}
