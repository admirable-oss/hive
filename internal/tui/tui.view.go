package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/admirable-oss/hive/internal/process"
)

// Layout: a top bar, a 2×2 grid of panels inside one box, and a footer.
//
//	┌ bee ──────────┬ process table ─────────┐   fixed height
//	├ telemetry ────┼ log stream ────────────┤   fills the rest
//	└───────────────┴────────────────────────┘
const (
	minWidth, minHeight = 80, 24
	topRowHeight        = 10
	chromeLines         = 5 // top bar + three box borders + footer
)

func (m Model) View() string {
	if m.Quitting {
		return ""
	}
	width, height := max(m.Width, minWidth), max(m.Height, minHeight)
	leftW := 36
	if width > 130 {
		leftW = 38
	}
	rightW := width - leftW - 3 // three vertical borders
	bottomRowHeight := max(height-topRowHeight-chromeLines, 9)

	// The right-hand column turns gold while the user is typing into an agent.
	edge := StyleBorder
	if m.Interactive {
		edge = StyleGoldBold
	}
	hline := func(l, mid, r string) string {
		return StyleBorder.Render(l+strings.Repeat("─", leftW)) + edge.Render(mid+strings.Repeat("─", rightW)+r)
	}

	var b strings.Builder
	b.WriteString(m.renderTopBar(width) + "\n")
	b.WriteString(StyleBorder.Render("┌"+strings.Repeat("─", leftW)+"┬"+strings.Repeat("─", rightW)+"┐") + "\n")
	writeRows(&b, topRowHeight, m.renderBeePanel(leftW, topRowHeight), m.renderProcessTablePanel(rightW, topRowHeight), leftW, rightW, StyleBorder, StyleBorder)
	b.WriteString(hline("├", "┼", "┤") + "\n")
	writeRows(&b, bottomRowHeight, m.renderTelemetryPanel(leftW, bottomRowHeight), m.renderLogsPanel(rightW, bottomRowHeight), leftW, rightW, edge, edge)
	b.WriteString(hline("└", "┴", "┘") + "\n")
	b.WriteString(m.renderFooterBar(width))
	return b.String()
}

