package mux

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/fuzzy"
	"github.com/admirable-oss/hive/internal/tui/keymap"
)

// overlay is a box that takes the keys while open: help, the Goto picker,
// a prompt, a confirmation.
type overlay interface {
	key(a *App, k uv.Key)
	paste(a *App, text string)
	click(a *App, line int)
	view(a *App) compositor.Overlay
}

func (a *App) closeOverlay() { a.ui.overlay = nil }

// keyName is the binding-style name of a key ("ctrl+u", "enter", "a").
func keyName(k uv.Key) string { return k.Keystroke() }

// --- text input ---

// input is an editable line.
type input struct {
	text   []rune
	cursor int
}

func newInput(s string) input { r := []rune(s); return input{text: r, cursor: len(r)} }

func (in *input) String() string { return string(in.text) }

func (in *input) insert(s string) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	r := []rune(s)
	in.text = slices.Insert(in.text, in.cursor, r...)
	in.cursor += len(r)
}

// edit applies an editing key; it reports false for keys it does not use.
func (in *input) edit(k uv.Key) bool {
	switch keyName(k) {
	case "left", "ctrl+b":
		in.cursor = max(in.cursor-1, 0)
	case "right", "ctrl+f":
		in.cursor = min(in.cursor+1, len(in.text))
	case "home", "ctrl+a":
		in.cursor = 0
	case "end", "ctrl+e":
		in.cursor = len(in.text)
	case "backspace", "ctrl+h":
		if in.cursor > 0 {
			in.text = slices.Delete(in.text, in.cursor-1, in.cursor)
			in.cursor--
		}
	case "delete", "ctrl+d":
		if in.cursor < len(in.text) {
			in.text = slices.Delete(in.text, in.cursor, in.cursor+1)
		}
	case "ctrl+u":
		in.text = in.text[in.cursor:]
		in.cursor = 0
	case "ctrl+k":
		in.text = in.text[:in.cursor]
	case "ctrl+w", "alt+backspace":
		i := in.cursor
		for i > 0 && in.text[i-1] == ' ' {
			i--
		}
		for i > 0 && in.text[i-1] != ' ' {
			i--
		}
		in.text = slices.Delete(in.text, i, in.cursor)
		in.cursor = i
	default:
		if k.Text != "" && k.Mod&^uv.ModShift == 0 {
			in.insert(k.Text)
			return true
		}
		return false
	}
	return true
}

func (in *input) view(prompt string) *compositor.Input {
	return &compositor.Input{Prompt: prompt, Text: in.String(), Cursor: in.cursor}
}

// --- prompt ---

// prompt asks for a line of text.
type prompt struct {
	title  string
	label  string
	in     input
	submit func(string)
}

func (a *App) openPrompt(title, initial string, submit func(string)) {
	a.ui.overlay = &prompt{title: title, label: "› ", in: newInput(initial), submit: submit}
}

func (p *prompt) key(a *App, k uv.Key) {
	switch keyName(k) {
	case "esc", "ctrl+c":
		a.closeOverlay()
	case "enter":
		a.closeOverlay()
		p.submit(p.in.String())
	default:
		p.in.edit(k)
	}
}

func (p *prompt) paste(_ *App, text string) { p.in.insert(text) }
func (p *prompt) click(*App, int)           {}

func (p *prompt) view(a *App) compositor.Overlay {
	return compositor.Overlay{
		Title: p.title, Width: min(60, a.width-4), Input: p.in.view(p.label), Selected: -1,
		Footer: []compositor.Span{{Text: "⏎ ok  esc cancel"}},
	}
}

// --- confirm ---

// confirm asks a yes/no question.
type confirm struct {
	title, message string
	yes            func()
}

func (a *App) openConfirm(title, message string, yes func()) {
	a.ui.overlay = &confirm{title: title, message: message, yes: yes}
}

func (c *confirm) key(a *App, k uv.Key) {
	switch keyName(k) {
	case "y", "Y", "enter":
		a.closeOverlay()
		c.yes()
	case "n", "N", "esc", "q", "ctrl+c":
		a.closeOverlay()
	}
}

func (c *confirm) paste(*App, string) {}

func (c *confirm) click(a *App, line int) {
	if line == 2 {
		a.closeOverlay()
		c.yes()
	}
}

func (c *confirm) view(a *App) compositor.Overlay {
	return compositor.Overlay{
		Title: c.title, Selected: -1,
		Lines: []compositor.Spans{
			{{Text: compositor.Truncate(c.message, max(a.width-8, 10))}},
			{},
			{
				{Text: "y", Style: uv.Style{Fg: a.theme.Accent, Attrs: uv.AttrBold}},
				{Text: " yes   "},
				{Text: "n", Style: uv.Style{Fg: a.theme.Accent, Attrs: uv.AttrBold}},
				{Text: " no"},
			},
		},
	}
}

// --- help ---

// help lists the key bindings, filtered by what is typed.
type help struct {
	in      input
	entries []keymap.Entry
	shown   []keymap.Entry
	scroll  int
}

