// Package compositor draws the multiplexer's screen: panes from their
// terminal cells, the borders and titles between them, popups, the sidebar,
// the tab bar and overlays. It is pure: a Scene goes in, cells come out in
// an ultraviolet screen buffer, so every mode can be checked with golden
// screens and the renderer's cell diff decides what reaches the terminal.
//
// The screen is laid out as
//
//	sidebar │ tab bar
//	        │ ─ title ───┬─ title ───   header row: the title line of top panes
//	        │ pane       │ pane
//	        │            ├─ title ───   separators carry the titles of panes below
//	        │            │ pane
//
// Pane rectangles are those of package layout, relative to the pane area;
// the daemon sizes each pane's terminal to its rectangle, so the client
// reports the pane area (Regions.Panes) as the tab's size.
package compositor

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/tui/theme"
	"github.com/admirable-oss/hive/internal/vt"
)

// Regions are the parts of the screen.
type Regions struct {
	Sidebar uv.Rectangle // empty when hidden
	TabBar  uv.Rectangle
	Header  uv.Rectangle // the title row above the panes
	Panes   uv.Rectangle // the tab's area, reported to the daemon
}

// MobileWidth is the widest screen treated as a phone: the sidebar, when
// shown, takes the whole width instead of sitting beside the panes.
const MobileWidth = 64

// Plan divides a width×height screen. sidebarW is the sidebar's width, 0
// when hidden.
func Plan(width, height, sidebarW int) Regions {
	var r Regions
	if width <= 0 || height <= 0 {
		return r
	}
	x0 := 0
	if sidebarW > 0 {
		if width <= MobileWidth || sidebarW >= width-10 {
			r.Sidebar = uv.Rect(0, 0, width, height)
			return r
		}
		r.Sidebar = uv.Rect(0, 0, sidebarW, height)
		x0 = sidebarW + 1 // one column of separator
	}
	w := width - x0
	r.TabBar = uv.Rect(x0, 0, w, 1)
	if height >= 2 {
		r.Header = uv.Rect(x0, 1, w, 1)
	}
	if height >= 3 {
		r.Panes = uv.Rect(x0, 2, w, height-2)
	}
	return r
}

// Scene is everything on screen.
type Scene struct {
	Width, Height int
	Theme         theme.Theme
	Regions       Regions

	Sidebar *Sidebar
	TabBar  TabBar
	Panes   []Pane // tiled panes of the visible tab
	Popups  []Pane // floating panes, drawn over the tiled ones
	// Empty is shown in the pane area when there are no panes.
	Empty    string
	Overlays []Overlay // drawn last, in order
	Toasts   []Toast
}

// Pane is one pane to draw.
type Pane struct {
	ID      string
	Rect    layout.Rect // relative to Regions.Panes
	Title   string
	Badges  []Span // after the title: [zoom], exit status, …
	Focused bool
	// Lines are the rows to show, usually a screen's Lines; cells beyond the
	// rectangle are cut, missing ones are blank.
	Lines [][]vt.Cell
	// Cursor is drawn for the focused pane unless it is hidden.
	Cursor *vt.Cursor
	// Highlights restyle parts of rows: the copy-mode selection, matches.
	Highlights []Highlight
	// Dim fades the content (the process has exited).
	Dim bool
	// Placeholder is centred in the pane when there are no lines.
	Placeholder string
}

// Highlight restyles cells [X1, X2) of row Y.
type Highlight struct {
	Y, X1, X2 int
	Kind      HighlightKind
}

// HighlightKind selects a highlight's style.
type HighlightKind uint8

const (
	HighlightSelection HighlightKind = iota
	HighlightMatch
	HighlightCurrentMatch
	HighlightCursor // copy mode's cursor cell
)

// TabBar is the row above the panes.
type TabBar struct {
	Left  []Span // usually the environment's name
	Tabs  []Tab
	Right []Span // mode indicator, hints
}

// Tab is one tab label.
type Tab struct {
	ID       string
	Label    string
	Active   bool
	Activity bool // output since the tab was last shown
}

// Sidebar is the left column.
type Sidebar struct {
	Rows   []SidebarRow
	Scroll int // first row shown
}

// SidebarRow is one row of the sidebar.
type SidebarRow struct {
	ID       string // what a click selects; empty for headers
	Kind     HitKind
	Spans    []Span
	Selected bool
	Header   bool
}

