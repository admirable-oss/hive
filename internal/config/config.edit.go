package config

import (
	"bytes"
	"strings"
)

// ResetKeys removes the [keys] table and every [keys.<mode>] table from a
// config document, keeping everything else (comments included) as it was,
// and appends the default, documented [keys] section. found reports whether
// the document had key settings to remove.
func ResetKeys(data []byte) (out []byte, found bool) {
	var b bytes.Buffer
	inKeys := false
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if name, ok := tableHeader(string(line)); ok {
			inKeys = name == "keys" || strings.HasPrefix(name, "keys.")
			found = found || inKeys
		}
		if !inKeys {
			b.Write(line)
		}
	}
	text := strings.TrimRight(b.String(), "\n")
	if text != "" {
		text += "\n\n"
	}
	return []byte(text + RenderKeys(Defaults().Keys)), found
}

// tableHeader returns the name of a [table] or [[array]] header line.
func tableHeader(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if i := strings.Index(s, "#"); i >= 0 && !strings.Contains(s[:i], `"`) {
		s = strings.TrimSpace(s[:i])
	}
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return "", false
	}
	s = strings.TrimSpace(strings.Trim(s, "[]"))
	if s == "" || strings.ContainsAny(s, "=,") {
		return "", false // an array value on its own line, not a header
	}
	// Normalise "keys . prefix" and quoted parts to dotted bare names.
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"'`)
	}
	return strings.Join(parts, "."), true
}