// writeRows renders exactly height rows of two panels side by side between
// vertical borders; short panels are padded, long ones cut.
func writeRows(b *strings.Builder, height int, left, right []string, leftW, rightW int, mid, outer lipgloss.Style) {
	for r := range height {
		b.WriteString(StyleBorder.Render("│") + padRight(at(left, r), leftW) +
			mid.Render("│") + padRight(at(right, r), rightW) + outer.Render("│") + "\n")
	}
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func (m Model) renderTopBar(width int) string {
	dots := StyleDotRed.Render("●") + " " + StyleDotYellow.Render("●") + " " + StyleDotGreen.Render("●")
	left := dots + StyleTitle.Render("  hive — "+m.WorkspacePath)

	var running, awaiting, complete int
	for _, p := range m.Processes {
		switch p.Status {
		case process.StatusRunning:
			running++
		case process.StatusStarting:
			awaiting++
		case process.StatusExited:
			complete++
		}
	}
	sep := StyleMuted.Render(" · ")
	right := StyleHeader.Render(fmt.Sprintf("%d RUNNING", running)) + sep +
		StyleHeader.Render(fmt.Sprintf("%d AWAITING", awaiting)) + sep +
		StyleHeader.Render(fmt.Sprintf("%d COMPLETE", complete))

	gap := max(width-ansi.StringWidth(left)-ansi.StringWidth(right)-2, 1)
	return fitWidth(" "+left+strings.Repeat(" ", gap)+right+" ", width)
}

func (m Model) renderBeePanel(width, height int) []string {
	beeLines := strings.Split(m.Bee.View(), "\n")
	res := make([]string, max(0, (height-len(beeLines))/2), height)
	for _, l := range beeLines {
		res = append(res, strings.Repeat(" ", max(1, (width-ansi.StringWidth(l))/2))+l)
	}
	return res
}

func (m Model) renderTelemetryPanel(width, height int) []string {
	cur := m.CurrentProcess()
	agent, uptime, env, pid := "01 · hive", formatElapsed(time.Since(m.StartTime)), "—", "—"
	if cur != nil {
		agent = fmt.Sprintf("%02d · %s", m.SelectedProc+1, cur.DisplayName())
		env = cur.EnvironmentID
		if cur.PID > 0 {
			pid = fmt.Sprint(cur.PID)
		}
		if !cur.StartedAt.IsZero() {
			uptime = formatElapsed(elapsed(*cur))
		}
	}
	field := func(label, value string, style lipgloss.Style) string {
		return "  " + padRight(StyleMuted.Render(label), 7) + " " + style.Render(truncate(value, width-12))
	}

	lines := []string{
		"",
		field("agent", agent, StyleSelectedText),
		field("uptime", uptime, StyleUnselectedText),
		field("env", env, StyleUnselectedText),
		field("pid", pid, StyleUnselectedText),
		"",
		"  " + StyleHeader.Render(fmt.Sprintf("HIVE · %d CELLS", max(len(m.Processes), 5))),
	}

	// A grid of cells, five per row, one per agent.
	rows := 2
	if height > 13 {
		rows = 3
	}
	if height > 20 {
		rows = 4
	}
	for r := range rows {
		cells := make([]string, 5)
		for c := range cells {
			cells[c] = StyleBorder.Render("◇")
			if i := r*5 + c; i < len(m.Processes) {
				switch m.Processes[i].Status {
				case process.StatusRunning:
					cells[c] = StyleGold.Render("◈")
				case process.StatusStarting:
					cells[c] = StylePurple.Render("◈")
				default:
					cells[c] = StyleMuted.Render("◇")
				}
			}
		}
		lines = append(lines, "  "+strings.Join(cells, "  "))
	}
	return lines
}

func (m Model) renderProcessTablePanel(width, height int) []string {
	const colCell, colStatus, colElapsed = 18, 9, 8
	colEnv := max(12, width-colCell-colStatus-colElapsed-6)
	row := func(cell, env, status, elapsed string) string {
		return "  " + padRight(cell, colCell) + " " + padRight(env, colEnv) + " " +
			padRight(status, colStatus) + " " + padLeft(elapsed, colElapsed)
	}

	lines := []string{
		row(StyleHeader.Render("CELL"), StyleHeader.Render("ENV"), StyleHeader.Render("STATUS"), StyleHeader.Render("ELAPSED")),
		"",
	}
	if len(m.Processes) == 0 {
		return append(lines, StyleMuted.Render("  No agents running. Launch one with: hive demo"))
	}

	// Scroll so the selected row is always visible.
	visible := height - len(lines)
	start := max(0, m.SelectedProc-visible+1)
	for i := start; i < len(m.Processes) && i < start+visible; i++ {
		p := m.Processes[i]
		marker, style, name := StyleMuted.Render("◇"), StyleUnselectedText, p.DisplayName()
		if i == m.SelectedProc {
			marker, style = StyleGold.Render("◈"), StyleSelectedText
			if m.Interactive {
				marker, style, name = StyleGoldBold.Render("▶"), StyleGoldBold, name+" [TAKEN]"
			}
		}
		lines = append(lines, row(
			marker+" "+style.Render(truncate(name, colCell-2)),
			StyleMuted.Render(truncate(p.EnvironmentID, colEnv)),
			formatStatusPill(p.Status),
			StyleMuted.Render(formatElapsed(elapsed(p))),
		))
	}
	return lines
}

func (m Model) renderLogsPanel(width, height int) []string {
	cur := m.CurrentProcess()
	title, badge := "ATTACHED · ACTIVITY", StyleMuted.Render("○ IDLE")
	isRunning := cur != nil && cur.Status == process.StatusRunning
	if cur != nil {
		name := strings.ToUpper(cur.DisplayName())
		title = "ATTACHED · " + name
		switch {
		case isRunning && m.Interactive:
			title, badge = "INTERACTIVE · "+name, StyleGoldBold.Render("● INPUT ACTIVE · [ESC] TO DETACH")
		case isRunning:
			badge = StyleGold.Render("● STREAMING LIVE")
		default:
			badge = StyleMuted.Render("○ EXITED")
		}
	}

	header, rule := "  "+StyleHeader.Render(title), StyleBorder
	if m.Interactive {
		header, rule = "  "+StyleGoldBold.Render("✦ "+title), StyleGold
	}
	gap := max(width-ansi.StringWidth(header)-ansi.StringWidth(badge)-2, 2)
	lines := []string{
		header + strings.Repeat(" ", gap) + badge,
		"  " + rule.Render(strings.Repeat("─", max(width-4, 0))),
	}

	var logs []string
	if cur != nil {
		logs = m.logLines[cur.ID]
	}
	switch {
	case len(logs) > 0:
		maxDisplay := max(height-3, 1)
		for _, l := range logs[max(0, len(logs)-maxDisplay):] {
			lines = append(lines, "  "+formatLogLine(l, width-4))
		}
		if isRunning && len(logs) < maxDisplay {
			if m.Interactive {
				lines = append(lines, "  "+StyleGoldBold.Render("› ")+StyleSelectedText.Render("live terminal input active — type here (esc to release)"))
			} else {
				lines = append(lines, "  "+StyleGold.Render("› ")+StyleMuted.Render("press enter to take control…"))
			}
		}
	case isRunning && m.Interactive:
		lines = append(lines, StyleGoldBold.Render("  › interactive terminal active — type commands or responses"))
	case isRunning:
		lines = append(lines,
			StyleMuted.Render("  › listening on agent output stream..."),
			StyleMuted.Render("  › press enter to take control and interact directly"))
	default:
		lines = append(lines, StyleMuted.Render("  › no activity recorded yet"))
	}
	return lines
}

type shortcut struct{ key, desc string }

var (
	interactiveKeys = []shortcut{{"esc", "release control"}, {"keys", "typing into agent terminal"}, {"↵", "submit / enter"}}
	compactKeys     = []shortcut{{"↑↓", "select"}, {"tab", "switch"}, {"↵", "interact"}, {"s", "stop"}, {"q", "detach"}}
	fullKeys        = []shortcut{{"↑↓", "select"}, {"tab", "switch"}, {"↵", "interact"}, {"a", "full attach"}, {"s", "stop"}, {"r", "refresh"}, {"q", "detach — agents keep running"}}
)

func (m Model) renderFooterBar(width int) string {
	keys := fullKeys
	switch {
	case m.Interactive:
		keys = interactiveKeys
	case width < 100:
		keys = compactKeys
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = StyleKeyBadge.Render(k.key) + " " + StyleKeyLabel.Render(k.desc)
	}
	return fitWidth(" "+strings.Join(parts, "  "), width)
}

func formatStatusPill(s process.Status) string {
	switch s {
	case process.StatusRunning:
		return StyleGold.Render("running")
	case process.StatusStarting:
		return StylePurple.Render("awaiting")
	case process.StatusExited:
		return StyleMuted.Render("complete")
	case process.StatusFailed, process.StatusKilled:
		return StyleRed.Render("failed")
	default:
		return StyleMuted.Render(string(s))
	}
}

// formatLogLine colours a sanitised log line by what it looks like: prompts,
// checkmarks, "› action detail" steps, warnings, errors and passes.
func formatLogLine(line string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	line = truncate(line, maxW)
	if strings.Contains(line, "\x1b[") {
		return line // the agent coloured it itself
	}

	first, size := utf8.DecodeRuneInString(line)
	rest := strings.TrimSpace(line[size:])
	switch {
	case first == '?' || first == '❯':
		return StyleGoldBold.Render(string(first)) + " " + StyleSelectedText.Render(rest)
	case first == '✔' || first == '✓':
		return StyleGreen.Render("✔") + " " + StyleSelectedText.Render(rest)
	case first == '›' || first == '>':
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			return StyleGold.Render(line)
		}
		return StyleGold.Render(parts[0]) + " " + StyleSelectedText.Render(parts[1]) + " " + highlightDiffs(parts[2])
	case strings.Contains(line, "WARNING") || strings.Contains(line, "warning"):
		return StyleGold.Render(line)
	case strings.Contains(line, "ERROR") || strings.Contains(line, "error") || strings.Contains(line, "FAIL") || strings.Contains(line, "failed"):
		return StyleRed.Render(line)
	case strings.Contains(line, "PASS") || strings.Contains(line, "passed"):
		return StyleGreen.Render(line)
	}
	return StyleUnselectedText.Render(line)
}