// Overlay is a box over the screen: help, a picker, a prompt, a dialog.
type Overlay struct {
	Title string
	// Width and Height are the box's outer size; 0 sizes it to fit, and it
	// never exceeds the screen.
	Width, Height int
	Input         *Input // an input line at the top
	Lines         []Spans
	Selected      int // highlighted line, -1 for none
	Scroll        int // first line shown
	Footer        []Span
	// Side, when set, is a pane drawn to the right of the lines (a
	// preview); the lines then take ListWidth columns (default 40%).
	Side      *Pane
	ListWidth int
}

// Input is an editable line.
type Input struct {
	Prompt string
	Text   string
	Cursor int // in runes
}

// Toast is a short notice in the top-right corner.
type Toast struct {
	Text string
	Kind ToastKind
}

// ToastKind picks a toast's colour.
type ToastKind uint8

const (
	ToastInfo ToastKind = iota
	ToastSuccess
	ToastWarning
	ToastError
)

// HitKind says what was clicked.
type HitKind uint8

const (
	HitNone HitKind = iota
	HitTab
	HitPane      // a pane's content
	HitPaneTitle // a pane's title line
	HitEnvironment
	HitAgent
	HitSidebar       // the sidebar outside any row
	HitSidebarBorder // the column between sidebar and panes
	HitOverlayLine   // a line of the top overlay
	HitNewTab        // the "+" after the tabs
	HitSidebarHeader // a sidebar panel's header (collapses it)
)

// Hit is a clickable area.
type Hit struct {
	Rect  uv.Rectangle
	Kind  HitKind
	ID    string
	Index int // overlay line
}

// Result is what Draw reports back.
type Result struct {
	// Cursor is where the terminal cursor goes; nil hides it.
	Cursor *vt.Cursor
	// Hits are clickable areas, topmost last.
	Hits []Hit
}

// HitAt returns the topmost hit at (x, y).
func (r *Result) HitAt(x, y int) (Hit, bool) {
	p := uv.Pos(x, y)
	for i := len(r.Hits) - 1; i >= 0; i-- {
		if p.In(r.Hits[i].Rect) {
			return r.Hits[i], true
		}
	}
	return Hit{}, false
}

// Compositor draws scenes. It keeps caches between frames, so use one per
// screen.
type Compositor struct {
	styles styles
}

// New returns a compositor.
func New() *Compositor { return &Compositor{} }

// Draw paints s onto scr, which must be s.Width × s.Height.
func (c *Compositor) Draw(scr uv.Screen, s *Scene) Result {
	var res Result
	st := newChrome(s.Theme)
	fill(scr, uv.Rect(0, 0, s.Width, s.Height), uv.Style{})
	reg := s.Regions

	if !reg.Sidebar.Empty() && s.Sidebar != nil {
		c.drawSidebar(scr, reg.Sidebar, s.Sidebar, st, &res)
		if reg.Sidebar.Max.X < s.Width {
			for y := 0; y < s.Height; y++ {
				scr.SetCell(reg.Sidebar.Max.X, y, &uv.Cell{Content: "│", Width: 1, Style: st.border})
			}
			res.Hits = append(res.Hits, Hit{Rect: uv.Rect(reg.Sidebar.Max.X, 0, 1, s.Height), Kind: HitSidebarBorder})
		}
	}
	if !reg.TabBar.Empty() {
		c.drawTabBar(scr, reg.TabBar, &s.TabBar, st, &res)
	}
	if !reg.Panes.Empty() {
		c.drawPanes(scr, s, st, &res)
	}
	for i := range s.Overlays {
		cur, box := c.drawOverlay(scr, s, &s.Overlays[i], st, &res, i == len(s.Overlays)-1)
		switch {
		case cur != nil:
			res.Cursor = cur
		case res.Cursor != nil && uv.Pos(res.Cursor.X, res.Cursor.Y).In(box):
			res.Cursor = nil // covered by the box
		}
	}
	if len(s.Toasts) > 0 {
		c.drawToasts(scr, s, st)
	}
	return res
}

// chrome are the styles derived from a theme.
type chrome struct {
	th                 theme.Theme
	text, muted, bar   uv.Style
	border, accent     uv.Style
	title, titleActive uv.Style
	tab, tabActive     uv.Style
	selected, header   uv.Style
	box, boxBorder     uv.Style
	highlight          [4]uv.Style
	toast              [4]uv.Style
}

