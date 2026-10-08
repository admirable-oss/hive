package terminal

import (
	"bytes"
	"testing"
)

func TestHistoryKeepsTheMostRecentBytes(t *testing.T) {
	tests := []struct {
		name   string
		cap    int
		writes []string
		want   string
	}{
		{"empty", 4, nil, ""},
		{"under capacity", 8, []string{"ab", "cd"}, "abcd"},
		{"exactly full", 4, []string{"ab", "cd"}, "abcd"},
		{"wraps", 4, []string{"abc", "de"}, "bcde"},
		{"wraps many times", 3, []string{"a", "b", "c", "d", "e", "f", "g"}, "efg"},
		{"oversized write", 3, []string{"ab", "cdefgh"}, "fgh"},
		{"oversized then small", 3, []string{"abcdef", "x"}, "efx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHistory(tt.cap)
			for _, w := range tt.writes {
				h.Write([]byte(w))
			}
			if got := string(h.Bytes()); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHistoryMatchesNaiveModel(t *testing.T) {
	const capacity = 7
	h := newHistory(capacity)
	var model []byte
	for i := range 500 {
		chunk := bytes.Repeat([]byte{byte('a' + i%26)}, i%11)
		h.Write(chunk)
		model = append(model, chunk...)
		if len(model) > capacity {
			model = model[len(model)-capacity:]
		}
		if got := h.Bytes(); !bytes.Equal(got, model) {
			t.Fatalf("step %d: got %q, want %q", i, got, model)
		}
	}
}

func BenchmarkHistoryWrite(b *testing.B) {
	h := newHistory(64 << 10)
	chunk := bytes.Repeat([]byte("x"), 4096)
	b.SetBytes(int64(len(chunk)))
	for b.Loop() {
		h.Write(chunk)
	}
}
