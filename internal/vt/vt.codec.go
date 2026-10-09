package vt

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frames travel as a 4-byte big-endian length followed by the encoded frame.
// The encoding is compact rather than self-describing: it is internal to
// Hive, and both ends come from the same build or negotiate it (protocol
// capability "frames/1").
//
//	frame: flags u8 · cols · rows · cursor x · cursor y · cursor flags u8 ·
//	       modes · bells · title · line count · lines
//	line:  y · cells
//	cells: run count · runs; run: style · cell count · cells
//	cell:  len(content)<<2 | width · content bytes
//	style: attrs u8 · underline u8 · fg · bg · underline colour
//
// Every integer without a size is an unsigned varint; strings are a varint
// length and bytes.

// MaxFrameSize bounds a decoded frame. The largest real frame is a keyframe
// of a big, busy screen; anything larger is corruption.
const MaxFrameSize = 32 << 20

// FrameCapability names this encoding in protocol negotiation.
const FrameCapability = "frames/1"

var errCorrupt = errors.New("vt: corrupt frame")

const (
	flagKeyframe = 1 << iota
)

const (
	cursorHidden = 1 << iota
	cursorBlink
)

// AppendFrame appends f, length-prefixed, to dst.
func AppendFrame(dst []byte, f *Frame) []byte {
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0)
	var flags byte
	if f.Keyframe {
		flags |= flagKeyframe
	}
	dst = append(dst, flags)
	dst = binary.AppendUvarint(dst, uint64(f.Cols))
	dst = binary.AppendUvarint(dst, uint64(f.Rows))
	dst = binary.AppendUvarint(dst, uint64(max(f.Cursor.X, 0)))
	dst = binary.AppendUvarint(dst, uint64(max(f.Cursor.Y, 0)))
	var cf byte
	if f.Cursor.Hidden {
		cf |= cursorHidden
	}
	if f.Cursor.Blink {
		cf |= cursorBlink
	}
	dst = append(dst, cf|byte(f.Cursor.Shape)<<2)
	dst = binary.AppendUvarint(dst, uint64(f.Modes))
	dst = binary.AppendUvarint(dst, uint64(f.Bells))
	dst = appendString(dst, f.Title)
	dst = binary.AppendUvarint(dst, uint64(len(f.Lines)))
	for _, l := range f.Lines {
		dst = binary.AppendUvarint(dst, uint64(l.Y))
		dst = appendCells(dst, l.Cells)
	}
	binary.BigEndian.PutUint32(dst[start:], uint32(len(dst)-start-4))
	return dst
}

// DecodeFrame decodes one frame body (without the length prefix).
func DecodeFrame(b []byte) (*Frame, error) {
	d := decoder{b: b}
	f := &Frame{}
	flags := d.byte()
	f.Keyframe = flags&flagKeyframe != 0
	f.Cols, f.Rows = d.int(), d.int()
	f.Cursor.X, f.Cursor.Y = d.int(), d.int()
	cf := d.byte()
	f.Cursor.Hidden, f.Cursor.Blink, f.Cursor.Shape = cf&cursorHidden != 0, cf&cursorBlink != 0, CursorShape(cf>>2)
	f.Modes = Modes(d.uvarint())
	f.Bells = uint32(d.uvarint())
	f.Title = d.string()
	n := d.int()
	if d.err == nil && (f.Cols < 1 || f.Rows < 1 || f.Cols > MaxCols || f.Rows > MaxRows || n > f.Rows) {
		return nil, errCorrupt
	}
	for range n {
		y := d.int()
		cells := d.cells(f.Cols)
		if d.err != nil {
			break
		}
		f.Lines = append(f.Lines, LineUpdate{Y: y, Cells: cells})
	}
	if d.err != nil {
		return nil, d.err
	}
	if len(d.b) != 0 {
		return nil, errCorrupt
	}
	return f, nil
}

