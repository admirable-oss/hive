package apisurface

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Markdown is the wire API reference, generated from an
// api.schema document.
func Markdown(schemaJSON []byte) (string, error) {
	var s struct {
		APILevel int `json:"api_level"`
		Methods  []struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
			Result map[string]any `json:"result"`
			Pipe   bool           `json:"pipe"`
		} `json:"methods"`
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, `# API reference

<!-- Generated from the daemon's api.schema; do not edit by hand. Rewrite it
with: HIVE_UPDATE_SURFACE=1 go test ./internal/runtime/tests -run Frozen -->

API level %d. Clients speak newline-delimited JSON over the daemon's unix
socket (`+"`hive api schema`"+` prints this as JSON Schema; `+"`pkg/hiveapi`"+` is the Go SDK;
the README's "Wire protocol" section has the framing). A protocol-2 client
opens with `+"`"+`{"type":"hello","version":"2","params":{"client":"my-tool"}}`+"`"+`, then
sends requests such as
`+"`"+`{"version":"2","type":"request","id":"1","method":"agent.list","params":{"all":true}}`+"`"+`,
each answered by a `+"`response`"+` with the same `+"`id`"+` and a `+"`result`"+` or an `+"`error`"+`
(`+"`code`"+`, `+"`message`"+`). Pipe methods answer, then carry a byte stream on the
connection. Every name below is stable within 1.x (the compatibility policy).
`, s.APILevel)

	groups := map[string][]int{}
	for i, m := range s.Methods {
		ns, _, _ := strings.Cut(m.Name, ".")
		groups[ns] = append(groups[ns], i)
	}
	for _, ns := range slices.Sorted(maps.Keys(groups)) {
		fmt.Fprintf(&b, "\n## %s\n", ns)
		for _, i := range groups[ns] {
			m := s.Methods[i]
			fmt.Fprintf(&b, "\n### `%s`", m.Name)
			if m.Pipe {
				b.WriteString(" (pipe)")
			}
			b.WriteString("\n\n")
			params := m.Params
			if ref, ok := params["$ref"].(string); ok {
				params = s.Defs[strings.TrimPrefix(ref, "#/$defs/")]
			}
			if props, ok := params["properties"].(map[string]any); ok && len(props) > 0 {
				b.WriteString("| Param | Type | |\n|---|---|---|\n")
				for _, name := range slices.Sorted(maps.Keys(props)) {
					p, _ := props[name].(map[string]any)
					fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, typeLink(p), cell(p["description"]))
				}
				b.WriteString("\n")
			} else {
				b.WriteString("No params.\n\n")
			}
			if m.Result != nil {
				fmt.Fprintf(&b, "Result: %s\n", typeLink(m.Result))
			}
		}
	}

	b.WriteString("\n## Types\n")
	for _, name := range slices.Sorted(maps.Keys(s.Defs)) {
		d := s.Defs[name]
		fmt.Fprintf(&b, "\n### %s\n\n", name)
		props, _ := d["properties"].(map[string]any)
		if len(props) == 0 {
			b.WriteString("An object.\n")
			continue
		}
		b.WriteString("| Field | Type | |\n|---|---|---|\n")
		for _, f := range slices.Sorted(maps.Keys(props)) {
			p, _ := props[f].(map[string]any)
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", f, typeLink(p), cell(p["description"]))
		}
	}
	return b.String(), nil
}

// typeLink names a schema's type, linking shared types to their section.
func typeLink(s map[string]any) string {
	t := typeName(s)
	base := strings.TrimLeft(strings.TrimPrefix(t, "map[string]"), "[]")
	if strings.Contains(base, ".") {
		anchor := strings.ToLower(strings.ReplaceAll(base, ".", ""))
		return "[" + strings.ReplaceAll(t, "[]", `\[\]`) + "](#" + anchor + ")"
	}
	return "`" + t + "`"
}

func cell(v any) string {
	if v == nil {
		return ""
	}
	return strings.ReplaceAll(fmt.Sprint(v), "|", `\|`)
}
