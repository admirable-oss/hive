package plugin

// Drawing view documents as lines of text, for clients that draw in cells
// (the TUI, `hive plugin views`). Each span keeps its node's style, and
// the button or link it belongs to, so a client can colour it and act on
// a click.

import (
	"fmt"
	"strings"

	"github.com/rivo/uniseg"
)

// ViewSpan is a run of text in one style.
type ViewSpan struct {
	Text string
	// Style is a text style (muted, bold, code, ok, warn, error), a tone
	// (badges), "button", "link", "progress" or "divider".
	Style string
	// Button is the id of the button the span draws; URL the link's.
	Button string
	URL    string
}

// ViewLine is one line of a drawn view.
type ViewLine []ViewSpan

// Width is the line's width in cells.
func (l ViewLine) Width() int {
	w := 0
	for _, s := range l {
		w += uniseg.StringWidth(s.Text)
	}
	return w
}

// String is the line's text.
func (l ViewLine) String() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// DrawView lays a view document out in lines at most width cells wide.
func DrawView(doc ViewDoc, width int) []ViewLine {
	return drawNode(doc.Root, max(width, 1))
}

func drawNode(n ViewNode, width int) []ViewLine {
	switch n.Kind {
	case NodeText:
		var out []ViewLine
		for _, l := range wrapText(n.Text, width) {
			out = append(out, ViewLine{{Text: l, Style: n.Style}})
		}
		return out
	case NodeBadge:
		return []ViewLine{{{Text: fit("["+n.Text+"]", width), Style: cmpStr(n.Style, "info")}}}
	case NodeButton:
		return []ViewLine{{{Text: fit("[ "+n.Text+" ]", width), Style: "button", Button: n.ID}}}
	case NodeLink:
		return []ViewLine{{{Text: fit(cmpStr(n.Text, n.URL), width), Style: "link", URL: n.URL}}}
	case NodeDivider:
		return []ViewLine{{{Text: strings.Repeat("─", width), Style: "divider"}}}
	case NodeProgress:
		return []ViewLine{progressLine(n, width)}
	case NodeTable:
		return tableLines(n, width)
	case NodeRow:
		return rowLines(n, width)
	case NodeColumn:
		var out []ViewLine
		for _, c := range n.Children {
			out = append(out, drawNode(c, width)...)
		}
		return out
	case NodeList:
		var out []ViewLine
		for _, c := range n.Children {
			for i, l := range drawNode(c, max(width-2, 1)) {
				lead := "  "
				if i == 0 {
					lead = "• "
				}
				out = append(out, append(ViewLine{{Text: lead, Style: "muted"}}, l...))
			}
		}
		return out
	}
	return nil
}

// rowLines puts a row's children side by side when each draws in one
// line and they fit; otherwise one under another.
func rowLines(n ViewNode, width int) []ViewLine {
	var parts []ViewLine
	total := 0
	for _, c := range n.Children {
		lines := drawNode(c, width)
		if len(lines) != 1 {
			return stack(n.Children, width)
		}
		parts = append(parts, lines[0])
		total += lines[0].Width()
	}
	if total+2*max(len(parts)-1, 0) > width {
		return stack(n.Children, width)
	}
	var line ViewLine
	for i, p := range parts {
		if i > 0 {
			line = append(line, ViewSpan{Text: "  "})
		}
		line = append(line, p...)
	}
	return []ViewLine{line}
}

func stack(children []ViewNode, width int) []ViewLine {
	var out []ViewLine
	for _, c := range children {
		out = append(out, drawNode(c, width)...)
	}
	return out
}

func progressLine(n ViewNode, width int) ViewLine {
	pct := fmt.Sprintf(" %3.0f%%", n.Value*100)
	label := ""
	if n.Text != "" {
		label = n.Text + " "
	}
	bar := width - uniseg.StringWidth(label) - len(pct)
	if bar < 4 {
		return ViewLine{{Text: fit(label+strings.TrimSpace(pct), width), Style: "progress"}}
	}
	full := int(n.Value*float64(bar) + 0.5)
	return ViewLine{
		{Text: label},
		{Text: strings.Repeat("█", full), Style: "progress"},
		{Text: strings.Repeat("░", bar-full), Style: "muted"},
		{Text: pct},
	}
}

// tableLines draws a table with its columns as wide as their widest cell,
// shrinking the widest ones until the table fits.
func tableLines(n ViewNode, width int) []ViewLine {
	cols := len(n.Columns)
	for _, r := range n.Rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return nil
	}
	widths := make([]int, cols)
	measure := func(cells []string) {
		for i, c := range cells {
			widths[i] = max(widths[i], uniseg.StringWidth(c))
		}
	}
	measure(n.Columns)
	for _, r := range n.Rows {
		measure(r)
	}
	gap := 2
	for sum(widths)+gap*(cols-1) > width {
		i := widest(widths)
		if widths[i] <= 1 {
			break
		}
		widths[i]--
	}
	line := func(cells []string, style string) ViewLine {
		var l ViewLine
		for i := range cols {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			text := pad(fit(c, widths[i]), widths[i])
			if i < cols-1 {
				text += strings.Repeat(" ", gap)
			}
			l = append(l, ViewSpan{Text: text, Style: style})
		}
		return l
	}
	var out []ViewLine
	if len(n.Columns) > 0 {
		out = append(out, line(n.Columns, "bold"))
	}
	for _, r := range n.Rows {
		out = append(out, line(r, ""))
	}
	return out
}

func sum(a []int) int {
	t := 0
	for _, v := range a {
		t += v
	}
	return t
}

func widest(a []int) int {
	best := 0
	for i, v := range a {
		if v > a[best] {
			best = i
		}
	}
	return best
}

// wrapText breaks text into lines of at most width cells, at spaces where
// it can; its own newlines are kept.
func wrapText(text string, width int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line, w := "", 0
		for _, word := range strings.Fields(para) {
			ww := uniseg.StringWidth(word)
			switch {
			case w == 0:
			case w+1+ww <= width:
				line, w = line+" ", w+1
			default:
				out = append(out, line)
				line, w = "", 0
			}
			for ww > width { // a word wider than the line
				head := fit(word, width)
				out = append(out, line+head)
				line, w = "", 0
				word = strings.TrimPrefix(word, head)
				ww = uniseg.StringWidth(word)
			}
			line, w = line+word, w+ww
		}
		out = append(out, line)
	}
	return out
}

// fit cuts s to at most width cells.
func fit(s string, width int) string {
	if uniseg.StringWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		if w+g.Width() > width {
			break
		}
		b.WriteString(g.Str())
		w += g.Width()
	}
	return b.String()
}

func pad(s string, width int) string {
	if w := uniseg.StringWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}
