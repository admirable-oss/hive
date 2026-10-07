package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/admirable-oss/hive/internal/process"
)

func (m Model) View() string {
	if m.Quitting {
		return ""
	}

	// 100% responsive to terminal dimensions (like Claude Code)
	width := m.Width
	if width < 80 {
		width = 80
	}

	height := m.Height
	if height < 24 {
		height = 24
	}

	leftW := 36
	if width > 130 {
		leftW = 38
	}
	rightW := width - leftW - 3 // accounting for borders: '│' + leftW + '│' + rightW + '│'

	// Dynamic height allocation:
	// Top row (Bee + Process Table): fixed 10 lines
	// Bottom row (Telemetry + Log Stream): expands to fill all remaining terminal height!
	topRowHeight := 10
	bottomRowHeight := height - topRowHeight - 5 // topBar(1) + topBorder(1) + midBorder(1) + botBorder(1) + footer(1) = 5
	if bottomRowHeight < 9 {
		bottomRowHeight = 9
	}

	var b strings.Builder

	// 1. Top Window Bar
	b.WriteString(m.renderTopBar(width))
	b.WriteString("\n")

	// 2. Box Top Border: ┌────────────────────┬────────────────────┐
	topBorder := StyleBorder.Render("┌" + strings.Repeat("─", leftW) + "┬" + strings.Repeat("─", rightW) + "┐")
	b.WriteString(topBorder)
	b.WriteString("\n")

	// 3. Top Row Panels (Bee Mascot on Left | Process Table on Right)
	topLeftLines := m.renderBeePanel(leftW, topRowHeight)
	topRightLines := m.renderProcessTablePanel(rightW, topRowHeight)

	for r := 0; r < topRowHeight; r++ {
		l := ""
		if r < len(topLeftLines) {
			l = topLeftLines[r]
		}
		ri := ""
		if r < len(topRightLines) {
			ri = topRightLines[r]
		}
		row := StyleBorder.Render("│") + padRight(l, leftW) + StyleBorder.Render("│") + padRight(ri, rightW) + StyleBorder.Render("│")
		b.WriteString(row)
		b.WriteString("\n")
	}

	// 4. Middle Divider: ├────────────────────┼────────────────────┤
	var midBorder string
	if m.Interactive {
		midBorder = StyleBorder.Render("├"+strings.Repeat("─", leftW)) + StyleGoldBold.Render("┼"+strings.Repeat("─", rightW)+"┤")
	} else {
		midBorder = StyleBorder.Render("├" + strings.Repeat("─", leftW) + "┼" + strings.Repeat("─", rightW) + "┤")
	}
	b.WriteString(midBorder)
	b.WriteString("\n")

	// 5. Bottom Row Panels (Telemetry on Left | Dominant Live Log Stream on Right)
	bottomLeftLines := m.renderTelemetryPanel(leftW, bottomRowHeight)
	bottomRightLines := m.renderLogsPanel(rightW, bottomRowHeight)

	for r := 0; r < bottomRowHeight; r++ {
		l := ""
		if r < len(bottomLeftLines) {
			l = bottomLeftLines[r]
		}
		ri := ""
		if r < len(bottomRightLines) {
			ri = bottomRightLines[r]
		}
		var row string
		if m.Interactive {
			row = StyleBorder.Render("│") + padRight(l, leftW) + StyleGoldBold.Render("│") + padRight(ri, rightW) + StyleGoldBold.Render("│")
		} else {
			row = StyleBorder.Render("│") + padRight(l, leftW) + StyleBorder.Render("│") + padRight(ri, rightW) + StyleBorder.Render("│")
		}
		b.WriteString(row)
		b.WriteString("\n")
	}

	// 6. Box Bottom Border: └────────────────────┴────────────────────┘
	var botBorder string
	if m.Interactive {
		botBorder = StyleBorder.Render("└"+strings.Repeat("─", leftW)) + StyleGoldBold.Render("┴"+strings.Repeat("─", rightW)+"┘")
	} else {
		botBorder = StyleBorder.Render("└" + strings.Repeat("─", leftW) + "┴" + strings.Repeat("─", rightW) + "┘")
	}
	b.WriteString(botBorder)
	b.WriteString("\n")

	// 7. Footer Status & Shortcuts
	b.WriteString(m.renderFooterBar(width))

	return b.String()
}

