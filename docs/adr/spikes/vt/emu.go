package main

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/vt"
	"github.com/hinshun/vt10x"
)

// screen extracts the visible text grid (rows right-trimmed).
type screener interface {
	Write([]byte) (int, error)
	Rows() []string
	Name() string
}

type xvt struct{ e *vt.Emulator }

func newXVT(w, h int) *xvt { return newXVTsized(w, h) }

// newXVTsized builds an emulator whose query replies are drained, as the
// shim will have to do (replies otherwise block Write).
func newXVTsized(w, h int) *xvt {
	e := vt.NewEmulator(w, h)
	go func() { _, _ = io.Copy(io.Discard, e) }()
	return &xvt{e}
}

func (x *xvt) Name() string                  { return "x/vt" }
func (x *xvt) Write(p []byte) (int, error)   { return x.e.Write(p) }
func (x *xvt) Rows() []string {
	rows := make([]string, x.e.Height())
	for y := range rows {
		var b strings.Builder
		for col := 0; col < x.e.Width(); col++ {
			c := x.e.CellAt(col, y)
			switch {
			case c == nil || (c.Content == "" && c.Width == 0 && col > 0):
				// continuation of a wide character
				if c == nil {
					b.WriteByte(' ')
				}
			case c.Content == "":
				b.WriteByte(' ')
			default:
				b.WriteString(c.Content)
			}
		}
		rows[y] = strings.TrimRight(b.String(), " ")
	}
	return rows
}

type x10 struct {
	t    vt10x.Terminal
	w, h int
}

func newX10(w, h int) *x10 { return &x10{vt10x.New(vt10x.WithSize(w, h)), w, h} }

func (x *x10) Name() string                { return "vt10x" }
func (x *x10) Write(p []byte) (int, error) { return x.t.Write(p) }
func (x *x10) Rows() []string {
	x.t.Lock()
	defer x.t.Unlock()
	rows := make([]string, x.h)
	for y := range rows {
		var b strings.Builder
		for col := 0; col < x.w; col++ {
			r := x.t.Cell(col, y).Char
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		rows[y] = strings.TrimRight(b.String(), " ")
	}
	return rows
}