func newChrome(th theme.Theme) chrome {
	c := chrome{th: th}
	c.text = uv.Style{Fg: th.Fg}
	c.muted = uv.Style{Fg: th.Muted}
	c.bar = uv.Style{Fg: th.Fg, Bg: th.Bg}
	c.border = uv.Style{Fg: th.Border}
	c.accent = uv.Style{Fg: th.Accent}
	c.title = uv.Style{Fg: th.Muted}
	c.titleActive = uv.Style{Fg: th.Accent, Attrs: uv.AttrBold}
	c.tab = uv.Style{Fg: th.Muted, Bg: th.Bg}
	c.tabActive = uv.Style{Fg: th.Accent, Bg: th.Bg, Attrs: uv.AttrBold, Underline: uv.UnderlineSingle}
	if th.Bg == nil {
		// The terminal theme has no chrome background: mark the active
		// tab by reversing it instead.
		c.tabActive = uv.Style{Attrs: uv.AttrBold | uv.AttrReverse}
	}
	c.selected = uv.Style{Fg: th.Fg, Bg: th.Selection, Attrs: uv.AttrBold}
	c.header = uv.Style{Fg: th.Muted, Bg: th.Bg, Attrs: uv.AttrBold}
	c.box = uv.Style{Fg: th.Fg, Bg: th.Bg}
	c.boxBorder = uv.Style{Fg: th.Accent, Bg: th.Bg}
	c.highlight = [4]uv.Style{
		HighlightSelection:    {Bg: th.Selection},
		HighlightMatch:        {Fg: th.Bg, Bg: th.Warning},
		HighlightCurrentMatch: {Fg: th.Bg, Bg: th.Accent, Attrs: uv.AttrBold},
		HighlightCursor:       {Attrs: uv.AttrReverse},
	}
	if th.Bg == nil {
		c.highlight[HighlightMatch] = uv.Style{Attrs: uv.AttrReverse, Underline: uv.UnderlineSingle}
		c.highlight[HighlightCurrentMatch] = uv.Style{Attrs: uv.AttrReverse | uv.AttrBold}
		c.highlight[HighlightSelection] = uv.Style{Attrs: uv.AttrReverse}
	}
	c.toast = [4]uv.Style{
		ToastInfo:    {Fg: th.Info, Bg: th.Bg},
		ToastSuccess: {Fg: th.Success, Bg: th.Bg},
		ToastWarning: {Fg: th.Warning, Bg: th.Bg},
		ToastError:   {Fg: th.Error, Bg: th.Bg, Attrs: uv.AttrBold},
	}
	return c
}

func (c *Compositor) drawSidebar(scr uv.Screen, r uv.Rectangle, sb *Sidebar, st chrome, res *Result) {
	fill(scr, r, st.bar)
	res.Hits = append(res.Hits, Hit{Rect: r, Kind: HitSidebar})
	start := max(sb.Scroll, 0)
	for i := 0; i < r.Dy() && start+i < len(sb.Rows); i++ {
		row := &sb.Rows[start+i]
		y := r.Min.Y + i
		base := st.bar
		switch {
		case row.Selected:
			base = st.selected
			fill(scr, uv.Rect(r.Min.X, y, r.Dx(), 1), base)
		case row.Header:
			base = st.header
		}
		spans := make(Spans, len(row.Spans))
		for j, sp := range row.Spans {
			spans[j] = Span{Text: sp.Text, Style: over(base, sp.Style)}
		}
		drawSpans(scr, r.Min.X+1, y, r.Dx()-2, spans)
		if row.ID != "" {
			res.Hits = append(res.Hits, Hit{Rect: uv.Rect(r.Min.X, y, r.Dx(), 1), Kind: row.Kind, ID: row.ID})
		}
	}
}

// over applies the colours and attributes set in top on base.
func over(base, top uv.Style) uv.Style {
	if top.Fg != nil {
		base.Fg = top.Fg
	}
	if top.Bg != nil {
		base.Bg = top.Bg
	}
	base.Attrs |= top.Attrs
	if top.Underline != 0 {
		base.Underline = top.Underline
	}
	return base
}

