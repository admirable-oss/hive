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
func SanitizeLogLine(raw string) string {
	if raw == "" {
		return ""
	}

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

	raw = ansiOscRe.ReplaceAllString(raw, "")

	// Replace cursor movement sequences with a space so words don't concatenate
	raw = ansiNonColorRe.ReplaceAllString(raw, " ")

	raw = strings.ReplaceAll(raw, "\t", "  ")

	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < 32 && c != '\x1b' && c != '\n' {
			continue
		}
		b.WriteByte(c)
	}

	// collapse multiple spaces into one to avoid weird gaps, but don't do it if we want to preserve layout?
	// actually, for Claude Code, it emits a lot of escapes, so collapsing spaces is safer for readability.
	res := b.String()
	res = regexp.MustCompile(` {2,}`).ReplaceAllString(res, " ")
	return strings.TrimSpace(res)
}

func CleanPlainLine(s string) string {
	return ansiAllRe.ReplaceAllString(s, "")
}
