package mux

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/bee"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/copymode"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/vt"
)

// previewLogLines is how much output the Overview shows for agents
// without a terminal.
const previewLogLines = 200

// overview is the dashboard: every agent of every environment, with a live
// preview of the selected one, under the bee. Enter goes to the agent's
// pane.
type overview struct {
	rows  []string // process IDs, by environment then start time
	selID string

	logsFor string // the agent logs were read for
	logs    [][]vt.Cell

	bee     bee.Model
	ticking bool
}

// beeTickMsg advances the bee's animation while the Overview is open.
type beeTickMsg struct{}

// minBeeHeight is the shortest screen that still has room for the bee.
const minBeeHeight = 24

type previewLogsMsg struct {
	procID string
	text   string
}

func (a *App) toggleOverview() {
	if a.ui.overview != nil {
		a.closeOverview()
		return
	}
	sel := ""
	if p := a.focusedPane(); p != nil {
		sel = p.ProcessID
	}
	a.openOverview(sel)
}

func (a *App) openOverview(sel string) {
	a.exitCopy()
	a.ui.mode = keymap.ModeTerminal
	a.ui.overview = &overview{selID: sel, bee: bee.New()}
	a.ui.overview.sync(a)
	a.ui.overview.animate(a)
	a.syncViews()
}

// animate keeps the bee's wings moving: one timer at a time, and none once
// the Overview closes.
func (o *overview) animate(a *App) {
	if o.ticking {
		return
	}
	o.ticking = true
	a.after(o.bee.FrameDelay(), beeTickMsg{})
}

func (a *App) beeTick() {
	o := a.ui.overview
	if o == nil {
		return
	}
	o.ticking = false
	state := bee.StateIdle
	switch {
	case a.ws.err != nil || !a.ws.live:
		state = bee.StateDisconnected
	case a.ws.snap != nil && slices.ContainsFunc(a.ws.snap.procs, func(p process.Process) bool { return p.Status == process.StatusRunning }):
		state = bee.StateActive
	}
	o.bee.SetState(state)
	o.bee.Tick()
	o.animate(a)
}

// header is how many lines precede the agent rows: the bee (when there is
// room) and the column titles.
func (o *overview) header(a *App) int {
	if a.height >= minBeeHeight {
		return bee.Height + 2
	}
	return 1
}

func (a *App) closeOverview() {
	a.ui.overview = nil
	a.seen()
	a.afterResize()
}

// sync rebuilds the rows from the workspace, keeping the selection.
func (o *overview) sync(a *App) {
	s := a.ws.snap
	o.rows = o.rows[:0]
	if s == nil {
		return
	}
	for _, e := range s.envs {
		for _, p := range s.procs {
			if p.EnvironmentID == e.ID {
				o.rows = append(o.rows, p.ID)
			}
		}
	}
	for _, p := range s.procs {
		if s.env(p.EnvironmentID) == nil {
			o.rows = append(o.rows, p.ID) // an agent of a removed environment
		}
	}
	if o.index() < 0 {
		o.selID = ""
		if len(o.rows) > 0 {
			o.selID = o.rows[0]
		}
	}
	o.fetchLogs(a)
}

func (o *overview) index() int {
	for i, id := range o.rows {
		if id == o.selID {
			return i
		}
	}
	return -1
}

func (o *overview) move(a *App, i int) {
	if len(o.rows) == 0 {
		return
	}
	o.selID = o.rows[min(max(i, 0), len(o.rows)-1)]
	o.fetchLogs(a)
	a.syncViews()
}

// fetchLogs reads the output of a selected agent that has no terminal to
// stream.
func (o *overview) fetchLogs(a *App) {
	p := a.ws.snap.process(o.selID)
	if p == nil || streamable(p) || o.logsFor == p.ID {
		return
	}
	if p.Terminal {
		if _, ok := a.cache[p.ID]; ok {
			return // its last screen is still here
		}
	}
	o.logsFor, o.logs = p.ID, nil
	id, c := p.ID, a.c
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		res, err := c.ProcessLogs(ctx, process.LogsRequest{ID: id, Tail: previewLogLines})
		if err != nil {
			return previewLogsMsg{procID: id, text: "(no output: " + errText(err) + ")"}
		}
		return previewLogsMsg{procID: id, text: res.Logs}
	})
}

func (o *overview) setLogs(m previewLogsMsg) {
	if m.procID != o.logsFor {
		return
	}
	text := strings.TrimRight(sanitizeLog(m.text), "\n")
	o.logs = copymode.FromText(strings.Split(text, "\n"))
}

