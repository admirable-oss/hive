package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/admirable-oss/hive/internal/process"
)

func (m Model) View() string {
	if m.Quitting {
		return ""
	}

	width := max(84, m.Width)
	if width > 130 {
		width = 130
	}

	leftW := 30
	rightW := width - leftW - 3 // accounting for borders

	var b strings.Builder

	// 1. Top Window Bar
	b.WriteString(m.renderTopBar(width))
	b.WriteString("\n")

	// 2. Box Top Border: ┌────────────────────┬────────────────────┐
	topBorder := StyleBorder.Render("┌" + strings.Repeat("─", leftW) + "┬" + strings.Repeat("─", rightW) + "┐")
	b.WriteString(topBorder)
	b.WriteString("\n")

	// 3. Top Row Panels (Bee Mascot on Left | Process Table on Right)
	topRowHeight := 9
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
	midBorder := StyleBorder.Render("├" + strings.Repeat("─", leftW) + "┼" + strings.Repeat("─", rightW) + "┤")
	b.WriteString(midBorder)
	b.WriteString("\n")

	// 5. Bottom Row Panels (Telemetry on Left | Live Attached Logs on Right)
	bottomRowHeight := 9
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
		row := StyleBorder.Render("│") + padRight(l, leftW) + StyleBorder.Render("│") + padRight(ri, rightW) + StyleBorder.Render("│")
		b.WriteString(row)
		b.WriteString("\n")
	}

	// 6. Box Bottom Border: └────────────────────┴────────────────────┘
	botBorder := StyleBorder.Render("└" + strings.Repeat("─", leftW) + "┴" + strings.Repeat("─", rightW) + "┘")
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

	gap := max(2, width-lipgloss.Width(left)-lipgloss.Width(right))
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (m Model) renderBeePanel(width, height int) []string {
	beeLines := strings.Split(m.Bee.View(), "\n")
	var res []string

	padTop := max(0, (height-len(beeLines))/2)
	for i := 0; i < padTop; i++ {
		res = append(res, "")
	}

	for _, l := range beeLines {
		beeWidth := lipgloss.Width(l)
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
	lines = append(lines, fmt.Sprintf("  %-9s %s", StyleMuted.Render("agent"), StyleSelectedText.Render(truncate(agentName, width-14))))
	lines = append(lines, fmt.Sprintf("  %-9s %s", StyleMuted.Render("uptime"), StyleUnselectedText.Render(uptimeStr)))
	lines = append(lines, fmt.Sprintf("  %-9s %s · %s", StyleMuted.Render("laptop"), StyleMuted.Render("asleep"), StyleGoldBold.Render("still running")))
	lines = append(lines, "")

	totalCells := max(len(m.AllProcesses), 5)
	lines = append(lines, fmt.Sprintf("  %s", StyleHeader.Render(fmt.Sprintf("HIVE · %d CELLS", totalCells))))

	// Render diamond cell grid (up to 3 rows of 5)
	var diamondRows []string
	count := 0
	for r := 0; r < 2; r++ {
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
	colCell := 16
	colBranch := max(16, width-16-12-10-4)
	colStatus := 12
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
		lines = append(lines, StyleMuted.Render("  No agents running. Start an agent with: hive process start <env> <cmd>"))
	} else {
		for i, p := range m.AllProcesses {
			isSelected := i == m.SelectedProc

			diamond := StyleMuted.Render("◇")
			cellStyle := StyleUnselectedText
			if isSelected {
				diamond = StyleGold.Render("◈")
				cellStyle = StyleSelectedText
			}

			cellName := processName(p)
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
	if cur != nil {
		headerTitle = fmt.Sprintf("ATTACHED · %s", strings.ToUpper(processName(*cur)))
	}

	lines = append(lines, "  "+StyleHeader.Render(headerTitle))
	lines = append(lines, "")

	logContent := m.ActiveLogs
	if logContent == "" {
		if cur != nil && cur.Status == process.StatusRunning {
			lines = append(lines, StyleMuted.Render("  › awaiting agent output..."))
		} else {
			lines = append(lines, StyleMuted.Render("  › no activity recorded yet"))
		}
	} else {
		rawLines := strings.Split(logContent, "\n")
		// Clean and tail the latest lines that fit inside height-2
		var clean []string
		for _, l := range rawLines {
			trimmed := strings.TrimRight(l, "\r ")
			if trimmed != "" {
				clean = append(clean, trimmed)
			}
		}

		maxDisplay := height - 3
		start := 0
		if len(clean) > maxDisplay {
			start = len(clean) - maxDisplay
		}
		for i := start; i < len(clean); i++ {
			formatted := formatLogLine(clean[i], width-4)
			lines = append(lines, "  "+formatted)
		}
	}

	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func (m Model) renderFooterBar(width int) string {
	shortcuts := []struct {
		key  string
		desc string
	}{
		{"↑↓", "select"},
		{"tab", "switch"},
		{"↵", "attach"},
		{"s", "stop"},
		{"r", "refresh"},
		{"q", "detach — agents keep running"},
	}

	var parts []string
	for _, sc := range shortcuts {
		parts = append(parts, fmt.Sprintf("%s %s", StyleKeyBadge.Render(sc.key), StyleKeyLabel.Render(sc.desc)))
	}
	return " " + strings.Join(parts, "  ")
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
	line = truncate(line, maxW)
	if strings.HasPrefix(line, "›") || strings.HasPrefix(line, ">") {
		parts := strings.SplitN(line, " ", 3)
		if len(parts) >= 3 {
			return StyleLogPrompt.Render(parts[0]) + " " + StyleLogAction.Render(parts[1]) + " " + StyleLogFile.Render(parts[2])
		}
		return StyleLogPrompt.Render(line)
	}
	return StyleUnselectedText.Render(line)
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
	if len(s) > maxLen && maxLen > 3 {
		return s[:maxLen-3] + "..."
	}
	return s
}

func padRight(s string, width int) string {
	visLen := lipgloss.Width(s)
	if visLen >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visLen)
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
