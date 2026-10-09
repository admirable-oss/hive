package mux

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/vt"
)

// regions divides the screen; the Overview hides the sidebar.
func (a *App) regions() compositor.Regions {
	sw := 0
	if a.ui.sidebar && a.ui.overview == nil {
		sw = a.ui.sidebarW
	}
	return compositor.Plan(a.width, a.height, sw)
}

// sidebarFull reports whether the sidebar fills the screen (a narrow
// screen): it then takes the keys, as a menu.
func (a *App) sidebarFull() bool {
	r := a.regions()
	return !r.Sidebar.Empty() && r.Panes.Empty()
}

// scene describes the frame. The returned func unlocks the pane screens
// it refers to; call it after drawing.
func (a *App) scene() (*compositor.Scene, func()) {
	unlock := a.lockViews()
	s := &compositor.Scene{Width: a.width, Height: a.height, Theme: a.theme, Regions: a.regions()}
	s.TabBar = a.tabBar()
	if !s.Regions.Sidebar.Empty() {
		s.Sidebar = a.sidebar()
	}
	if a.ui.overview != nil {
		// The Overview covers the screen: no panes under it.
		s.Overlays = append(s.Overlays, a.ui.overview.view(a))
	} else {
		a.addPanes(s)
	}
	if a.ui.overlay != nil {
		s.Overlays = append(s.Overlays, a.ui.overlay.view(a))
	}
	for _, t := range a.ui.toasts {
		s.Toasts = append(s.Toasts, compositor.Toast{Text: t.text, Kind: t.kind})
	}
	return s, unlock
}

// --- tab bar ---

func (a *App) tabBar() compositor.TabBar {
	var tb compositor.TabBar
	if e := a.env(); e != nil {
		tb.Left = append(tb.Left, compositor.Span{Text: e.ID})
		if g := gitSummary(e.Git); g != "" {
			tb.Left = append(tb.Left, compositor.Span{Text: " " + g, Style: uv.Style{Fg: a.theme.Muted}})
		}
	}
	if s := a.ws.snap; s != nil {
		active := a.tab()
		for i, t := range s.tabsOf(a.ws.envID) {
			tb.Tabs = append(tb.Tabs, compositor.Tab{
				ID:     t.ID,
				Label:  fmt.Sprintf("%d %s", i+1, t.Name),
				Active: active != nil && t.ID == active.ID,
			})
		}
	}
	tb.Right = a.modeIndicator()
	return tb
}

// modeIndicator is the right end of the tab bar: the input mode, or a hint.
func (a *App) modeIndicator() []compositor.Span {
	badge := func(text string, c color.Color) []compositor.Span {
		return []compositor.Span{{Text: " " + text + " ", Style: uv.Style{Fg: a.theme.Bg, Bg: c, Attrs: uv.AttrBold}}}
	}
	if a.theme.Bg == nil {
		badge = func(text string, _ color.Color) []compositor.Span {
			return []compositor.Span{{Text: " " + text + " ", Style: uv.Style{Attrs: uv.AttrBold | uv.AttrReverse}}}
		}
	}
	var out []compositor.Span
	if !a.ws.live && a.ws.snap != nil {
		out = append(out, compositor.Span{Text: "offline ", Style: uv.Style{Fg: a.theme.Error}})
	}
	switch {
	case a.ui.overview != nil:
		return append(out, badge("OVERVIEW", a.theme.Info)...)
	case a.ui.mode == keymap.ModePrefix:
		return append(out, badge("PREFIX", a.theme.Accent)...)
	case a.ui.mode == keymap.ModeNavigate:
		return append(out, badge("NAVIGATE", a.theme.Info)...)
	case a.ui.mode == keymap.ModeResize:
		return append(out, badge("RESIZE", a.theme.Warning)...)
	case a.ui.mode == keymap.ModeCopy:
		return append(out, badge("COPY", a.theme.Warning)...) // the pane's badge has the position
	}
	if keys := a.km.Keys(keymap.ModePrefix, keymap.Help); len(keys) > 0 {
		hint := keymap.Display(a.km.Prefixes()[0]) + " " + keymap.Display(keys[0]) + " help"
		out = append(out, compositor.Span{Text: hint, Style: uv.Style{Fg: a.theme.Muted}})
	}
	return out
}

