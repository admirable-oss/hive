package vt_test

import (
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

// newTerm returns a terminal closed at the end of the test.
func newTerm(t *testing.T, cols, rows int, opts vt.Options) *vt.Terminal {
	t.Helper()
	term := vt.New(cols, rows, opts)
	t.Cleanup(func() { _ = term.Close() })
	return term
}

const initial = "1\r\n2\r\n3\r\n4\r\n5"

// conformance is the corpus from docs/adr/spikes/vt: each input must leave
// the screen showing want (the remaining lines blank).
var conformance = []struct {
	name, input string
	want        []string
	known       string // a known emulator bug; the case is skipped with this reason
}{
	{name: "text and CRLF", input: "hello\r\nworld", want: []string{"hello", "world"}},
	{name: "autowrap", input: strings.Repeat("a", 80) + "b", want: []string{strings.Repeat("a", 80), "b"}},
	{name: "pending wrap then CRLF", input: strings.Repeat("x", 80) + "\r\ny", want: []string{strings.Repeat("x", 80), "y"}},
	{name: "CUP", input: "\x1b[3;5Hab", want: []string{"", "", "    ab"}},
	{name: "EL to end", input: "abcdef\x1b[1;3H\x1b[K", want: []string{"ab"}},
	{name: "EL to start", input: "abcdef\x1b[1;3H\x1b[1K", want: []string{"   def"}},
	{name: "ED 2", input: "abc\r\ndef\x1b[2J"},
	{name: "ED 0 from middle", input: "abc\r\ndef\r\nghi\x1b[2;2H\x1b[J", want: []string{"abc", "d"}},
	{name: "DECSTBM scroll up", input: initial + "\x1b[2;4r\x1b[4;1H\nX", want: []string{"1", "3", "4", "X", "5"}},
	{name: "RI scrolls region down", input: initial + "\x1b[2;4r\x1b[2;1H\x1bMY", want: []string{"1", "Y", "2", "3", "5"}},
	{name: "IL", input: initial + "\x1b[2;1H\x1b[L", want: []string{"1", "", "2", "3", "4", "5"}},
	{name: "DL", input: initial + "\x1b[2;1H\x1b[M", want: []string{"1", "3", "4", "5"}},
	{name: "SU", input: initial + "\x1b[2S", want: []string{"3", "4", "5"}},
	{name: "DECSC/DECRC", input: "ab\x1b7\x1b[5;5Hxy\x1b8cd", want: []string{"abcd", "", "", "", "    xy"}},
	{name: "alt screen 1049 restores", input: "main\x1b[?1049h\x1b[2J\x1b[Halt\x1b[?1049l", want: []string{"main"}},
	{name: "alt screen shows alt", input: "main\x1b[?1049h\x1b[2J\x1b[Halt", want: []string{"alt"}},
	{name: "wide chars", input: "日本語x", want: []string{"日本語x"}},
	{name: "wide char wraps at margin", input: strings.Repeat("a", 79) + "日", want: []string{strings.Repeat("a", 79), "日"}, known: "x/vt drops a wide character that does not fit at the right margin (ADR 0001)"},
	{name: "emoji", input: "🐝x", want: []string{"🐝x"}},
	{name: "combining mark", input: "éx", want: []string{"éx"}},
	{name: "tab stops", input: "a\tb", want: []string{"a       b"}},
	{name: "DEC line drawing", input: "\x1b(0lqk\x1b(B", want: []string{"┌─┐"}},
	{name: "origin mode", input: "\x1b[3;5r\x1b[?6h\x1b[1;1HZ\x1b[?6l\x1b[r", want: []string{"", "", "Z"}},
	{name: "DCH", input: "abcdef\x1b[1;2H\x1b[2P", want: []string{"adef"}},
	{name: "ICH", input: "abc\x1b[1;2H\x1b[2@", want: []string{"a  bc"}},
	{name: "ECH", input: "abcdef\x1b[1;2H\x1b[2X", want: []string{"a  def"}},
	{name: "Ink-style redraw (Claude Code)", input: "line1\r\nline2\r\nline3\x1b[2K\x1b[1A\x1b[2K\x1b[1A\x1b[2K\x1b[Gnew1\r\nnew2", want: []string{"new1", "new2"}},
	{name: "synchronized output 2026", input: "\x1b[?2026hok\x1b[?2026l", want: []string{"ok"}},
	{name: "OSC 8 hyperlink", input: "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", want: []string{"link"}},
	{name: "OSC title is not printed", input: "\x1b]0;my title\x07ok", want: []string{"ok"}},
	{name: "truecolor SGR is not printed", input: "\x1b[38;2;255;0;0mred\x1b[0m", want: []string{"red"}},
	{name: "CHA/VPA/HPA", input: "\x1b[5Ga\x1b[3db\x1b[1`c", want: []string{"    a", "", "c    b"}},
	{name: "backspace overwrite", input: "abc\b\bX", want: []string{"aXc"}},
	{name: "CR overwrite (progress bar)", input: "50%\r75%\r100%", want: []string{"100%"}},
}

func TestConformance(t *testing.T) {
	for _, tc := range conformance {
		t.Run(tc.name, func(t *testing.T) {
			if tc.known != "" {
				t.Skip(tc.known)
			}
			term := newTerm(t, 80, 10, vt.Options{})
			if _, err := term.Write([]byte(tc.input)); err != nil {
				t.Fatal(err)
			}
			assertLines(t, term.Snapshot(), tc.want)
		})
	}
}

// TestConformanceSplitWrites feeds every case one byte at a time, as an
// unlucky PTY read pattern would; the screen must not change.
func TestConformanceSplitWrites(t *testing.T) {
	for _, tc := range conformance {
		if tc.known != "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			term := newTerm(t, 80, 10, vt.Options{})
			for i := range len(tc.input) {
				_, _ = term.Write([]byte{tc.input[i]})
			}
			if tc.name == "combining mark" {
				// x/vt flushes a grapheme cluster at the end of each write, so
				// a combining mark in a separate write lands in its own cell.
				t.Skip("grapheme clusters split across writes are not joined (x/vt)")
			}
			assertLines(t, term.Snapshot(), tc.want)
		})
	}
}

func TestSplitUTF8(t *testing.T) {
	term := newTerm(t, 20, 2, vt.Options{})
	_, _ = term.Write([]byte("\xe6\x97"))
	_, _ = term.Write([]byte("\xa5x"))
	assertLines(t, term.Snapshot(), []string{"日x"})
}

func assertLines(t *testing.T, s *vt.Screen, want []string) {
	t.Helper()
	for y := range s.Rows {
		exp := ""
		if y < len(want) {
			exp = want[y]
		}
		if got := s.LineText(y); got != exp {
			t.Fatalf("line %d = %q, want %q\nscreen:\n%s", y, got, exp, s.Text())
		}
	}
}