func (m Model) renderTopBar(width int) string {
	dots := StyleDotRed.Render("●") + " " + StyleDotYellow.Render("●") + " " + StyleDotGreen.Render("●")
	title := StyleTitle.Render("  hive — " + m.WorkspacePath)
	left := dots + title

	// Count stats
	running := 0
	awaiting := 0
	complete := 0
	for _, p := range m.AllProcesses {
		switch p.Status {
		case process.StatusRunning:
			running++
		case process.StatusStarting:
			awaiting++
		case process.StatusExited:
			complete++
		}
	}

	statRunning := StyleHeader.Render(fmt.Sprintf("%d RUNNING", running))
	statAwaiting := StyleHeader.Render(fmt.Sprintf("%d AWAITING", awaiting))
	statComplete := StyleHeader.Render(fmt.Sprintf("%d COMPLETE", complete))
	sep := StyleMuted.Render(" · ")
	right := statRunning + sep + statAwaiting + sep + statComplete

	leftW := ansi.StringWidth(left)
	rightW := ansi.StringWidth(right)
	gap := width - leftW - rightW - 2
	if gap < 1 {
		gap = 1
	}

	bar := " " + left + strings.Repeat(" ", gap) + right + " "
	if ansi.StringWidth(bar) > width {
		return ansi.Truncate(bar, width, "")
	}
	return bar
}

func (m Model) renderBeePanel(width, height int) []string {
	beeLines := strings.Split(m.Bee.View(), "\n")
	var res []string

	padTop := max(0, (height-len(beeLines))/2)
	for i := 0; i < padTop; i++ {
		res = append(res, "")
	}

	for _, l := range beeLines {
		beeWidth := ansi.StringWidth(l)
		margin := max(1, (width-beeWidth)/2)
		res = append(res, strings.Repeat(" ", margin)+l)
	}

	for len(res) < height {
		res = append(res, "")
	}
	return res
}