func (a *App) openHelp() {
	h := &help{entries: a.km.Entries()}
	h.filter()
	a.ui.overlay = h
}

func (h *help) filter() {
	h.shown = keymap.Filter(h.entries, h.in.String())
	h.scroll = 0
}

func (h *help) key(a *App, k uv.Key) {
	page := max(a.height-8, 1)
	switch keyName(k) {
	case "esc", "ctrl+c", "enter":
		a.closeOverlay()
	case "up", "ctrl+p":
		h.scroll = max(h.scroll-1, 0)
	case "down", "ctrl+n":
		h.scroll++
	case "pgup":
		h.scroll = max(h.scroll-page, 0)
	case "pgdown":
		h.scroll += page
	default:
		if h.in.edit(k) {
			h.filter()
		}
	}
	h.scroll = min(h.scroll, max(len(h.shown)-1, 0))
}

func (h *help) paste(_ *App, text string) { h.in.insert(text); h.filter() }
func (h *help) click(*App, int)           {}

func (h *help) view(a *App) compositor.Overlay {
	muted := uv.Style{Fg: a.theme.Muted}
	accent := uv.Style{Fg: a.theme.Accent, Attrs: uv.AttrBold}
	prefix := keymap.Display(a.km.Prefixes()[0])
	var lines []compositor.Spans
	var mode keymap.Mode
	for _, e := range h.shown {
		if e.Mode != mode {
			mode = e.Mode
			title := strings.ToUpper(string(mode)) + " MODE"
			if mode == keymap.ModePrefix {
				title += " (after " + prefix + ")"
			}
			if len(lines) > 0 {
				lines = append(lines, compositor.Spans{})
			}
			lines = append(lines, compositor.Spans{{Text: title, Style: muted}})
		}
		keys := make([]string, len(e.Keys))
		for i, k := range e.Keys {
			keys[i] = keymap.Display(k)
		}
		lines = append(lines, compositor.Spans{
			{Text: fmt.Sprintf("%-14s", strings.Join(keys, " ")), Style: accent},
			{Text: " " + e.Description},
		})
	}
	if len(lines) == 0 {
		lines = append(lines, compositor.Spans{{Text: "no binding matches", Style: muted}})
	}
	return compositor.Overlay{
		Title: "Keys", Width: min(72, a.width), Height: a.height - 2,
		Input: h.in.view("filter › "), Lines: lines, Selected: -1, Scroll: h.scroll,
		Footer: []compositor.Span{{Text: "type to filter  ↑↓ scroll  esc close"}},
	}
}

// --- toasts ---

// toastTTL is how long a toast stays.
const toastTTL = 4 * time.Second

// maxToasts bounds the toasts on screen; the oldest go first.
const maxToasts = 4

type toast struct {
	text  string
	kind  compositor.ToastKind
	until time.Time
}

type toastExpiredMsg struct{}

// toast shows a short notice.
func (a *App) toast(kind compositor.ToastKind, text string) {
	for i, t := range a.ui.toasts {
		if t.text == text {
			a.ui.toasts = slices.Delete(a.ui.toasts, i, i+1)
			break
		}
	}
	a.ui.toasts = append(a.ui.toasts, toast{text: text, kind: kind, until: a.now().Add(toastTTL)})
	if len(a.ui.toasts) > maxToasts {
		a.ui.toasts = a.ui.toasts[len(a.ui.toasts)-maxToasts:]
	}
	a.dirty = true
	a.after(toastTTL, toastExpiredMsg{})
}

func (a *App) expireToasts() {
	now := a.now()
	a.ui.toasts = slices.DeleteFunc(a.ui.toasts, func(t toast) bool { return !now.Before(t.until) })
}

// --- picker ---

// pickItem is one row of the Goto picker.
type pickItem struct {
	paneID, procID string
	label          string // matched against the query
	detail         string
	active         bool
	failed         bool
}

// pickFilter narrows the picker by agent state.
type pickFilter uint8

const (
	pickAll pickFilter = iota
	pickRunning
	pickExited
)

var pickFilterNames = [...]string{"all", "running", "exited"}

// picker is the fuzzy Goto: panes and agents across environments.
type picker struct {
	in       input
	filter   pickFilter
	items    []pickItem
	shown    []fuzzy.Result // indices into items, ranked
	selected int
}

func (a *App) openPicker() {
	p := &picker{}
	p.reload(a)
	a.ui.overlay = p
}

