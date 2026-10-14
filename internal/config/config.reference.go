package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Reference is the generated configuration reference: every key with its default and what it does,
// generated from the documented default file (Render), so the reference
// and `hive config default` never disagree.
func Reference() string {
	var b strings.Builder
	b.WriteString(`# Configuration reference

<!-- Generated from internal/config (Render); do not edit by hand. Rewrite it
with: HIVE_UPDATE_SURFACE=1 go test ./internal/config -run Reference -->

Hive reads ` + "`~/.config/hive/config.toml`" + ` (or ` + "`$XDG_CONFIG_HOME/hive/config.toml`" + `, or
` + "`$HIVE_CONFIG`" + `). Every key is optional; ` + "`hive config default`" + ` prints this
whole file with its defaults, and ` + "`hive config init`" + ` writes it. Unknown keys and
invalid values are reported as warnings (` + "`hive config validate`" + `) and fall back to
the defaults, so a typo never stops the daemon.
`)
	var (
		section  string
		comments []string
		lastDesc string
		rows     []string
	)
	flushRows := func() {
		if len(rows) > 0 {
			b.WriteString("\n| Key | Default | Description |\n|---|---|---|\n")
			b.WriteString(strings.Join(rows, ""))
			rows = nil
		}
	}
	flushNotes := func() {
		if len(comments) == 0 {
			return
		}
		flushRows()
		var prose, example []string
		for _, c := range comments {
			if len(example) > 0 || tomlLine.MatchString(c) {
				example = append(example, c)
			} else {
				prose = append(prose, c)
			}
		}
		if len(prose) > 0 {
			b.WriteString("\n" + strings.Join(prose, " ") + "\n")
		}
		if len(example) > 0 {
			b.WriteString("\n```toml\n" + strings.Join(example, "\n") + "\n```\n")
		}
		comments = nil
	}
	for _, line := range strings.Split(string(Render(Defaults())), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case section == "" && strings.HasPrefix(trimmed, "#"):
			continue // the file's own header
		case trimmed == "":
			if len(comments) > 0 && len(rows) > 0 {
				flushNotes()
			}
		case strings.HasPrefix(trimmed, "#"):
			comments = append(comments, strings.TrimSpace(strings.TrimPrefix(trimmed, "#")))
		case strings.HasPrefix(trimmed, "["):
			flushNotes()
			flushRows()
			section = strings.Trim(trimmed, "[]")
			fmt.Fprintf(&b, "\n## `[%s]`\n", section)
			lastDesc = ""
		default:
			key, value, ok := strings.Cut(trimmed, " = ")
			if !ok {
				continue
			}
			desc := lastDesc
			if len(comments) > 0 {
				desc = strings.Join(comments, " ")
				comments = nil
			}
			lastDesc = desc
			rows = append(rows, fmt.Sprintf("| %#q | %#q | %s |\n", key, strings.ReplaceAll(value, "|", `\|`), strings.ReplaceAll(desc, "|", `\|`)))
		}
	}
	flushNotes()
	flushRows()
	return b.String()
}

// tomlLine recognises a commented-out example line.
var tomlLine = regexp.MustCompile(`^(\[\[?[a-z_.]+\]\]?|[a-z_]+ = .*)$`)
