package terminal

// history is a fixed-capacity ring of the most recent output bytes. Writes
// never allocate; the old slice-trimming approach copied the whole buffer on
// every chunk once it was full.
type history struct {
	buf   []byte
	start int // index of the oldest byte
	n     int // bytes stored
}

func newHistory(capacity int) *history {
	return &history{buf: make([]byte, max(capacity, 1))}
}

func (h *history) Write(p []byte) {
	c := len(h.buf)
	if len(p) >= c {
		copy(h.buf, p[len(p)-c:])
		h.start, h.n = 0, c
		return
	}
	end := (h.start + h.n) % c
	k := copy(h.buf[end:], p)
	copy(h.buf, p[k:])
	if h.n += len(p); h.n > c {
		h.start = (h.start + h.n - c) % c
		h.n = c
	}
}

// Bytes returns a copy of the stored bytes, oldest first.
func (h *history) Bytes() []byte {
	out := make([]byte, h.n)
	k := copy(out, h.buf[h.start:min(h.start+h.n, len(h.buf))])
	copy(out[k:], h.buf[:h.n-k])
	return out
}
