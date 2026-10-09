package vt

// Scrollback keeps the lines that scrolled off a terminal, newest last,
// within a byte budget: lines are stored encoded (about 2 bytes per plain
// character) and the oldest are dropped when the budget is exceeded.
type Scrollback struct {
	budget int
	lines  [][]byte // ring; lines[head] is the oldest
	head   int
	n      int
	bytes  int
	pushed uint64 // lines ever pushed, so readers can detect new history
}

// NewScrollback returns a store holding at most budget bytes of lines.
func NewScrollback(budget int) *Scrollback {
	return &Scrollback{budget: budget}
}

// Push appends a line. Trailing blank cells are not stored.
func (s *Scrollback) Push(line []Cell) {
	s.pushed++
	if s.budget <= 0 {
		return
	}
	enc := appendCells(nil, trimBlank(line))
	if len(enc) > s.budget {
		return
	}
	if s.n == len(s.lines) {
		s.grow()
	}
	s.lines[(s.head+s.n)%len(s.lines)] = enc
	s.n++
	s.bytes += len(enc)
	for s.bytes > s.budget {
		s.bytes -= len(s.lines[s.head])
		s.lines[s.head] = nil
		s.head = (s.head + 1) % len(s.lines)
		s.n--
	}
}

func (s *Scrollback) grow() {
	next := make([][]byte, max(64, 2*len(s.lines)))
	for i := range s.n {
		next[i] = s.lines[(s.head+i)%len(s.lines)]
	}
	s.lines, s.head = next, 0
}

// Len returns the number of stored lines.
func (s *Scrollback) Len() int { return s.n }

// Bytes returns the encoded size of the stored lines.
func (s *Scrollback) Bytes() int { return s.bytes }

// Pushed returns how many lines were ever pushed, including dropped ones.
func (s *Scrollback) Pushed() uint64 { return s.pushed }

// Line returns stored line i (0 is the oldest). Its length is the number of
// cells that were not trailing blanks.
func (s *Scrollback) Line(i int) []Cell {
	if i < 0 || i >= s.n {
		return nil
	}
	cells, _, err := readCells(s.lines[(s.head+i)%len(s.lines)])
	if err != nil {
		return nil // cannot happen: the data was encoded here
	}
	return cells
}

// Tail returns up to n of the newest lines, oldest first.
func (s *Scrollback) Tail(n int) [][]Cell {
	n = min(n, s.n)
	out := make([][]Cell, 0, n)
	for i := s.n - n; i < s.n; i++ {
		out = append(out, s.Line(i))
	}
	return out
}

func trimBlank(line []Cell) []Cell {
	end := len(line)
	for end > 0 && line[end-1].IsBlank() {
		end--
	}
	return line[:end]
}

// Since returns the stored lines pushed after the first pushed lines (as
// counted by Pushed), oldest first, and the current count. Lines dropped
// for the budget are skipped.
func (s *Scrollback) Since(pushed uint64) ([][]Cell, uint64) {
	if pushed >= s.pushed {
		return nil, s.pushed
	}
	newer := s.pushed - pushed // lines pushed since
	first := 0
	if newer < uint64(s.n) {
		first = s.n - int(newer)
	}
	out := make([][]Cell, 0, s.n-first)
	for i := first; i < s.n; i++ {
		out = append(out, s.Line(i))
	}
	return out, s.pushed
}