func (c *Compositor) drawTabBar(scr uv.Screen, r uv.Rectangle, tb *TabBar, st chrome, res *Result) {
	fill(scr, r, st.bar)
	y := r.Min.Y
	end := r.Max.X
	right := Spans(tb.Right)
	rightW := right.Width()
	if rightW > 0 && rightW < r.Dx()/2 {
		end -= rightW + 1
		drawSpans(scr, end+1, y, rightW, withBase(right, st.bar))
	}
	x := r.Min.X + 1
	if left := Spans(tb.Left); len(left) > 0 {
		x = drawSpans(scr, x, y, end-x, withBase(left, over(st.bar, uv.Style{Attrs: uv.AttrBold})))
		x = drawText(scr, x, y, end-x, " │", uv.Style{Fg: st.th.Border, Bg: st.th.Bg})
	}
	for _, t := range tb.Tabs {
		if x >= end {
			break
		}
		label := " " + t.Label + " "
		if t.Activity && !t.Active {
			label = " " + t.Label + "• "
		}
		style := st.tab
		if t.Active {
			style = st.tabActive
		}
		x0 := x
		x = drawText(scr, x, y, end-x, label, style)
		res.Hits = append(res.Hits, Hit{Rect: uv.Rect(x0, y, x-x0, 1), Kind: HitTab, ID: t.ID})
	}
	if x+3 <= end {
		x0 := x
		x = drawText(scr, x, y, 3, " + ", st.tab)
		res.Hits = append(res.Hits, Hit{Rect: uv.Rect(x0, y, x-x0, 1), Kind: HitNewTab})
	}
}

func withBase(ss Spans, base uv.Style) Spans {
	out := make(Spans, len(ss))
	for i, s := range ss {
		out[i] = Span{Text: s.Text, Style: over(base, s.Style)}
	}
	return out
}

func (c *Compositor) drawPanes(scr uv.Screen, s *Scene, st chrome, res *Result) {
	area := s.Regions.Panes
	ox, oy := area.Min.X, area.Min.Y
	b := newBorders(area.Dx(), area.Dy())
	for i := range s.Panes {
		b.addPane(s.Panes[i].Rect, s.Panes[i].Focused)
	}
	if len(s.Panes) == 0 {
		b.hline(0, area.Dx()-1, -1, false)
	}
	b.draw(scr, ox, oy, st.border, st.accent)

	for i := range s.Panes {
		p := &s.Panes[i]
		if cur := c.drawPane(scr, ox, oy, area, p, st, res); cur != nil {
			res.Cursor = cur
		}
		c.drawTitle(scr, ox+p.Rect.X, oy+p.Rect.Y-1, p.Rect.W, p, st, res)
	}
	if len(s.Panes) == 0 && s.Empty != "" {
		c.centre(scr, area, s.Empty, st.muted)
	}

	for i := range s.Popups {
		p := &s.Popups[i]
		r := p.Rect
		box := uv.Rect(ox+r.X-1, oy+r.Y-1, r.W+2, r.H+2).Intersect(area.Union(s.Regions.Header))
		drawBox(scr, box, uv.Style{Fg: st.th.Accent}, "")
		if cur := c.drawPane(scr, ox, oy, area, p, st, res); cur != nil {
			res.Cursor = cur
		}
		c.drawTitle(scr, ox+r.X, oy+r.Y-1, r.W, p, st, res)
	}
}

// drawTitle writes a pane's title and badges on the line at (x, y).
func (c *Compositor) drawTitle(scr uv.Screen, x, y, w int, p *Pane, st chrome, res *Result) {
	if w < 4 {
		return
	}
	style := st.title
	if p.Focused {
		style = st.titleActive
	}
	spans := Spans{{Text: " " + Truncate(p.Title, max(w-4, 1)) + " ", Style: style}}
	for _, b := range p.Badges {
		spans = append(spans, Span{Text: b.Text + " ", Style: b.Style})
	}
	drawSpans(scr, x+1, y, w-2, spans)
	res.Hits = append(res.Hits, Hit{Rect: uv.Rect(x, y, w, 1), Kind: HitPaneTitle, ID: p.ID})
}

