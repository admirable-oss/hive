package vt_test

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

func TestFrameReaderEndsCleanly(t *testing.T) {
	term := newTerm(t, 10, 2, vt.Options{})
	var v vt.View
	data := vt.AppendFrame(nil, term.Frame(&v))
	fr := vt.NewFrameReader(bytes.NewReader(data))
	if _, err := fr.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := fr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
	fr = vt.NewFrameReader(bytes.NewReader(data[:len(data)-1]))
	if _, err := fr.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame: got %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := vt.NewFrameReader(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff})).Next(); err == nil {
		t.Fatal("an absurd frame length must be rejected")
	}
}

func FuzzDecodeFrame(f *testing.F) {
	r := rand.New(rand.NewPCG(1, 2))
	term := vt.New(30, 6, vt.Options{})
	var v vt.View
	for range 5 {
		_, _ = term.Write(randomOutput(r, 10))
		if fr := term.Frame(&v); fr != nil {
			f.Add(vt.AppendFrame(nil, fr)[4:])
		}
	}
	_, _ = term.Write([]byte("\x1b]8;;https://example.com\x07linked\x1b]8;;\x07 text"))
	if fr := term.Frame(&v); fr != nil {
		f.Add(vt.AppendFrameWith(nil, fr, true)[4:])
	}
	_ = term.Close()
	f.Add([]byte{})
	f.Add([]byte{1, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, err := vt.DecodeFrame(data)
		if err != nil {
			return
		}
		// Anything accepted must re-encode (with links) to a frame that
		// decodes to the same cells.
		again, err := vt.DecodeFrame(vt.AppendFrameWith(nil, fr, true)[4:])
		if err != nil {
			t.Fatalf("re-encoded frame does not decode: %v", err)
		}
		s1, s2 := &vt.Screen{}, &vt.Screen{}
		s1.Apply(fr)
		s2.Apply(again)
		if !reflect.DeepEqual(s1.Lines, s2.Lines) {
			t.Fatal("round trip changed the frame")
		}
	})
}