// sanitizeLog drops escape sequences and control characters from raw
// output, which would otherwise draw as garbage.
func sanitizeLog(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b:
			// Skip a CSI or OSC sequence, or a two-byte escape.
			j := i + 1
			switch {
			case j < len(s) && s[j] == '[':
				for j++; j < len(s) && (s[j] < 0x40 || s[j] > 0x7e); j++ {
				}
			case j < len(s) && s[j] == ']':
				for j++; j < len(s) && s[j] != 0x07 && (s[j] != 0x1b || j+1 >= len(s) || s[j+1] != '\\'); j++ {
				}
				if j < len(s) && s[j] == 0x1b {
					j++
				}
			}
			i = j
		case c == '\n' || c == '\t' || c >= 0x20 && c != 0x7f:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func (o *overview) key(a *App, k uv.Key) {
	i := o.index()
	switch keyName(k) {
	case "esc", "q":
		a.closeOverview()
	case "up", "k":
		o.move(a, i-1)
	case "down", "j":
		o.move(a, i+1)
	case "home", "g":
		o.move(a, 0)
	case "end", "G":
		o.move(a, len(o.rows)-1)
	case "pgup":
		o.move(a, i-max(a.height-6, 1))
	case "pgdown":
		o.move(a, i+max(a.height-6, 1))
	case "enter", "l", "right":
		o.choose(a)
	case "x":
		o.stop(a)
	case "/", "w":
		a.openPicker()
	}
}

func (o *overview) click(a *App, line int) {
	row := line - o.header(a)
	if row < 0 || row >= len(o.rows) {
		return
	}
	if o.rows[row] == o.selID {
		o.choose(a)
		return
	}
	o.move(a, row)
}

// choose goes to the selected agent's pane.
func (o *overview) choose(a *App) {
	if a.ws.snap.paneOfProcess(o.selID) == nil {
		a.toast(compositor.ToastInfo, "this agent has no pane")
		return
	}
	id := o.selID
	a.ui.overview = nil
	a.gotoProcess(id)
	a.afterResize()
}

// stop asks, then stops the selected agent.
func (o *overview) stop(a *App) {
	p := a.ws.snap.process(o.selID)
	if p == nil || !p.Active() {
		return
	}
	id, c := p.ID, a.c
	a.openConfirm("Stop agent", "Stop "+a.agentName(p)+"?", func() {
		a.call("stop", func(ctx context.Context) error { return c.ProcessStop(ctx, id) }, nil)
	})
}

// view is the Overview box. The caller holds lockViews.
func (o *overview) view(a *App) compositor.Overlay {
	s := a.ws.snap
	muted := uv.Style{Fg: a.theme.Muted}
	nameW, envW := 22, 14
	running, ended := 0, 0
	var lines []compositor.Spans
	if o.header(a) > 1 {
		for _, l := range o.bee.Lines() {
			lines = append(lines, append(compositor.Spans{{Text: "  "}}, l...))
		}
		lines = append(lines, compositor.Spans{})
	}
	lines = append(lines, compositor.Spans{{Text: fmt.Sprintf("  %-*s %-*s %s", nameW, "AGENT", envW, "ENVIRONMENT", "STATUS"), Style: muted}})
	for _, id := range o.rows {
		p := s.process(id)
		if p == nil {
			continue
		}
		if p.Active() {
			running++
		} else {
			ended++
		}
		glyph, c := a.statusGlyph(p)
		lines = append(lines, compositor.Spans{
			{Text: glyph + " ", Style: uv.Style{Fg: c}},
			{Text: fmt.Sprintf("%-*s ", nameW, compositor.Truncate(a.agentName(p), nameW))},
			{Text: fmt.Sprintf("%-*s ", envW, compositor.Truncate(p.EnvironmentID, envW)), Style: muted},
			{Text: statusText(p) + " " + age(p, a.now()), Style: uv.Style{Fg: c}},
		})
	}
	if len(o.rows) == 0 {
		lines = append(lines, compositor.Spans{{Text: "  no agents yet", Style: muted}})
	}
	title := fmt.Sprintf("Overview · %d running · %d ended", running, ended)
	ov := compositor.Overlay{
		Title: title, Width: a.width, Height: a.height,
		Lines: lines, Selected: o.index() + o.header(a), ListWidth: nameW + envW + 20,
		Footer: []compositor.Span{{Text: "↑↓ select  ⏎ go to pane  x stop  / find  esc close"}},
	}
	if o.index() < 0 {
		ov.Selected = -1
	}
	if p := s.process(o.selID); p != nil {
		ov.Side = o.preview(a, p, a.height-3)
	}
	return ov
}

// preview is the selected agent's screen (its last rows when taller than
// the box) or output.
func (o *overview) preview(a *App, p *process.Process, h int) *compositor.Pane {
	pv := &compositor.Pane{ID: p.ID, Title: a.agentName(p), Dim: !p.Active()}
	scr, _ := a.screenOf(p.ID)
	switch {
	case scr != nil:
		lines := scr.Lines
		if len(lines) > h && h > 0 {
			end := min(max(scr.Cursor.Y+1, h), len(lines))
			lines = lines[end-h : end]
		}
		pv.Lines = lines
	case o.logsFor == p.ID && o.logs != nil:
		lines := o.logs
		if len(lines) > h && h > 0 {
			lines = lines[len(lines)-h:]
		}
		pv.Lines = lines
	case streamable(p):
		pv.Placeholder = "connecting…"
	default:
		pv.Placeholder = "no output"
	}
	return pv
}

// age is how long an agent has run, or how long ago it ended.
func age(p *process.Process, now time.Time) string {
	if p.Active() || p.EndedAt == nil {
		if p.StartedAt.IsZero() {
			return ""
		}
		return shortDuration(now.Sub(p.StartedAt))
	}
	return shortDuration(now.Sub(*p.EndedAt)) + " ago"
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
