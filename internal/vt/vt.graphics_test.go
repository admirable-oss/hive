package vt_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

type graphicsFixture struct {
	name     string
	outcomes []string
	visible  string
	seq      []byte
}

var repeat = regexp.MustCompile(`\{(.)\*(\d+)\}`)

func loadGraphicsFixtures(t *testing.T) []graphicsFixture {
	t.Helper()
	f, err := os.Open("testdata/kitty-graphics.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []graphicsFixture
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " | ", 4)
		if len(parts) != 4 {
			t.Fatalf("bad fixture line %q", line)
		}
		seq, err := strconv.Unquote(parts[3])
		if err != nil {
			t.Fatalf("%s: %v", parts[0], err)
		}
		seq = repeat.ReplaceAllStringFunc(seq, func(m string) string {
			sub := repeat.FindStringSubmatch(m)
			n, _ := strconv.Atoi(sub[2])
			return strings.Repeat(sub[1], n)
		})
		outcomes := strings.Fields(parts[1])
		if parts[1] == "-" {
			outcomes = nil
		}
		out = append(out, graphicsFixture{name: parts[0], outcomes: outcomes, visible: parts[2], seq: []byte(seq)})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// classify runs observed commands through the relay check and the upload
// assembler, as the mux does for terminals without Kitty.
func classify(commands []vt.KittyGraphicsCommand) []string {
	var asm vt.KittyGraphicsAssembler
	active := false
	var out []string
	for _, c := range commands {
		if c.ValidateKittyGraphicsRelay() != nil {
			out = append(out, "reject")
			asm.Reset()
			active = false
			continue
		}
		if !active && c.Action != "t" && c.Action != "T" {
			out = append(out, "command")
			continue
		}
		upload, err := asm.Push(c)
		switch {
		case err != nil:
			out = append(out, "invalid")
			active = false
		case upload == nil:
			out = append(out, "partial")
			active = true
		default:
			out = append(out, "upload")
			active = false
		}
	}
	return out
}

// feed writes chunks to a fresh terminal and returns what its graphics
// callback observed and its first line.
func feed(chunks ...[]byte) ([]vt.KittyGraphicsCommand, string) {
	var got []vt.KittyGraphicsCommand
	term := vt.New(40, 2, vt.Options{KittyGraphics: func(c vt.KittyGraphicsCommand) {
		c.Raw = append([]byte(nil), c.Raw...)
		got = append(got, c)
	}})
	defer term.Close()
	for _, c := range chunks {
		_, _ = term.Write(c)
	}
	return got, strings.TrimRight(term.Snapshot().LineText(0), " ")
}

// TestKittyGraphicsFixtures: every fixture gives the same outcome however
// the PTY splits its bytes, and graphics never reach the screen as text.
func TestKittyGraphicsFixtures(t *testing.T) {
	fixtures := loadGraphicsFixtures(t)
	if len(fixtures) < 30 {
		t.Fatalf("only %d fixtures loaded", len(fixtures))
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			check := func(how string, chunks ...[]byte) {
				t.Helper()
				cmds, visible := feed(chunks...)
				if got := classify(cmds); strings.Join(got, " ") != strings.Join(fx.outcomes, " ") {
					t.Fatalf("%s: outcomes %v, want %v", how, got, fx.outcomes)
				}
				if visible != fx.visible {
					t.Fatalf("%s: screen shows %q, want %q", how, visible, fx.visible)
				}
				for _, c := range cmds {
					if !bytes.Contains(fx.seq, c.Raw) {
						t.Fatalf("%s: observed bytes %q that were not written", how, c.Raw)
					}
				}
			}
			check("whole", fx.seq)
			step := 1
			if len(fx.seq) > 512 {
				step = 97 // long payloads: a sample of split points
			}
			for i := 1; i < len(fx.seq); i += step {
				check("split at "+strconv.Itoa(i), fx.seq[:i], fx.seq[i:])
			}
			if len(fx.seq) <= 512 {
				var bytewise [][]byte
				for i := range fx.seq {
					bytewise = append(bytewise, fx.seq[i:i+1])
				}
				check("byte by byte", bytewise...)
			}
		})
	}
}

func TestParseKittyGraphicsCommand_RejectsTrailingBytes(t *testing.T) {
	ok := []byte("\x1b_Ga=p,i=1;\x1b\\")
	if _, err := vt.ParseKittyGraphicsCommand(ok, vt.Cursor{}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), ok...), 'x'), ok[:len(ok)-1], []byte("plain")} {
		if _, err := vt.ParseKittyGraphicsCommand(bad, vt.Cursor{}); err == nil {
			t.Errorf("%q parsed as one command", bad)
		}
	}
}

