package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/admirable-oss/hive/internal/process"
)

func (m Model) View() string {
	if m.Quitting {
		return ""
	}

	var b strings.Builder

	// Header: Bee + Title text
	header := m.renderHeader()
	b.WriteString(header)
	b.WriteString("\n")

	// Divider
	divider := StyleBorder.Render(strings.Repeat("─", max(40, m.Width)))
	b.WriteString(divider)
	b.WriteString("\n")

	// Main content area
	if m.Inspecting != nil {
		b.WriteString(m.renderProcessDetail())
	} else if len(m.Environments) == 0 {
		b.WriteString(m.renderEmptyState())
	} else {
		b.WriteString(m.renderMainColumns())
	}

	b.WriteString("\n")
	b.WriteString(divider)
	b.WriteString("\n")

	// Status & shortcut bar
	b.WriteString(m.renderFooter())

	return b.String()
}

func (m Model) renderHeader() string {
	beeArt := m.Bee.View()

	tagline := lipgloss.JoinVertical(
		lipgloss.Left,
		"",
		StyleTitle.Render("HIVE 0.0.1"),
		StyleSubtitle.Render("Run them anywhere."),
		StyleMuted.Render("Leave them running."),
	)

	return lipgloss.JoinHorizontal(lipgloss.Top, beeArt, "   ", tagline)
}

func (m Model) renderMainColumns() string {
	leftWidth := 26
	rightWidth := max(30, m.Width-leftWidth-5)

	// Column titles
	envHeader := StyleHeader.Render(" ENVIRONMENTS")
	procHeader := StyleHeader.Render(" PROCESSES")

	sep := StyleBorder.Render("│")

	var envLines []string
	for i, env := range m.Environments {
		prefix := "  "
		style := StyleUnselectedText
		if i == m.SelectedEnv {
			if m.Focus == FocusEnvironments {
				prefix = StyleIndicator.Render("› ")
			} else {
				prefix = StyleMuted.Render("› ")
			}
			style = StyleSelectedText
		}
		name := style.Render(truncate(env.ID, leftWidth-4))
		envLines = append(envLines, prefix+name)
	}

	currentProcs := m.currentProcesses()
	var procLines []string
	if len(currentProcs) == 0 {
		procLines = append(procLines, StyleMuted.Render("  no processes running"))
	} else {
		for i, p := range currentProcs {
			prefix := "  "
			nameStyle := StyleUnselectedText
			if i == m.SelectedProc && m.Focus == FocusProcesses {
				prefix = StyleIndicator.Render("› ")
				nameStyle = StyleSelectedText
			} else if i == m.SelectedProc {
				prefix = StyleMuted.Render("› ")
			}

			cmdName := p.Command
			if len(p.Args) > 0 {
				cmdName += " " + strings.Join(p.Args, " ")
			}
			cmdName = truncate(cmdName, 22)

			statusText := formatStatus(p.Status)

			line := fmt.Sprintf("%s%-22s  %s", prefix, nameStyle.Render(cmdName), statusText)
			procLines = append(procLines, line)
		}
	}

	maxRows := max(len(envLines), len(procLines))
	maxRows = max(maxRows, 5)

	var rows []string
	rows = append(rows, fmt.Sprintf("%-26s %s %s", envHeader, sep, procHeader))

	for r := 0; r < maxRows; r++ {
		e := ""
		if r < len(envLines) {
			e = envLines[r]
		}
		p := ""
		if r < len(procLines) {
			p = procLines[r]
		}
		rows = append(rows, fmt.Sprintf("%-26s %s %s", padRight(e, leftWidth), sep, padRight(p, rightWidth)))
	}

	return strings.Join(rows, "\n")
}

func (m Model) renderEmptyState() string {
	return lipgloss.JoinVertical(
		lipgloss.Center,
		"",
		StyleSubtitle.Render("The hive is empty."),
		StyleMuted.Render("Create an environment with:"),
		StyleAccent.Render("hive environment create <name>"),
		"",
	)
}

func (m Model) renderProcessDetail() string {
	p := m.Inspecting
	if p == nil {
		return ""
	}

	var rows []string
	rows = append(rows, StyleTitle.Render("PROCESS DETAIL"), "")
	rows = append(rows, fmt.Sprintf("  %-14s %s", StyleMuted.Render("ID"), StyleSelectedText.Render(p.ID)))
	rows = append(rows, fmt.Sprintf("  %-14s %d", StyleMuted.Render("PID"), p.PID))
	rows = append(rows, fmt.Sprintf("  %-14s %s", StyleMuted.Render("Status"), formatStatus(p.Status)))
	rows = append(rows, fmt.Sprintf("  %-14s %s", StyleMuted.Render("Environment"), p.EnvironmentID))
	rows = append(rows, fmt.Sprintf("  %-14s %v", StyleMuted.Render("Terminal"), p.Terminal))

	cmdStr := p.Command
	if len(p.Args) > 0 {
		cmdStr += " " + strings.Join(p.Args, " ")
	}
	rows = append(rows, fmt.Sprintf("  %-14s %s", StyleMuted.Render("Command"), cmdStr))
	rows = append(rows, fmt.Sprintf("  %-14s %s", StyleMuted.Render("Working Dir"), p.WorkingDir))

	if p.ExitCode != nil {
		rows = append(rows, fmt.Sprintf("  %-14s %d", StyleMuted.Render("Exit Code"), *p.ExitCode))
	}

	return strings.Join(rows, "\n")
}

func (m Model) renderFooter() string {
	// Left side: Connection status + counts
	var connStatus string
	if m.Connected {
		totalProcs := 0
		for _, ps := range m.Processes {
			totalProcs += len(ps)
		}
		connStatus = fmt.Sprintf(
			"%s connected    %s",
			StyleConnected.Render("●"),
			StyleMuted.Render(fmt.Sprintf("%d environments · %d processes", len(m.Environments), totalProcs)),
		)
	} else {
		connStatus = fmt.Sprintf(
			"%s disconnected (runtime not reachable)",
			StyleDisconnected.Render("○"),
		)
	}

	// Right side: Contextual navigation hints
	var hints string
	if m.Inspecting != nil {
		if m.Inspecting.Status == process.StatusRunning {
			hints = "a attach   s stop   esc back   q quit"
		} else {
			hints = "esc back   q quit"
		}
	} else if m.Focus == FocusProcesses {
		procs := m.currentProcesses()
		if len(procs) > 0 && m.SelectedProc < len(procs) && procs[m.SelectedProc].Status == process.StatusRunning {
			hints = "↑↓ navigate   enter inspect   a attach   s stop   esc back   q quit"
		} else {
			hints = "↑↓ navigate   enter inspect   esc back   r refresh   q quit"
		}
	} else {
		hints = "↑↓ navigate   tab processes   r refresh   q quit"
	}

	return fmt.Sprintf(" %s\n %s", connStatus, StyleMuted.Render(hints))
}

func formatStatus(s process.Status) string {
	switch s {
	case process.StatusRunning:
		return StyleStatusRunning.Render("● running")
	case process.StatusExited:
		return StyleStatusExited.Render("○ exited")
	case process.StatusFailed:
		return StyleStatusFailed.Render("× failed")
	case process.StatusKilled:
		return StyleStatusFailed.Render("× killed")
	case process.StatusStarting:
		return StyleStatusRunning.Render("◌ starting")
	default:
		return StyleStatusExited.Render("◇ " + string(s))
	}
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