// reload rebuilds the items from the workspace (it changed while open).
func (p *picker) reload(a *App) {
	s := a.ws.snap
	if s == nil {
		return
	}
	var keep string
	if p.selected < len(p.shown) {
		keep = p.items[p.shown[p.selected].Index].procID
	}
	p.items = p.items[:0]
	seen := map[string]bool{}
	for _, e := range s.envs {
		for _, t := range s.tabsOf(e.ID) {
			for _, pp := range s.panes {
				if pp.TabID != t.ID {
					continue
				}
				seen[pp.ProcessID] = true
				it := pickItem{paneID: pp.ID, procID: pp.ProcessID, label: e.ID + " / " + t.Name + " / " + a.paneTitle(&pp, nil)}
				if pp.Process != nil {
					it.active, it.failed, it.detail = pp.Process.Active(), failedStatus(pp.Process), a.agentStatusText(pp.Process)
				}
				p.items = append(p.items, it)
			}
		}
	}
	for _, pr := range s.procs {
		if seen[pr.ID] {
			continue
		}
		p.items = append(p.items, pickItem{
			procID: pr.ID, label: pr.EnvironmentID + " / " + commandName(&pr),
			detail: a.agentStatusText(&pr) + " · no pane", active: pr.Active(), failed: failedStatus(&pr),
		})
	}
	p.rank()
	for i, r := range p.shown {
		if p.items[r.Index].procID == keep {
			p.selected = i
		}
	}
}

func (p *picker) rank() {
	var labels []string
	var idx []int
	for i, it := range p.items {
		if p.filter == pickRunning && !it.active || p.filter == pickExited && it.active {
			continue
		}
		labels = append(labels, it.label)
		idx = append(idx, i)
	}
	p.shown = fuzzy.Rank(p.in.String(), labels)
	for i := range p.shown {
		p.shown[i].Index = idx[p.shown[i].Index]
	}
	p.selected = min(p.selected, max(len(p.shown)-1, 0))
}

func (p *picker) key(a *App, k uv.Key) {
	switch keyName(k) {
	case "esc", "ctrl+c":
		a.closeOverlay()
	case "enter":
		p.choose(a)
	case "up", "ctrl+p", "ctrl+k":
		p.selected = max(p.selected-1, 0)
	case "down", "ctrl+n", "ctrl+j":
		p.selected = min(p.selected+1, max(len(p.shown)-1, 0))
	case "tab":
		p.filter = (p.filter + 1) % pickFilter(len(pickFilterNames))
		p.selected = 0
		p.rank()
	case "shift+tab":
		p.filter = (p.filter + pickFilter(len(pickFilterNames)) - 1) % pickFilter(len(pickFilterNames))
		p.selected = 0
		p.rank()
	default:
		if p.in.edit(k) {
			p.selected = 0
			p.rank()
		}
	}
}

func (p *picker) choose(a *App) {
	if p.selected >= len(p.shown) {
		return
	}
	it := p.items[p.shown[p.selected].Index]
	a.closeOverlay()
	if it.paneID != "" {
		a.gotoPane(it.paneID)
	} else {
		a.gotoProcess(it.procID)
	}
}

func (p *picker) paste(_ *App, text string) { p.in.insert(text); p.selected = 0; p.rank() }

func (p *picker) click(a *App, line int) {
	if line >= 0 && line < len(p.shown) {
		p.selected = line
		p.choose(a)
	}
}

func (p *picker) view(a *App) compositor.Overlay {
	muted := uv.Style{Fg: a.theme.Muted}
	match := uv.Style{Fg: a.theme.Accent, Attrs: uv.AttrBold}
	var lines []compositor.Spans
	for _, r := range p.shown {
		it := p.items[r.Index]
		glyph, c := "○", a.theme.Muted
		switch {
		case it.active:
			glyph, c = "●", a.theme.Success
		case it.failed:
			glyph, c = "✗", a.theme.Error
		}
		line := compositor.Spans{{Text: glyph + " ", Style: uv.Style{Fg: c}}}
		line = append(line, highlightMatches(it.label, r.Positions, match)...)
		line = append(line, compositor.Span{Text: "  " + it.detail, Style: muted})
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, compositor.Spans{{Text: "nothing matches", Style: muted}})
	}
	var filters []compositor.Span
	for i, n := range pickFilterNames {
		st := muted
		if pickFilter(i) == p.filter {
			st = match
		}
		filters = append(filters, compositor.Span{Text: n + " ", Style: st})
	}
	footer := append([]compositor.Span{{Text: "⏎ go  tab filter: "}}, filters...)
	return compositor.Overlay{
		Title: "Go to", Width: min(80, a.width), Height: min(max(len(lines)+6, 10), a.height-2),
		Input: p.in.view("› "), Lines: lines, Selected: p.selected, Footer: footer,
	}
}

// highlightMatches styles the runes of s at positions.
func highlightMatches(s string, positions []int, st uv.Style) []compositor.Span {
	if len(positions) == 0 {
		return []compositor.Span{{Text: s}}
	}
	var out []compositor.Span
	var cur strings.Builder
	curHit := false
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		sp := compositor.Span{Text: cur.String()}
		if curHit {
			sp.Style = st
		}
		out = append(out, sp)
		cur.Reset()
	}
	pi := 0
	for i, r := range []rune(s) {
		hit := pi < len(positions) && positions[pi] == i
		if hit {
			pi++
		}
		if hit != curHit {
			flush()
			curHit = hit
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}
