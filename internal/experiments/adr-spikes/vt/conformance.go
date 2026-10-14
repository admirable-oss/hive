package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

type vtCase struct {
	name, input string
	want        []string // expected first rows; the rest must be blank
}

var initial = "1\r\n2\r\n3\r\n4\r\n5"

var cases = []vtCase{
	{"text and CRLF", "hello\r\nworld", []string{"hello", "world"}},
	{"autowrap", strings.Repeat("a", 80) + "b", []string{strings.Repeat("a", 80), "b"}},
	{"pending wrap then CRLF", strings.Repeat("x", 80) + "\r\ny", []string{strings.Repeat("x", 80), "y"}},
	{"CUP", "\x1b[3;5Hab", []string{"", "", "    ab"}},
	{"EL to end", "abcdef\x1b[1;3H\x1b[K", []string{"ab"}},
	{"EL to start", "abcdef\x1b[1;3H\x1b[1K", []string{"   def"}},
	{"ED 2", "abc\r\ndef\x1b[2J", nil},
	{"ED 0 from middle", "abc\r\ndef\r\nghi\x1b[2;2H\x1b[J", []string{"abc", "d"}},
	{"DECSTBM scroll up", initial + "\x1b[2;4r\x1b[4;1H\nX", []string{"1", "3", "4", "X", "5"}},
	{"RI scrolls region down", initial + "\x1b[2;4r\x1b[2;1H\x1bMY", []string{"1", "Y", "2", "3", "5"}},
	{"IL", initial + "\x1b[2;1H\x1b[L", []string{"1", "", "2", "3", "4", "5"}},
	{"DL", initial + "\x1b[2;1H\x1b[M", []string{"1", "3", "4", "5"}},
	{"SU/SD", initial + "\x1b[2S", []string{"3", "4", "5"}},
	{"DECSC/DECRC", "ab\x1b7\x1b[5;5Hxy\x1b8cd", []string{"abcd", "", "", "", "    xy"}},
	{"alt screen 1049 restores", "main\x1b[?1049h\x1b[2J\x1b[Halt\x1b[?1049l", []string{"main"}},
	{"alt screen shows alt", "main\x1b[?1049h\x1b[2J\x1b[Halt", []string{"alt"}},
	{"wide chars", "日本語x", []string{"日本語x"}},
	{"wide char wraps at margin", strings.Repeat("a", 79) + "日", []string{strings.Repeat("a", 79), "日"}},
	{"emoji", "🐝x", []string{"🐝x"}},
	{"combining mark", "éx", []string{"éx"}},
	{"tab stops", "a\tb", []string{"a       b"}},
	{"DEC line drawing", "\x1b(0lqk\x1b(B", []string{"┌─┐"}},
	{"origin mode", "\x1b[3;5r\x1b[?6h\x1b[1;1HZ\x1b[?6l\x1b[r", []string{"", "", "Z"}},
	{"DCH", "abcdef\x1b[1;2H\x1b[2P", []string{"adef"}},
	{"ICH", "abc\x1b[1;2H\x1b[2@", []string{"a  bc"}},
	{"ECH", "abcdef\x1b[1;2H\x1b[2X", []string{"a  def"}},
	{"Ink-style redraw (Claude Code)", "line1\r\nline2\r\nline3\x1b[2K\x1b[1A\x1b[2K\x1b[1A\x1b[2K\x1b[Gnew1\r\nnew2", []string{"new1", "new2"}},
	{"synchronized output 2026", "\x1b[?2026hok\x1b[?2026l", []string{"ok"}},
	{"OSC 8 hyperlink", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", []string{"link"}},
	{"OSC title is not printed", "\x1b]0;my title\x07ok", []string{"ok"}},
	{"truecolor SGR is not printed", "\x1b[38;2;255;0;0mred\x1b[0m", []string{"red"}},
	{"CHA/HPA/VPA", "\x1b[5Ga\x1b[3db\x1b[1`c", []string{"", "", "c   ab"[0:0] + "c", ""}},
	{"backspace overwrite", "abc\b\bX", []string{"aXc"}},
	{"CR overwrite (progress bar)", "50%\r75%\r100%", []string{"100%"}},
	{"split UTF-8 across writes", "\xe6\x97", nil}, // completed by the runner
}

type caseResult struct {
	name   string
	pass   map[string]bool
	detail map[string]string
}

func runConformance(emus []func() screener) []caseResult {
	var results []caseResult
	for _, c := range cases {
		r := caseResult{name: c.name, pass: map[string]bool{}, detail: map[string]string{}}
		for _, mk := range emus {
			e := mk()
			want := c.want
			if c.name == "split UTF-8 across writes" {
				_, _ = e.Write([]byte("\xe6\x97"))
				_, _ = e.Write([]byte("\xa5x"))
				want = []string{"日x"}
			} else {
				_, _ = e.Write([]byte(c.input))
			}
			if c.name == "CHA/HPA/VPA" {
				// CHA 5 → col 4 'a' (row 0); VPA 3 → row 2 col 5 'b'; HPA 1 → col 0 'c' on row 2
				want = []string{"    a", "", "c    b"}
			}
			got := e.Rows()
			ok := true
			for i, row := range got {
				exp := ""
				if i < len(want) {
					exp = want[i]
				}
				if row != exp {
					ok = false
					r.detail[e.Name()] = fmt.Sprintf("row %d: got %q want %q", i, row, exp)
					break
				}
			}
			r.pass[e.Name()] = ok
		}
		results = append(results, r)
	}
	return results
}

// dsrReply checks that a cursor-position query is answered, which apps like
// vim and Ink-based agents rely on.
func dsrReply() (string, error) {
	e := newXVT(80, 24)
	buf := make([]byte, 64)
	done := make(chan int, 1)
	// Replies go through a synchronous pipe: someone must be reading before
	// the query is written, or Write blocks forever.
	go func() {
		n, _ := e.e.Read(buf)
		done <- n
	}()
	_, _ = e.e.Write([]byte("\x1b[3;7H\x1b[6n"))
	select {
	case n := <-done:
		return string(buf[:n]), nil
	case <-time.After(time.Second):
		_ = e.e.Close()
		return "", io.ErrNoProgress
	}
}
