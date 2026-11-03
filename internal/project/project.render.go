package project

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Render writes a project file in the canonical form: the keys in the
// documented order, one table per tab, pane, agent and environment.
// Comments are not kept; what parses from the result is what f says.
func Render(f fileProject) []byte {
	var b strings.Builder
	w := &tomlWriter{b: &b}
	w.kv("version", f.Version)
	w.kv("name", f.Name)
	w.kv("toolchain", f.Toolchain)
	w.kv("workflows", f.Workflows)
	w.kv("plugins", f.Plugins)
	w.table("vars", f.Vars)
	w.table("secrets", f.Secrets)
	w.env("env", f.Env)
	for _, g := range f.Gate {
		w.header("[[gate]]")
		w.kv("name", g.Name)
		w.kv("command", g.Command)
		w.kv("timeout", g.Timeout)
	}
	w.tabs("tab", f.Tab)
	w.agents("agent", f.Agent)
	for _, e := range f.Environment {
		w.header("[[environment]]")
		w.kv("id", e.ID)
		w.kv("path", e.Path)
		w.kv("toolchain", e.Toolchain)
		w.table("environment.vars", e.Vars)
		w.table("environment.secrets", e.Secrets)
		w.env("environment.env", e.Env)
		w.tabs("environment.tab", e.Tab)
		w.agents("environment.agent", e.Agent)
	}
	return []byte(b.String())
}

type tomlWriter struct {
	b *strings.Builder
}

func (w *tomlWriter) header(h string) {
	if w.b.Len() > 0 {
		w.b.WriteByte('\n')
	}
	w.b.WriteString(h)
	w.b.WriteString("\n")
}

func (w *tomlWriter) kv(key string, v any) {
	s, ok := tomlValue(v)
	if ok {
		fmt.Fprintf(w.b, "%s = %s\n", key, s)
	}
}

func (w *tomlWriter) table(name string, m map[string]string) {
	if len(m) == 0 {
		return
	}
	w.header("[" + name + "]")
	for _, k := range slices.Sorted(maps.Keys(m)) {
		// An empty value is a value here: the variable is set, to "".
		fmt.Fprintf(w.b, "%s = %s\n", k, tomlString(m[k]))
	}
}

func (w *tomlWriter) env(name string, e fileEnv) {
	if len(e.Required) == 0 && len(e.Optional) == 0 {
		return
	}
	w.header("[" + name + "]")
	w.kv("required", e.Required)
	w.kv("optional", e.Optional)
}

func (w *tomlWriter) tabs(name string, tabs []fileTab) {
	for _, t := range tabs {
		w.header("[[" + name + "]]")
		w.kv("name", t.Name)
		w.kv("split", t.Split)
		for _, p := range t.Pane {
			w.header("[[" + name + ".pane]]")
			w.kv("name", p.Name)
			w.kv("command", p.Command)
			w.kv("cwd", p.Cwd)
		}
	}
}

func (w *tomlWriter) agents(name string, agents []fileAgent) {
	for _, a := range agents {
		w.header("[[" + name + "]]")
		w.kv("name", a.Name)
		w.kv("kind", a.Kind)
		w.kv("isolate", a.Isolate)
		w.kv("worktree", a.Worktree)
		w.kv("base", a.Base)
		w.kv("args", a.Args)
	}
}

// tomlValue renders a value; empty ones are left out.
func tomlValue(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case int:
		return strconv.Itoa(x), x != 0
	case bool:
		return "true", x
	case string:
		return tomlString(x), x != ""
	case []string:
		if len(x) == 0 {
			return "", false
		}
		parts := make([]string, len(x))
		for i, s := range x {
			parts[i] = tomlString(s)
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			s, _ := tomlValue(e)
			if _, isString := e.(string); isString {
				s = tomlString(e.(string))
			}
			parts = append(parts, s)
		}
		if len(parts) == 0 {
			return "", false
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	}
	return fmt.Sprint(v), true
}

// tomlString is a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f || r == utf8.RuneError:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
