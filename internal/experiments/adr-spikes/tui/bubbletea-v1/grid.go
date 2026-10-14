package main

import (
	"fmt"
	"strings"
)

const (
	cols, rows   = 3, 3
	paneW, paneH = 70, 14 // inner size; borders add 2 each way → 216x48
)

// grid is the shared state: 9 panes of log lines plus a typing pane.
type grid struct {
	lines [cols * rows][]string
	n     int
}

func newGrid() *grid {
	g := &grid{}
	for p := range g.lines {
		for i := range paneH {
			g.lines[p] = append(g.lines[p], fmt.Sprintf("pane %d boot line %d", p, i))
		}
	}
	return g
}

// scrollAll appends one coloured log line to every pane (busy agents).
func (g *grid) scrollAll() {
	g.n++
	for p := range g.lines {
		l := fmt.Sprintf("\x1b[38;5;%dm› edit\x1b[0m src/file_%05d.ts +%d -%d %s", (g.n+p)%256, g.n, g.n%97, g.n%13, strings.Repeat("·", g.n%30))
		g.lines[p] = append(g.lines[p][1:], l)
	}
}

// typeChar appends a character to the last line of pane 4 (a user typing).
func (g *grid) typeChar() {
	g.n++
	last := len(g.lines[4]) - 1
	if len(g.lines[4][last]) > paneW-2 {
		g.lines[4][last] = ""
	}
	g.lines[4][last] += string(rune('a' + g.n%26))
}

func (g *grid) paneText(p int) string {
	var b strings.Builder
	for i, l := range g.lines[p] {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
	}
	return b.String()
}
