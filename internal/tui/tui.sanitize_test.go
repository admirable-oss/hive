package tui_test

import (
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/tui"
)

func TestSanitizeLogLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain line",
			input:    "› read src/auth · 214 files",
			expected: "› read src/auth · 214 files",
		},
		{
			name:     "carriage return overwrite",
			input:    "progress 10%\rprogress 50%\rprogress 100%",
			expected: "progress 100%",
		},
		{
			name:     "claude cursor show and carriage return",
			input:    "\x1b[?25h\rClaude Code will read files",
			expected: "Claude Code will read files",
		},
		{
			name:     "clear line escape sequence",
			input:    "\x1b[2K\r› plan rotate tokens",
			expected: "› plan rotate tokens",
		},
		{
			name:     "preserves SGR color codes",
			input:    "\x1b[32m✔ done\x1b[0m",
			expected: "\x1b[32m✔ done\x1b[0m",
		},
		{
			name:     "claude cursor positioning between words does not concatenate",
			input:    "Accessing\x1b[12Gworkspace:\x1b[24Gclean",
			expected: "Accessing workspace: clean",
		},
		{
			name:     "claude safety prompt spacing",
			input:    "Quick\x1b[6Gsafety\x1b[13Gcheck:\x1b[20GIs this a project?",
			expected: "Quick safety check: Is this a project?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tui.SanitizeLogLine(tt.input)
			if !strings.Contains(got, tt.expected) {
				t.Errorf("SanitizeLogLine(%q) = %q, expected to contain %q", tt.input, got, tt.expected)
			}
		})
	}
}
