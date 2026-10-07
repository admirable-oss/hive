package tui

import (
	"regexp"
	"strings"
)

// maxLogLines bounds the sanitised lines kept per process.
const maxLogLines = 200

var (
	// Non-SGR CSI sequences (cursor moves, clears, …). SGR colour codes end in
	// 'm' and are kept so agents' own colours survive.
	ansiNonColorRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-LN-Za-ln-z]|\x1b\[\?[0-9;]*[a-zA-Z]|\x1b[=>]`)
	// OSC sequences: ESC ] … terminated by BEL or ESC \.
	ansiOscRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
	spacesRe  = regexp.MustCompile(` {2,}`)
)

// sanitizeLog turns raw PTY/stdout output into displayable lines. It runs once
// per fetch, not once per frame.
func sanitizeLog(raw string) []string {
	var lines []string
	for line := range strings.SplitSeq(raw, "\n") {
		if clean := SanitizeLogLine(line); clean != "" {
			lines = append(lines, clean)
		}
	}
	if len(lines) > maxLogLines {
		lines = lines[len(lines)-maxLogLines:]
	}
	return lines
}

// SanitizeLogLine flattens one raw output line for the log panel: carriage
// return overwrites keep only the final text, cursor-movement escapes become
// spaces (so TUI agents like Claude Code don't glue words together), and
// other control bytes are dropped. Colour codes are preserved.
func SanitizeLogLine(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "\r") {
		parts := strings.Split(raw, "\r")
		for i := len(parts) - 1; i >= 0; i-- {
			if strings.TrimSpace(parts[i]) != "" {
				raw = parts[i]
				break
			}
		}
	}

	raw = ansiOscRe.ReplaceAllString(raw, "")
	raw = ansiNonColorRe.ReplaceAllString(raw, " ")
	raw = strings.ReplaceAll(raw, "\t", "  ")

	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c >= 32 || c == '\x1b' {
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(spacesRe.ReplaceAllString(b.String(), " "))
}
