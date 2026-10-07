package tui

import (
	"regexp"
	"strings"
)

var (
	// regex to strip non-color ANSI escape sequences (cursor movements, screen clears, etc.)
	// Matches ESC [ ... but NOT ending with 'm' (which are SGR color/style sequences)
	ansiNonColorRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-LN-Za-ln-z]|\x1b\[\?[0-9;]*[a-zA-Z]|\x1b[=>]`)
	// regex for OSC sequences: ESC ] ... (BEL or ESC \)
	ansiOscRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
	// regex to strip all ANSI codes if plain text is needed
	ansiAllRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
)

// SanitizeLogLine cleans a raw line from PTY or process stdout:
// 1. Handles carriage returns (\r) by keeping the text after the last \r
// 2. Strips cursor repositioning and terminal control escape sequences
// 3. Removes non-printable ASCII control characters
// 4. Normalizes tabs to spaces
func SanitizeLogLine(raw string) string {
	if raw == "" {
		return ""
	}

	// If line has carriage return \r, in terminal emulation it overwrites the line.
	// Split by \r and take the last non-empty segment.
	if strings.Contains(raw, "\r") {
		parts := strings.Split(raw, "\r")
		for i := len(parts) - 1; i >= 0; i-- {
			p := strings.TrimSpace(parts[i])
			if p != "" {
				raw = parts[i]
				break
			}
		}
	}

	// Strip OSC title/palette sequences
	raw = ansiOscRe.ReplaceAllString(raw, "")

	// Strip cursor movement sequences (e.g. \x1b[2K, \x1b[1A, \x1b[?25h)
	raw = ansiNonColorRe.ReplaceAllString(raw, "")

	// Convert tabs to 2 spaces
	raw = strings.ReplaceAll(raw, "\t", "  ")

	// Strip raw control characters (except newline and ESC)
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < 32 && c != '\x1b' && c != '\n' {
			continue
		}
		b.WriteByte(c)
	}

	return strings.TrimRight(b.String(), " ")
}

// CleanPlainLine returns a line with ALL ANSI codes stripped, safe for length calculations.
func CleanPlainLine(s string) string {
	return ansiAllRe.ReplaceAllString(s, "")
}
