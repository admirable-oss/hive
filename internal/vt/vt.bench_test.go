package vt_test

import (
	"math/rand/v2"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

// BenchmarkTerminalWrite is the emulator's throughput on typical agent
// output (text, colours, cursor moves, scrolling).
func BenchmarkTerminalWrite(b *testing.B) {
	out := randomOutput(rand.New(rand.NewPCG(1, 2)), 4000)
	term := vt.New(200, 50, vt.Options{})
	defer term.Close()
	b.SetBytes(int64(len(out)))
	b.ResetTimer()
	for b.Loop() {
		_, _ = term.Write(out)
	}
}

// BenchmarkFrameRoundTrip encodes and decodes a full-screen keyframe.
func BenchmarkFrameRoundTrip(b *testing.B) {
	term := vt.New(200, 50, vt.Options{})
	defer term.Close()
	_, _ = term.Write(randomOutput(rand.New(rand.NewPCG(3, 4)), 4000))
	var view vt.View
	f := term.Frame(&view)
	var buf []byte
	b.ResetTimer()
	for b.Loop() {
		buf = vt.AppendFrameWith(buf[:0], f, true)
		if _, err := vt.DecodeFrame(buf[4:]); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(len(buf)))
}

// BenchmarkCoalesce merges a burst of small frames for a slow reader.
func BenchmarkCoalesce(b *testing.B) {
	term := vt.New(200, 50, vt.Options{})
	defer term.Close()
	r := rand.New(rand.NewPCG(5, 6))
	var view vt.View
	frames := []*vt.Frame{term.Frame(&view)}
	for range 50 {
		_, _ = term.Write(randomOutput(r, 20))
		if f := term.Frame(&view); f != nil {
			frames = append(frames, f)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		var c vt.Coalescer
		for _, f := range frames {
			c.Add(f)
		}
		_ = c.Take()
	}
}

// BenchmarkTerminalWriteLines is the emulator's throughput on plain
// scrolling lines, the bulk of what agents print.
func BenchmarkTerminalWriteLines(b *testing.B) {
	var out []byte
	for i := range 500 {
		out = append(out, []byte("\x1b[32m✔\x1b[0m wrote internal/module/file_"+string(rune('a'+i%26))+".go (42 lines, 3 changes)\r\n")...)
	}
	term := vt.New(200, 50, vt.Options{})
	defer term.Close()
	b.SetBytes(int64(len(out)))
	b.ResetTimer()
	for b.Loop() {
		_, _ = term.Write(out)
	}
}
