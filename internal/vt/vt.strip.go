package vt

import "io"

// GraphicsStripper writes a terminal byte stream to W without its Kitty
// graphics commands (ESC _ G … ESC \), so a log of agent output never keeps
// image data. Commands may span writes; at most two bytes (ESC, ESC _) are
// held back between writes. Every other byte passes through unchanged, in
// order, including other APC strings, with one addition: an APC introducer
// that passed through, whose string a dropped graphics command ended, is
// closed with ST so it does not swallow the rest of the log.
type GraphicsStripper struct {
	W     io.Writer
	state stripState
	out   []byte
	// dangling marks an ESC _ written for an APC string the input aborted:
	// until something else is written, it still opens a string in W.
	dangling bool
}

type stripState uint8

const (
	stripGround   stripState = iota
	stripEscape              // ESC held
	stripAPC                 // ESC _ held: G decides
	stripGraphics            // inside a graphics command: dropped
	stripGraphicsEscape
)

// NewGraphicsStripper returns a stripper writing to w.
func NewGraphicsStripper(w io.Writer) *GraphicsStripper { return &GraphicsStripper{W: w} }

// Write strips p and writes the rest. It reports len(p) on success, since
// every byte of p was consumed (written, held or dropped).
func (s *GraphicsStripper) Write(p []byte) (int, error) {
	out := s.out[:0]
	for i := 0; i < len(p); i++ {
		b := p[i]
		switch s.state {
		case stripGround:
			if b == 0x1b {
				s.state = stripEscape
			} else {
				out = append(out, b)
				s.dangling = false
			}
		case stripEscape:
			switch b {
			case '_':
				s.state = stripAPC
			case 0x1b:
				out = append(out, 0x1b) // the first ESC was not ours to hold
				s.dangling = false
			default:
				out = append(out, 0x1b, b)
				s.dangling = false
				s.state = stripGround
			}
		case stripAPC:
			switch b {
			case 'G':
				// What ended the APC string written just above is this
				// command, which is dropped: close the string, or the log
				// keeps an introducer that swallows everything after it
				// (and ESC _ G would read as a graphics command again).
				if s.dangling {
					out = append(out, 0x1b, '\\')
					s.dangling = false
				}
				s.state = stripGraphics
			case 0x1b:
				out = append(out, 0x1b, '_')
				s.dangling = true
				s.state = stripEscape
			default:
				out = append(out, 0x1b, '_', b)
				s.dangling = false
				s.state = stripGround
			}
		case stripGraphics:
			switch b {
			case 0x1b:
				s.state = stripGraphicsEscape
			case 0x18, 0x1a: // CAN, SUB abort the string
				s.state = stripGround
			}
		case stripGraphicsEscape:
			switch b {
			case '\\', 0x18, 0x1a:
				s.state = stripGround
			case 0x1b:
			default:
				// Any other ESC ended the command (as terminals parse
				// it); that ESC starts a sequence b continues.
				s.state = stripEscape
				i--
			}
		}
	}
	s.out = out[:0]
	if cap(s.out) > 64<<10 {
		s.out = nil
	}
	if len(out) == 0 {
		return len(p), nil
	}
	if _, err := s.W.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close writes any held ESC or ESC _ (a stream that ended mid-sequence) and
// closes W when it is an io.Closer.
func (s *GraphicsStripper) Close() error {
	var err error
	switch s.state {
	case stripEscape:
		_, err = s.W.Write([]byte{0x1b})
	case stripAPC:
		_, err = s.W.Write([]byte{0x1b, '_'})
	}
	s.state, s.dangling = stripGround, false
	if c, ok := s.W.(io.Closer); ok {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	return err
}