// FrameReader reads length-prefixed frames from a byte stream.
type FrameReader struct {
	r   *bufio.Reader
	buf []byte
}

// NewFrameReader returns a reader of the frames in r.
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: bufio.NewReaderSize(r, 64<<10)}
}

// Next returns the next frame. It returns io.EOF at a clean end of stream.
func (fr *FrameReader) Next() (*Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(fr.r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrameSize {
		return nil, fmt.Errorf("%w: frame of %d bytes", errCorrupt, n)
	}
	if cap(fr.buf) < int(n) {
		fr.buf = make([]byte, n)
	}
	body := fr.buf[:n]
	if _, err := io.ReadFull(fr.r, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return DecodeFrame(body)
}

func appendString(dst []byte, s string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

// appendCells encodes cells as runs of equal style.
func appendCells(dst []byte, cells []Cell) []byte {
	runs := 0
	for i := range cells {
		if i == 0 || cells[i].Style != cells[i-1].Style {
			runs++
		}
	}
	dst = binary.AppendUvarint(dst, uint64(runs))
	for i := 0; i < len(cells); {
		j := i + 1
		for j < len(cells) && cells[j].Style == cells[i].Style {
			j++
		}
		dst = appendStyle(dst, cells[i].Style)
		dst = binary.AppendUvarint(dst, uint64(j-i))
		for _, c := range cells[i:j] {
			dst = binary.AppendUvarint(dst, uint64(len(c.Content))<<2|uint64(c.Width&3))
			dst = append(dst, c.Content...)
		}
		i = j
	}
	return dst
}

func appendStyle(dst []byte, s Style) []byte {
	dst = append(dst, byte(s.Attrs), byte(s.Underline))
	dst = binary.AppendUvarint(dst, uint64(s.Fg))
	dst = binary.AppendUvarint(dst, uint64(s.Bg))
	return binary.AppendUvarint(dst, uint64(s.UnderlineColor))
}

// readCells decodes an appendCells encoding and returns the remaining bytes.
func readCells(b []byte) ([]Cell, []byte, error) {
	d := decoder{b: b}
	cells := d.cells(-1)
	return cells, d.b, d.err
}

type decoder struct {
	b   []byte
	err error
}

func (d *decoder) fail() {
	if d.err == nil {
		d.err = errCorrupt
	}
	d.b = nil
}

func (d *decoder) byte() byte {
	if d.err != nil || len(d.b) == 0 {
		d.fail()
		return 0
	}
	v := d.b[0]
	d.b = d.b[1:]
	return v
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.fail()
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *decoder) int() int {
	v := d.uvarint()
	if v > 1<<31 {
		d.fail()
		return 0
	}
	return int(v)
}

func (d *decoder) string() string {
	n := d.int()
	if d.err != nil || n > len(d.b) {
		d.fail()
		return ""
	}
	s := string(d.b[:n])
	d.b = d.b[n:]
	return s
}

// cells decodes a cell list; limit (when non-negative) bounds its length.
func (d *decoder) cells(limit int) []Cell {
	runs := d.int()
	if d.err != nil || runs > len(d.b) {
		d.fail()
		return nil
	}
	var cells []Cell
	for range runs {
		st := Style{Attrs: Attr(d.byte()), Underline: Underline(d.byte())}
		st.Fg, st.Bg, st.UnderlineColor = Color(d.uvarint()), Color(d.uvarint()), Color(d.uvarint())
		count := d.int()
		if d.err != nil || count > len(d.b) || (limit >= 0 && len(cells)+count > limit) {
			d.fail()
			return nil
		}
		for range count {
			hdr := d.uvarint()
			n := int(hdr >> 2)
			if d.err != nil || n > len(d.b) || n > 64 {
				d.fail()
				return nil
			}
			cells = append(cells, Cell{Content: string(d.b[:n]), Width: uint8(hdr & 3), Style: st})
			d.b = d.b[n:]
		}
	}
	return cells
}