// gitSummary is "main ↑1↓2 *": branch, ahead/behind, and a mark when the
// tree has changes ("!" for conflicts).
func gitSummary(g *git.Status) string {
	if g == nil {
		return ""
	}
	b := g.Branch
	if g.Detached || b == "" {
		b = "@" + g.Head
	}
	if g.Ahead > 0 {
		b += fmt.Sprintf(" ↑%d", g.Ahead)
	}
	if g.Behind > 0 {
		b += fmt.Sprintf(" ↓%d", g.Behind)
	}
	switch {
	case g.Conflicts > 0:
		b += " !"
	case g.Staged+g.Modified+g.Untracked > 0:
		b += " *"
	}
	return b
}

// --- sidebar ---

// sidebar panels; their IDs are the header rows' hit IDs.
const (
	panelEnvs   = "environments"
	panelAgents = "agents"
)

func (a *App) sidebar() *compositor.Sidebar {
	rows := a.sidebarRows()
	if a.sidebarFull() {
		sel := a.selectableRows(rows)
		if len(sel) > 0 {
			a.ui.sidebarAt = min(max(a.ui.sidebarAt, 0), len(sel)-1)
			for i := range rows {
				rows[i].Selected = false
			}
			rows[sel[a.ui.sidebarAt]].Selected = true
		}
	}
	return &compositor.Sidebar{Rows: rows}
}

func (a *App) selectableRows(rows []compositor.SidebarRow) []int {
	var out []int
	for i, r := range rows {
		if r.ID != "" && !r.Header {
			out = append(out, i)
		}
	}
	return out
}

func (a *App) sidebarRows() []compositor.SidebarRow {
	s := a.ws.snap
	if s == nil {
		return nil
	}
	muted := uv.Style{Fg: a.theme.Muted}
	header := func(id, title string, n int) compositor.SidebarRow {
		arrow := "▾ "
		if a.ui.collapsed[id] {
			arrow = "▸ "
		}
		return compositor.SidebarRow{
			ID: id, Kind: compositor.HitSidebarHeader, Header: true,
			Spans: []compositor.Span{{Text: arrow + title}, {Text: fmt.Sprintf(" %d", n), Style: muted}},
		}
	}

	var rows []compositor.SidebarRow
	rows = append(rows, header(panelEnvs, "Environments", len(s.envs)))
	if !a.ui.collapsed[panelEnvs] {
		for _, e := range s.envs {
			glyph, c := a.envGlyph(s, e)
			spans := []compositor.Span{{Text: glyph + " ", Style: uv.Style{Fg: c}}, {Text: e.ID}}
			if g := gitSummary(e.Git); g != "" {
				spans = append(spans, compositor.Span{Text: "  " + g, Style: muted})
			}
			rows = append(rows, compositor.SidebarRow{ID: e.ID, Kind: compositor.HitEnvironment, Selected: e.ID == a.ws.envID, Spans: spans})
		}
	}

	agents := a.envAgents()
	rows = append(rows, compositor.SidebarRow{}, header(panelAgents, "Agents", len(agents)))
	if !a.ui.collapsed[panelAgents] {
		focused := ""
		if p := a.focusedPane(); p != nil {
			focused = p.ProcessID
		}
		for _, p := range agents {
			glyph, c := a.statusGlyph(&p)
			rows = append(rows, compositor.SidebarRow{
				ID: p.ID, Kind: compositor.HitAgent, Selected: p.ID == focused,
				Spans: []compositor.Span{{Text: glyph + " ", Style: uv.Style{Fg: c}}, {Text: a.agentName(&p)}},
			})
		}
		if len(agents) == 0 {
			rows = append(rows, compositor.SidebarRow{Spans: []compositor.Span{{Text: "  none yet", Style: muted}}})
		}
	}
	return rows
}