// drawPane draws a pane's content and returns its cursor, if shown.
func (c *Compositor) drawPane(scr uv.Screen, ox, oy int, area uv.Rectangle, p *Pane, st chrome, res *Result) *vt.Cursor {
	r := p.Rect
	if r.W <= 0 || r.H <= 0 {
		return nil
	}
	x0, y0 := ox+r.X, oy+r.Y
	rect := uv.Rect(x0, y0, r.W, r.H).Intersect(area)
	res.Hits = append(res.Hits, Hit{Rect: rect, Kind: HitPane, ID: p.ID})
	fill(scr, rect, uv.Style{})
	if len(p.Lines) == 0 && p.Placeholder != "" {
		c.centre(scr, rect, p.Placeholder, st.muted)
		return nil
	}
	var hl map[int][]Highlight
	if len(p.Highlights) > 0 {
		hl = map[int][]Highlight{}
		for _, h := range p.Highlights {
			hl[h.Y] = append(hl[h.Y], h)
		}
	}
	for y := 0; y < r.H && y < len(p.Lines); y++ {
		line := p.Lines[y]
		for x := 0; x < r.W && x < len(line); x++ {
			cell := line[x]
			if cell.Width == 0 {
				continue // the right half of a wide character
			}
			if x+int(cell.Width) > r.W {
				break // a wide character cut by the border
			}
			style := c.styles.get(cell.Style)
			if p.Dim {
				style.Attrs |= uv.AttrFaint
			}
			for _, h := range hl[y] {
				if x >= h.X1 && x < h.X2 {
					style = over(style, st.highlight[h.Kind])
					if h.Kind == HighlightCursor {
						style.Attrs |= uv.AttrReverse
					}
				}
			}
			content := cell.Content
			if content == "" {
				content = " "
			}
			out := uv.Cell{Content: content, Width: int(cell.Width), Style: style}
			if cell.Link != "" {
				out.Link = uv.Link{URL: cell.Link} // the outer terminal makes it clickable
			}
			scr.SetCell(x0+x, y0+y, &out)
		}
		// Highlights past the end of the text (an empty selected line).
		for _, h := range hl[y] {
			for x := max(h.X1, len(line)); x < h.X2 && x < r.W; x++ {
				scr.SetCell(x0+x, y0+y, &uv.Cell{Content: " ", Width: 1, Style: st.highlight[h.Kind]})
			}
		}
	}
	if !p.Focused || p.Cursor == nil || p.Cursor.Hidden || p.Dim {
		return nil
	}
	if p.Cursor.X < 0 || p.Cursor.Y < 0 || p.Cursor.X >= r.W || p.Cursor.Y >= r.H {
		return nil
	}
	cur := *p.Cursor
	cur.X += x0
	cur.Y += y0
	return &cur
}

// centre writes text in the middle of r.
func (c *Compositor) centre(scr uv.Screen, r uv.Rectangle, text string, st uv.Style) {
	lines := strings.Split(text, "\n")
	y := r.Min.Y + (r.Dy()-len(lines))/2
	for i, l := range lines {
		l = Truncate(l, r.Dx())
		w := Spans{{Text: l}}.Width()
		drawText(scr, r.Min.X+(r.Dx()-w)/2, y+i, r.Dx(), l, st)
	}
}

// drawBox draws a rounded box around r's edge, with title on the top line.
func drawBox(scr uv.Screen, r uv.Rectangle, st uv.Style, title string) {
	if r.Dx() < 2 || r.Dy() < 2 {
		return
	}
	x1, y1, x2, y2 := r.Min.X, r.Min.Y, r.Max.X-1, r.Max.Y-1
	set := func(x, y int, s string) { scr.SetCell(x, y, &uv.Cell{Content: s, Width: 1, Style: st}) }
	for x := x1 + 1; x < x2; x++ {
		set(x, y1, "─")
		set(x, y2, "─")
	}
	for y := y1 + 1; y < y2; y++ {
		set(x1, y, "│")
		set(x2, y, "│")
	}
	set(x1, y1, "╭")
	set(x2, y1, "╮")
	set(x1, y2, "╰")
	set(x2, y2, "╯")
	if title != "" && r.Dx() > 6 {
		drawText(scr, x1+2, y1, r.Dx()-4, " "+Truncate(title, r.Dx()-6)+" ", over(st, uv.Style{Attrs: uv.AttrBold}))
	}
}

