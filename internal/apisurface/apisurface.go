// Package apisurface checks Hive's public surfaces against their frozen
// copies in api/v1: the wire API, the CLI, the configuration keys and the
// Go SDK. Each surface is a sorted list of lines; a line that disappears
// (a method, a field, a flag, a key, a changed type) breaks callers.
// (The daemon fills a missing parameter with its zero value, so a new
// parameter never breaks an older caller.) Additions are allowed, but the frozen
// copy must be updated (HIVE_UPDATE_SURFACE=1 go test ./... -run Frozen) so
// they are reviewed.
package apisurface

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// UpdateEnv, set to 1, rewrites the frozen copies instead of checking them.
const UpdateEnv = "HIVE_UPDATE_SURFACE"

// Check compares a surface with its frozen copy at path (relative to the
// test's package), failing t on breaking changes and on additions the
// copy lacks.
func Check(t *testing.T, path string, current []string) {
	t.Helper()
	current = sortUnique(current)
	if os.Getenv(UpdateEnv) == "1" {
		if err := os.WriteFile(path, []byte(strings.Join(current, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with -update)", err)
	}
	frozen := sortUnique(strings.Split(strings.TrimSpace(string(data)), "\n"))
	breaking, added := Compare(frozen, current)
	if len(breaking) > 0 {
		t.Errorf("breaking changes to %s (see docs/compatibility.md):\n  %s", path, strings.Join(breaking, "\n  "))
	}
	if len(added) > 0 {
		t.Errorf("%s does not have these additions yet; review them and run HIVE_UPDATE_SURFACE=1 go test ./... -run Frozen:\n  %s", path, strings.Join(added, "\n  "))
	}
}

// Compare returns the frozen lines that are gone or changed (breaking) and
// the current lines that are new.
func Compare(frozen, current []string) (breaking, added []string) {
	have := map[string]bool{}
	for _, l := range current {
		have[l] = true
	}
	was := map[string]bool{}
	for _, l := range frozen {
		was[l] = true
		if !have[l] {
			breaking = append(breaking, "- "+l)
		}
	}
	for _, l := range current {
		if !was[l] {
			added = append(added, "+ "+l)
		}
	}
	return breaking, added
}

func sortUnique(lines []string) []string {
	out := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return strings.TrimSpace(l) == "" })
	sort.Strings(out)
	return slices.Compact(out)
}

// SchemaLines flattens an api.schema document: every method with its
// params and result fields and their types, and every shared type's
// fields.
func SchemaLines(schemaJSON []byte) ([]string, error) {
	var s struct {
		Methods []struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
			Result map[string]any `json:"result"`
			Pipe   bool           `json:"pipe"`
		} `json:"methods"`
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range s.Methods {
		kind := "method"
		if m.Pipe {
			kind = "pipe method"
		}
		out = append(out, kind+" "+m.Name)
		out = flatten(out, m.Name+" params", m.Params)
		out = flatten(out, m.Name+" result", m.Result)
	}
	for _, name := range slices.Sorted(maps.Keys(s.Defs)) {
		out = flatten(out, "type "+name, s.Defs[name])
	}
	return out, nil
}

// flatten writes a schema's type, and for an object its fields, recursively
// (not through references, which are listed as types of their own).
func flatten(out []string, path string, schema map[string]any) []string {
	if schema == nil {
		return out
	}
	out = append(out, path+": "+typeName(schema))
	if props, ok := schema["properties"].(map[string]any); ok {
		for _, name := range slices.Sorted(maps.Keys(props)) {
			if sub, ok := props[name].(map[string]any); ok {
				out = flatten(out, path+"."+name, sub)
			}
		}
	}
	return out
}

func typeName(s map[string]any) string {
	if ref, ok := s["$ref"].(string); ok {
		return strings.TrimPrefix(ref, "#/$defs/")
	}
	t := fmt.Sprint(s["type"])
	if t == "<nil>" {
		t = "any"
	}
	switch t {
	case "array":
		if items, ok := s["items"].(map[string]any); ok {
			return "[]" + typeName(items)
		}
	case "object":
		if add, ok := s["additionalProperties"].(map[string]any); ok {
			return "map[string]" + typeName(add)
		}
	}
	return t
}
