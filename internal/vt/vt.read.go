package vt

import "strings"

// Unwrap joins lines that continue on the next line. Terminals do not say
// which lines wrapped, so a line that fills the full width is taken to
// continue (the same inference tmux makes without its own wrap flags).
func Unwrap(lines [][]Cell, width int) []string {
	var (
		out []string
		cur strings.Builder
	)
	for i, l := range lines {
		cur.WriteString(LineText(l))
		full := len(l) >= width && width > 0 && !l[width-1].IsBlank()
		if !full || i == len(lines)-1 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	return out
}