func TestGraphicsEventStream_RoundTripAndBounds(t *testing.T) {
	events := []vt.GraphicsEvent{
		{Command: []byte("\x1b_Ga=p,i=1;\x1b\\"), Cursor: vt.Cursor{X: 3, Y: 7}},
		{Command: bytes.Repeat([]byte{'A'}, 8200), Cursor: vt.Cursor{X: 70000, Y: -1}},
	}
	var buf []byte
	for _, e := range events {
		buf = vt.AppendGraphicsEvent(buf, e)
	}
	r := vt.NewGraphicsReader(bytes.NewReader(buf))
	for i, want := range events {
		got, err := r.Next()
		if err != nil || !bytes.Equal(got.Command, want.Command) {
			t.Fatalf("event %d: %v", i, err)
		}
		if i == 1 && (got.Cursor.X != 65535 || got.Cursor.Y != 0) {
			t.Fatalf("cursor %+v not clamped", got.Cursor)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end: %v", err)
	}
	// A cut-off stream is an error, not a short event.
	if _, err := vt.NewGraphicsReader(bytes.NewReader(buf[:len(buf)-10])).Next(); err != nil {
		t.Fatalf("first event of a cut stream: %v", err)
	}
	r = vt.NewGraphicsReader(bytes.NewReader(buf[:len(buf)-10]))
	_, _ = r.Next()
	if _, err := r.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("cut event: %v", err)
	}
	// Lengths past the bound or zero are refused before allocating.
	for _, n := range []uint32{0, vt.MaxGraphicsEventBytes + 1, 1 << 31} {
		hdr := []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n), 0, 0, 0, 0}
		if _, err := vt.NewGraphicsReader(bytes.NewReader(hdr)).Next(); err == nil {
			t.Errorf("length %d accepted", n)
		}
	}
}

func TestGraphicsStripper(t *testing.T) {
	png := "\x1b_Ga=T,f=100;iVBORw0KGgo=\x1b\\"
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"a" + png + "b", "ab"},
		{png + png + "x", "x"},
		{"\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"\x1b_Xother\x1b\\", "\x1b_Xother\x1b\\"},
		{"\x1b\x1b_Ga=t;AAAA\x1b\\z", "\x1bz"},
		{"\x1b_Ga=t;AA\x1b\x1b\\after", "after"},
		{"\x1b_Ga=t;AAAA\x18after", "after"},
		{"\x1b]0;title\x07ok", "\x1b]0;title\x07ok"},
		{"\x1b_Ga=p;\x1b[31mvisible\x1b\\", "\x1b[31mvisible\x1b\\"},
		// An APC string this aborts passes through, closed, so what is left
		// never reads as a graphics command again.
		{"\x1b_\x1b_G\x1b\\G", "\x1b_\x1b\\G"},
	}
	for _, tc := range cases {
		for split := 0; split <= len(tc.in); split++ {
			var out bytes.Buffer
			s := vt.NewGraphicsStripper(&out)
			if n, err := s.Write([]byte(tc.in[:split])); err != nil || n != split {
				t.Fatalf("write: %d %v", n, err)
			}
			_, _ = s.Write([]byte(tc.in[split:]))
			_ = s.Close()
			if out.String() != tc.want {
				t.Fatalf("%q split at %d: got %q, want %q", tc.in, split, out.String(), tc.want)
			}
		}
	}
	// A stream that ends on a held ESC or ESC _ keeps it.
	for _, tail := range []string{"\x1b", "\x1b_"} {
		var out bytes.Buffer
		s := vt.NewGraphicsStripper(&out)
		_, _ = s.Write([]byte("x" + tail))
		_ = s.Close()
		if out.String() != "x"+tail {
			t.Fatalf("tail %q: got %q", tail, out.String())
		}
	}
}

// FuzzKittyGraphicsParser: any output, split anywhere, never panics, and
// whatever the relay accepts fits in one graphics event.
func FuzzKittyGraphicsParser(f *testing.F) {
	f.Add([]byte("\x1b_Ga=T,f=100,c=1,r=1;AAAA\x1b\\"), uint16(3))
	f.Add([]byte("\x1b_Ga=t,m=1;AAAA\x1b\\\x1b_Gm=0;AAAA\x1b\\"), uint16(20))
	f.Add([]byte("\x1b_G\x1b\x1b\\\x18\x1b]0;\x1b_G\x07"), uint16(1))
	f.Fuzz(func(t *testing.T, data []byte, at uint16) {
		split := int(at) % (len(data) + 1)
		cmds, _ := feed(data[:split], data[split:])
		var asm vt.KittyGraphicsAssembler
		for _, c := range cmds {
			if c.ValidateKittyGraphicsRelay() == nil {
				if len(c.Raw) > vt.MaxGraphicsEventBytes {
					t.Fatalf("relayable command of %d bytes", len(c.Raw))
				}
				ev := vt.AppendGraphicsEvent(nil, vt.GraphicsEvent{Command: c.Raw})
				if _, err := vt.NewGraphicsReader(bytes.NewReader(ev)).Next(); err != nil {
					t.Fatalf("relayable command does not round-trip: %v", err)
				}
			}
			_, _ = asm.Push(c)
		}
		var out bytes.Buffer
		s := vt.NewGraphicsStripper(&out)
		_, _ = s.Write(data[:split])
		_, _ = s.Write(data[split:])
		if bytes.Contains(out.Bytes(), []byte("\x1b_G")) {
			t.Fatalf("stripped output still has a graphics command: %q", out.Bytes())
		}
	})
}
