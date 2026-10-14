package main

import "testing"

func BenchmarkXVT(b *testing.B) {
	data := agentStream(4 << 20)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		e := newXVTsized(220, 50)
		feedChunks(e, data)
	}
}

func BenchmarkXVTNoScrollback(b *testing.B) {
	data := agentStream(4 << 20)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		e := newXVTsized(220, 50)
		e.e.SetScrollbackSize(0)
		feedChunks(e, data)
	}
}

func BenchmarkXVTSmallScrollback(b *testing.B) {
	data := agentStream(4 << 20)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		e := newXVTsized(220, 50)
		e.e.SetScrollbackSize(500)
		feedChunks(e, data)
	}
}

func TestEmptyMemory(t *testing.T) {
	for _, sb := range []int{-1, 0, 500} {
		var keep []*xvt
		n := heapAfter(func() {
			for range 20 {
				e := newXVTsized(220, 50)
				if sb >= 0 {
					e.e.SetScrollbackSize(sb)
				}
				keep = append(keep, e)
			}
		})
		t.Logf("scrollback=%d: %.1f KiB per empty emulator", sb, float64(n)/20/1024)
		_ = keep
	}
}