// drawOverlay draws a box and returns its input's cursor, if any, and the
// box's area.
func (c *Compositor) drawOverlay(scr uv.Screen, s *Scene, o *Overlay, st chrome, res *Result, top bool) (*vt.Cursor, uv.Rectangle) {
	w, h := o.Width, o.Height
	if w <= 0 {
		w = len(o.Title) + 8
		for _, l := range o.Lines {
			w = max(w, l.Width()+4)
		}
		if o.Input != nil {
			w = max(w, 40)
		}
	}
	if h <= 0 {
		h = len(o.Lines) + 2
		if o.Input != nil {
			h += 2
		}
		if len(o.Footer) > 0 {
			h++
		}
	}
	w, h = min(w, s.Width), min(h, s.Height)
	if w < 4 || h < 3 {
		return nil, uv.Rectangle{}
	}
	r := uv.Rect((s.Width-w)/2, (s.Height-h)/2, w, h)
	fill(scr, r, st.box)
	drawBox(scr, r, st.boxBorder, o.Title)
	inner := uv.Rect(r.Min.X+2, r.Min.Y+1, r.Dx()-4, r.Dy()-2)
	y := inner.Min.Y
	var cursor *vt.Cursor
	if o.Input != nil {
		x := drawText(scr, inner.Min.X, y, inner.Dx(), o.Input.Prompt, over(st.box, uv.Style{Fg: st.th.Accent, Attrs: uv.AttrBold}))
		runes := []rune(o.Input.Text)
		pos := min(max(o.Input.Cursor, 0), len(runes))
		before := string(runes[:pos])
		// Keep the cursor visible in a long input by showing its tail.
		avail := inner.Max.X - x
		for Spans([]Span{{Text: before}}).Width() >= avail && len(before) > 0 {
			_, size := firstRune(before)
			before = before[size:]
		}
		cx := drawText(scr, x, y, avail, before, st.box)
		drawText(scr, cx, y, inner.Max.X-cx, string(runes[pos:]), st.box)
		if top {
			cursor = &vt.Cursor{X: cx, Y: y, Shape: vt.CursorBar}
		}
		y += 2
	}
	bodyH := inner.Max.Y - y
	if len(o.Footer) > 0 {
		bodyH--
		drawSpans(scr, inner.Min.X, inner.Max.Y-1, inner.Dx(), withBase(o.Footer, over(st.box, uv.Style{Fg: st.th.Muted})))
	}
	listW := inner.Dx()
	if o.Side != nil && bodyH > 0 {
		listW = o.ListWidth
		if listW <= 0 {
			listW = inner.Dx() * 2 / 5
		}
		listW = min(listW, inner.Dx()-3)
		sepX := inner.Min.X + listW + 1
		for i := range bodyH {
			scr.SetCell(sepX, y+i, &uv.Cell{Content: "│", Width: 1, Style: over(st.box, uv.Style{Fg: st.th.Border})})
		}
		side := *o.Side
		side.Rect = layout.Rect{W: inner.Max.X - sepX - 1, H: bodyH}
		if side.Rect.W > 0 {
			c.drawPane(scr, sepX+1, y, uv.Rect(sepX+1, y, side.Rect.W, bodyH), &side, st, res)
		}
	}
	scroll := max(o.Scroll, 0)
	if o.Selected >= 0 {
		// Keep the selection in view.
		if o.Selected < scroll {
			scroll = o.Selected
		}
		if o.Selected >= scroll+bodyH {
			scroll = o.Selected - bodyH + 1
		}
	}
	for i := 0; i < bodyH && scroll+i < len(o.Lines); i++ {
		idx := scroll + i
		base := st.box
		rowW := r.Dx() - 2
		if o.Side != nil {
			rowW = listW + 2
		}
		if idx == o.Selected {
			base = st.selected
			fill(scr, uv.Rect(r.Min.X+1, y+i, rowW, 1), base)
		}
		drawSpans(scr, inner.Min.X, y+i, listW, withBase(o.Lines[idx], base))
		if top {
			res.Hits = append(res.Hits, Hit{Rect: uv.Rect(r.Min.X+1, y+i, rowW, 1), Kind: HitOverlayLine, Index: idx})
		}
	}
	if len(o.Lines) > bodyH && bodyH > 0 {
		// A scroll position marker on the right border.
		pos := scroll * (bodyH - 1) / max(len(o.Lines)-bodyH, 1)
		scr.SetCell(r.Max.X-1, y+min(pos, bodyH-1), &uv.Cell{Content: "┃", Width: 1, Style: st.boxBorder})
	}
	return cursor, r
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func (c *Compositor) drawToasts(scr uv.Screen, s *Scene, st chrome) {
	y := 1
	for _, t := range s.Toasts {
		text := " " + Truncate(t.Text, max(s.Width/2-2, 10)) + " "
		w := Spans{{Text: text}}.Width()
		x := s.Width - w - 1
		if x < 0 || y >= s.Height {
			return
		}
		drawText(scr, x, y, w, text, st.toast[t.Kind])
		y++
	}
}