// highlightDiffs colours +N / -N change counts green and red.
func highlightDiffs(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		switch {
		case strings.HasPrefix(w, "+"):
			words[i] = StyleGreen.Render(w)
		case strings.HasPrefix(w, "-"):
			words[i] = StyleRed.Render(w)
		default:
			words[i] = StyleUnselectedText.Render(w)
		}
	}
	return strings.Join(words, " ")
}

func elapsed(p process.Process) time.Duration {
	switch {
	case p.StartedAt.IsZero():
		return 0
	case p.EndedAt != nil:
		return p.EndedAt.Sub(p.StartedAt)
	}
	return time.Since(p.StartedAt)
}

func formatElapsed(d time.Duration) string {
	s := int(max(d, 0).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// truncate shortens s to maxLen cells with an ellipsis; ANSI-aware.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	return ansi.Truncate(s, maxLen, "…")
}

// fitWidth cuts s to width cells without an ellipsis; ANSI-aware.
func fitWidth(s string, width int) string { return ansi.Truncate(s, width, "") }

// padRight fits s into exactly width cells. ANSI-aware, unlike fmt's %-*s.
func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = fitWidth(s, width)
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}

func padLeft(s string, width int) string {
	s = fitWidth(s, width)
	return strings.Repeat(" ", max(width-ansi.StringWidth(s), 0)) + s
}