// envAgents are the current environment's agents: panes in tab order,
// then agents without a pane.
func (a *App) envAgents() []process.Process {
	s := a.ws.snap
	var out []process.Process
	seen := map[string]bool{}
	for _, t := range s.tabsOf(a.ws.envID) {
		for _, p := range s.panes {
			if p.TabID == t.ID && p.Process != nil && !seen[p.ProcessID] {
				seen[p.ProcessID] = true
				out = append(out, *p.Process)
			}
		}
	}
	for _, p := range s.procs {
		if p.EnvironmentID == a.ws.envID && !seen[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

// envGlyph marks an environment by its agents: running, failed, idle.
func (a *App) envGlyph(s *snapshot, e environment.Environment) (string, color.Color) {
	running, failed := 0, 0
	for _, p := range s.procs {
		if p.EnvironmentID != e.ID {
			continue
		}
		switch {
		case p.Active():
			running++
		case failedStatus(&p):
			failed++
		}
	}
	switch {
	case failed > 0 && running == 0:
		return "●", a.theme.Error
	case running > 0:
		return "●", a.theme.Success
	}
	return "○", a.theme.Muted
}

func failedStatus(p *process.Process) bool {
	return p.Status == process.StatusFailed || p.Status == process.StatusKilled || (p.ExitCode != nil && *p.ExitCode != 0)
}

// statusGlyph is an agent's dot and colour.
func (a *App) statusGlyph(p *process.Process) (string, color.Color) {
	switch {
	case p.Status == process.StatusStarting:
		return "◐", a.theme.Info
	case p.Active():
		return "●", a.theme.Success
	case failedStatus(p):
		return "✗", a.theme.Error
	}
	return "○", a.theme.Muted
}

// statusText describes an agent's state: "running", "exited 1".
func statusText(p *process.Process) string {
	if p.Active() || p.ExitCode == nil {
		return string(p.Status)
	}
	return fmt.Sprintf("%s %d", p.Status, *p.ExitCode)
}

// agentName is what the UI calls an agent: its pane's name, else its
// command.
func (a *App) agentName(p *process.Process) string {
	if pp := a.ws.snap.paneOfProcess(p.ID); pp != nil && pp.Name != "" {
		return pp.Name
	}
	return commandName(p)
}

// commandName is an agent's display name without a command's directory
// ("/bin/zsh" is "zsh").
func commandName(p *process.Process) string {
	n := p.DisplayName()
	if strings.HasPrefix(n, "/") {
		return filepath.Base(n)
	}
	return n
}

// --- panes ---

func (a *App) addPanes(s *compositor.Scene) {
	snap := a.ws.snap
	switch {
	case snap == nil && a.ws.err != nil:
		s.Empty = "Cannot reach the hive daemon\n" + errText(a.ws.err) + "\nretrying…"
		return
	case snap == nil:
		s.Empty = "Connecting to the hive daemon…"
		return
	case a.env() == nil:
		s.Empty = "No environments yet\n\n" + a.keyHint(keymap.NewTab) + " opens a tab in " + displayDir(a.opts.Cwd)
		return
	}
	t := a.tab()
	if t == nil {
		s.Empty = "No tabs in " + a.ws.envID + "\n\n" + a.keyHint(keymap.NewTab) + " opens one"
		return
	}
	geo := a.geometry()
	popups := map[string]bool{}
	for _, pp := range t.Popups {
		popups[pp.Pane] = true
	}
	for _, id := range a.drawOrder(t) {
		r, ok := geo[id]
		p := snap.pane(id)
		if !ok || p == nil {
			continue
		}
		cp := a.drawPane(t, p, r.W, r.H)
		cp.Rect = r
		if popups[id] {
			s.Popups = append(s.Popups, cp)
		} else {
			s.Panes = append(s.Panes, cp)
		}
	}
}

// drawOrder lists the tab's tiled panes in layout order, then popups.
func (a *App) drawOrder(t *pane.Tab) []string {
	var ids []string
	if t.Layout != nil {
		ids = t.Layout.Panes()
	}
	for _, pp := range t.Popups {
		ids = append(ids, pp.Pane)
	}
	return ids
}

// drawPane describes one pane for the compositor. The caller holds
// lockViews.
func (a *App) drawPane(t *pane.Tab, p *pane.Pane, w, h int) compositor.Pane {
	cp := compositor.Pane{ID: p.ID, Focused: p.ID == t.Focused}
	proc := p.Process
	scr, _ := a.screenOf(p.ProcessID)
	if scr != nil {
		cp.Lines = scr.Lines
		// An open box has the keys: the pane's cursor would mislead.
		if cp.Focused && a.ui.overlay == nil {
			cur := scr.Cursor
			cp.Cursor = &cur
		}
	}
	cp.Title = a.paneTitle(p, scr)
	if t.Zoomed == p.ID {
		cp.Badges = append(cp.Badges, compositor.Span{Text: "[zoom]", Style: uv.Style{Fg: a.theme.Accent}})
	}
	if c := a.ui.copy; c != nil && c.paneID == p.ID {
		if c.c == nil {
			cp.Placeholder = "loading history…"
			cp.Lines = nil
		} else {
			c.c.Resize(w, h)
			cp.Lines, cp.Highlights, _ = c.c.View()
			cp.Cursor = nil
			if !c.mouse {
				line, total := c.c.Position()
				cp.Badges = append(cp.Badges, compositor.Span{Text: fmt.Sprintf("[copy %d/%d]", line, total), Style: uv.Style{Fg: a.theme.Warning}})
			}
		}
	}
	switch {
	case proc == nil:
		cp.Placeholder = "no process"
	case !proc.Active():
		cp.Dim = true
		style := uv.Style{Fg: a.theme.Muted}
		if failedStatus(proc) {
			style.Fg = a.theme.Error
		}
		cp.Badges = append(cp.Badges, compositor.Span{Text: "[" + statusText(proc) + "]", Style: style})
		if cp.Lines == nil {
			cp.Placeholder = "the process " + statusText(proc) + "\n\n" + a.keyHint(keymap.ClosePane) + " closes the pane"
		}
	case !proc.Terminal:
		cp.Placeholder = "not a terminal agent"
	case cp.Lines == nil && cp.Placeholder == "":
		cp.Placeholder = "starting…"
	}
	return cp
}

// paneTitle is the pane's name, else the title its program set, else its
// command.
func (a *App) paneTitle(p *pane.Pane, scr *vt.Screen) string {
	switch {
	case p.Name != "":
		return p.Name
	case scr != nil && strings.TrimSpace(scr.Title) != "":
		return strings.TrimSpace(scr.Title)
	case p.Process != nil:
		return commandName(p.Process)
	}
	return p.ID
}

// keyHint is how to press an action from terminal mode: "C-b c".
func (a *App) keyHint(act keymap.Action) string {
	if keys := a.km.Keys(keymap.ModeTerminal, act); len(keys) > 0 {
		return keymap.Display(keys[0])
	}
	if keys := a.km.Keys(keymap.ModePrefix, act); len(keys) > 0 {
		return keymap.Display(a.km.Prefixes()[0]) + " " + keymap.Display(keys[0])
	}
	return string(act)
}

// WindowTitle is the outer terminal's title.
func (a *App) WindowTitle() string {
	parts := []string{"hive"}
	if e := a.env(); e != nil {
		parts = append(parts, e.ID)
	}
	if t := a.tab(); t != nil {
		parts = append(parts, t.Name)
	}
	return strings.Join(parts, " · ")
}

// WantsHover reports whether the focused pane's program asked for every
// mouse movement, which the outer terminal then has to report.
func (a *App) WantsHover() bool {
	p := a.focusedPane()
	if p == nil {
		return false
	}
	v := a.views[p.ProcessID]
	return v != nil && v.modes()&vt.ModeMouseAny != 0
}

// displayDir shortens a path for messages.
func displayDir(dir string) string {
	if dir == "" {
		return "the current directory"
	}
	if home := homeDir(); home != "" && (dir == home || strings.HasPrefix(dir, home+"/")) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}