func (m Model) renderTelemetryPanel(width, height int) []string {
	var lines []string

	cur := m.CurrentProcess()
	agentName := "01 · hive"
	if cur != nil {
		agentName = fmt.Sprintf("%02d · %s", m.SelectedProc+1, processName(*cur))
	}

	uptimeStr := formatElapsed(time.Since(m.StartTime))
	if cur != nil && !cur.StartedAt.IsZero() {
		uptimeStr = formatElapsed(time.Since(cur.StartedAt))
	}

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %-7s %s", StyleMuted.Render("agent"), StyleSelectedText.Render(truncate(agentName, width-12))))
	lines = append(lines, fmt.Sprintf("  %-7s %s", StyleMuted.Render("uptime"), StyleUnselectedText.Render(uptimeStr)))
	lines = append(lines, fmt.Sprintf("  %-7s %s · %s", StyleMuted.Render("laptop"), StyleMuted.Render("asleep"), StyleGoldBold.Render("still running")))
	lines = append(lines, "")

	totalCells := max(len(m.AllProcesses), 5)
	lines = append(lines, fmt.Sprintf("  %s", StyleHeader.Render(fmt.Sprintf("HIVE · %d CELLS", totalCells))))

	// Render diamond cell grid (up to 3 rows of 5)
	var diamondRows []string
	count := 0
	numRows := 2
	if height > 13 {
		numRows = 3
	}
	if height > 20 {
		numRows = 4
	}
	for r := 0; r < numRows; r++ {
		var rowParts []string
		for c := 0; c < 5; c++ {
			if count < len(m.AllProcesses) {
				p := m.AllProcesses[count]
				switch p.Status {
				case process.StatusRunning:
					rowParts = append(rowParts, StyleGold.Render("◈"))
				case process.StatusStarting:
					rowParts = append(rowParts, StylePurple.Render("◈"))
				default:
					rowParts = append(rowParts, StyleMuted.Render("◇"))
				}
			} else {
				rowParts = append(rowParts, StyleBorder.Render("◇"))
			}
			count++
		}
		diamondRows = append(diamondRows, "  "+strings.Join(rowParts, "  "))
	}
	lines = append(lines, diamondRows...)

	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func (m Model) renderProcessTablePanel(width, height int) []string {
	var lines []string

	// Column widths
	colCell := 18
	colBranch := max(16, width-colCell-14-10-8)
	colStatus := 14
	colElapsed := 8

	hdr := fmt.Sprintf(
		"  %-*s %-*s %-*s %*s",
		colCell, StyleHeader.Render("CELL"),
		colBranch, StyleHeader.Render("BRANCH / ENV"),
		colStatus, StyleHeader.Render("STATUS"),
		colElapsed, StyleHeader.Render("ELAPSED"),
	)
	lines = append(lines, hdr)
	lines = append(lines, "")

	if len(m.AllProcesses) == 0 {
		lines = append(lines, StyleMuted.Render("  No agents running. Launch one with: hive demo"))
	} else {
		// Only render rows that fit within available height-2
		maxRows := height - 2
		for i, p := range m.AllProcesses {
			if i >= maxRows {
				break
			}
			isSelected := i == m.SelectedProc

			diamond := StyleMuted.Render("◇")
			cellStyle := StyleUnselectedText
			if isSelected {
				if m.Interactive {
					diamond = StyleGoldBold.Render("▶")
					cellStyle = StyleGoldBold
				} else {
					diamond = StyleGold.Render("◈")
					cellStyle = StyleSelectedText
				}
			}

			cellName := processName(p)
			if isSelected && m.Interactive {
				cellName = cellName + " [TAKEN]"
			}
			cellDisplay := fmt.Sprintf("%s %s", diamond, cellStyle.Render(truncate(cellName, colCell-3)))

			branchDisplay := fmt.Sprintf("agent/%s", cellName)
			branchDisplay = truncate(branchDisplay, colBranch)

			statusDisplay := formatStatusPill(p.Status)

			elapsedDisplay := "00:00"
			if !p.StartedAt.IsZero() {
				if p.EndedAt != nil {
					elapsedDisplay = formatElapsed(p.EndedAt.Sub(p.StartedAt))
				} else {
					elapsedDisplay = formatElapsed(time.Since(p.StartedAt))
				}
			}

			row := fmt.Sprintf(
				"  %-*s %-*s %-*s %*s",
				colCell, cellDisplay,
				colBranch, StyleMuted.Render(branchDisplay),
				colStatus, statusDisplay,
				colElapsed, StyleMuted.Render(elapsedDisplay),
			)
			lines = append(lines, row)
		}
	}

	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func (m Model) renderLogsPanel(width, height int) []string {
	var lines []string

	cur := m.CurrentProcess()
	headerTitle := "ATTACHED · ACTIVITY"
	statusBadge := StyleMuted.Render("○ IDLE")
	if cur != nil {
		name := strings.ToUpper(processName(*cur))
		headerTitle = fmt.Sprintf("ATTACHED · %s", name)
		if cur.Status == process.StatusRunning {
			if m.Interactive {
				headerTitle = fmt.Sprintf("INTERACTIVE · %s", name)
				statusBadge = StyleGoldBold.Render("● INPUT ACTIVE · [ESC] TO DETACH")
			} else {
				statusBadge = StyleGold.Render("● STREAMING LIVE (stdout)")
			}
		} else {
			statusBadge = StyleMuted.Render("○ EXITED")
		}
	}

	var leftH string
	if m.Interactive {
		leftH = "  " + StyleGoldBold.Render("✦ ") + StyleGoldBold.Render(headerTitle)
	} else {
		leftH = "  " + StyleHeader.Render(headerTitle)
	}
	leftW := ansi.StringWidth(leftH)
	badgeW := ansi.StringWidth(statusBadge)
	gap := width - leftW - badgeW - 2
	if gap < 2 {
		gap = 2
	}
	headerLine := leftH + strings.Repeat(" ", gap) + statusBadge
	lines = append(lines, headerLine)

	// Horizontal separator under log header for console feel
	sepW := width - 4
	if sepW > 0 {
		if m.Interactive {
			lines = append(lines, "  "+StyleGold.Render(strings.Repeat("─", sepW)))
		} else {
			lines = append(lines, "  "+StyleBorder.Render(strings.Repeat("─", sepW)))
		}
	} else {
		lines = append(lines, "")
	}

	logContent := m.ActiveLogs
	if strings.TrimSpace(logContent) == "" {
		if cur != nil && cur.Status == process.StatusRunning {
			if m.Interactive {
				lines = append(lines, StyleGoldBold.Render("  › interactive terminal active — type commands or responses"))
			} else {
				lines = append(lines, StyleMuted.Render("  › listening on agent output stream..."))
				lines = append(lines, StyleMuted.Render("  › press enter to take control and interact directly"))
			}
		} else {
			lines = append(lines, StyleMuted.Render("  › no activity recorded yet"))
		}
	} else {
		rawLines := strings.Split(logContent, "\n")
		var clean []string
		for _, l := range rawLines {
			sanitized := SanitizeLogLine(l)
			if sanitized != "" {
				clean = append(clean, sanitized)
			}
		}

		maxDisplay := height - 3
		if maxDisplay < 1 {
			maxDisplay = 1
		}
		start := 0
		if len(clean) > maxDisplay {
			start = len(clean) - maxDisplay
		}
		for i := start; i < len(clean); i++ {
			formatted := formatLogLine(clean[i], width-4)
			lines = append(lines, "  "+formatted)
		}

		// If fewer lines than available height, show active indicator
		if cur != nil && cur.Status == process.StatusRunning && len(clean) < maxDisplay && len(lines) < height-1 {
			if m.Interactive {
				lines = append(lines, "  "+StyleGoldBold.Render("› ")+StyleSelectedText.Render("live terminal input active — type here (esc to release)"))
			} else {
				lines = append(lines, "  "+StyleGold.Render("› ")+StyleMuted.Render("press enter to take control…"))
			}
		}
	}

	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func (m Model) renderFooterBar(width int) string {
	if m.Interactive {
		var parts []string
		parts = append(parts, fmt.Sprintf("%s %s", StyleKeyBadge.Render("esc"), StyleKeyLabel.Render("release control")))
		parts = append(parts, fmt.Sprintf("%s %s", StyleKeyBadge.Render("keys"), StyleKeyLabel.Render("typing into agent terminal")))
		parts = append(parts, fmt.Sprintf("%s %s", StyleKeyBadge.Render("↵"), StyleKeyLabel.Render("submit / enter")))
		res := " " + strings.Join(parts, "  ")
		if ansi.StringWidth(res) > width {
			return ansi.Truncate(res, width, "")
		}
		return res
	}

	shortcutsShort := []struct {
		key  string
		desc string
	}{
		{"↑↓", "select"},
		{"tab", "switch"},
		{"↵", "interact"},
		{"s", "stop"},
		{"q", "detach"},
	}

	shortcutsFull := []struct {
		key  string
		desc string
	}{
		{"↑↓", "select"},
		{"tab", "switch"},
		{"↵", "interact"},
		{"a", "full attach"},
		{"s", "stop"},
		{"r", "refresh"},
		{"q", "detach — agents keep running"},
	}

	shortcuts := shortcutsFull
	if width < 100 {
		shortcuts = shortcutsShort
	}

	var parts []string
	for _, sc := range shortcuts {
		parts = append(parts, fmt.Sprintf("%s %s", StyleKeyBadge.Render(sc.key), StyleKeyLabel.Render(sc.desc)))
	}
	res := " " + strings.Join(parts, "  ")
	if ansi.StringWidth(res) > width {
		return ansi.Truncate(res, width, "")
	}
	return res
}

func formatStatusPill(s process.Status) string {
	switch s {
	case process.StatusRunning:
		return StyleStatusRunning.Render("running")
	case process.StatusStarting:
		return StyleStatusAwaiting.Render("awaiting")
	case process.StatusExited:
		return StyleStatusComplete.Render("complete")
	case process.StatusFailed, process.StatusKilled:
		return StyleStatusFailed.Render("failed")
	default:
		return StyleStatusComplete.Render(string(s))
	}
}

func formatLogLine(line string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if ansi.StringWidth(line) > maxW {
		line = ansi.Truncate(line, maxW, "…")
	}

	// Preserve native SGR styling from Claude Code or rich terminal tools
	if strings.Contains(line, "\x1b[") {
		return line
	}

	// Interactive agent prompts like ? or ❯
	if strings.HasPrefix(line, "?") || strings.HasPrefix(line, "❯") {
		first := line[:1]
		rest := strings.TrimSpace(line[1:])
		return StyleGoldBold.Render(first) + " " + StyleSelectedText.Render(rest)
	}

	// Success checkmarks
	if strings.HasPrefix(line, "✔") || strings.HasPrefix(line, "✓") {
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "✔"), "✓"))
		return StyleGreen.Render("✔") + " " + StyleSelectedText.Render(rest)
	}

	// Custom step styling for agent prompts (› action file +diff -diff)
	if strings.HasPrefix(line, "›") || strings.HasPrefix(line, ">") {
		parts := strings.SplitN(line, " ", 3)
		if len(parts) >= 3 {
			prompt := StyleGold.Render(parts[0])
			action := StyleSelectedText.Render(parts[1])
			detail := parts[2]
			// Color diff markers (+9 -3) if present
			if strings.Contains(detail, "+") || strings.Contains(detail, "-") {
				detail = highlightDiffs(detail)
			} else {
				detail = StyleLogFile.Render(detail)
			}
			return prompt + " " + action + " " + detail
		}
		return StyleGold.Render(line)
	}

	// Warning highlighting
	if strings.Contains(line, "WARNING") || strings.Contains(line, "warning") {
		return StyleGold.Render(line)
	}
	// Error highlighting
	if strings.Contains(line, "ERROR") || strings.Contains(line, "error") || strings.Contains(line, "FAIL") || strings.Contains(line, "failed") {
		return StyleStatusFailed.Render(line)
	}
	// Success highlighting
	if strings.Contains(line, "PASS") || strings.Contains(line, "passed") {
		return StyleGreen.Render(line)
	}

	return StyleUnselectedText.Render(line)
}

func highlightDiffs(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if strings.HasPrefix(w, "+") {
			words[i] = StyleGreen.Render(w)
		} else if strings.HasPrefix(w, "-") {
			words[i] = StyleStatusFailed.Render(w)
		} else {
			words[i] = StyleLogFile.Render(w)
		}
	}
	return strings.Join(words, " ")
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSec := int(d.Seconds())
	m := totalSec / 60
	s := totalSec % 60
	if m >= 60 {
		h := m / 60
		m = m % 60
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > maxLen {
		return ansi.Truncate(s, maxLen, "…")
	}
	return s
}

func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w > width {
		s = ansi.Truncate(s, width, "")
		w = ansi.StringWidth(s)
	}
	if w < width {
		s += strings.Repeat(" ", width-w)
	}
	return s
}

func processName(p process.Process) string {
	name := p.Command
	for _, arg := range p.Args {
		if !strings.HasPrefix(arg, "-") && !strings.Contains(arg, ";") && !strings.Contains(arg, "\n") && len(arg) < 30 {
			name = arg
			break
		}
	}
	return name
}
