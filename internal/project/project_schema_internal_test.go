package project

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestSchemaCoversTheFormat: every key the parser accepts is in the JSON
// Schema, and the schema has no key the parser refuses.
func TestSchemaCoversTheFormat(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema []byte
		file   any
	}{
		{"hive.toml", Schema(false), fileProject{}},
		{"hive.local.toml", Schema(true), fileLocal{}},
	} {
		var root map[string]any
		if err := json.Unmarshal(tc.schema, &root); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		defs, _ := root["$defs"].(map[string]any)
		compare(t, tc.name, root, defs, reflect.TypeOf(tc.file))
	}
}

func compare(t *testing.T, where string, schema, defs map[string]any, typ reflect.Type) {
	t.Helper()
	if ref, ok := schema["$ref"].(string); ok {
		schema = defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
	}
	for typ.Kind() == reflect.Slice || typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
		if items, ok := schema["items"].(map[string]any); ok {
			schema = items
			if ref, ok := schema["$ref"].(string); ok {
				schema = defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
			}
		}
	}
	if typ.Kind() == reflect.Map && typ.Elem().Kind() == reflect.Struct {
		if ap, ok := schema["additionalProperties"].(map[string]any); ok {
			compare(t, where+".*", ap, defs, typ.Elem())
			return
		}
		t.Errorf("%s: a table of tables, not in the schema", where)
		return
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	props, _ := schema["properties"].(map[string]any)
	var keys []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		key := f.Tag.Get("toml")
		keys = append(keys, key)
		p, ok := props[key].(map[string]any)
		if !ok {
			t.Errorf("%s.%s: accepted by the parser, missing from the schema", where, key)
			continue
		}
		compare(t, where+"."+key, p, defs, f.Type)
	}
	for k := range props {
		if !slices.Contains(keys, k) {
			t.Errorf("%s.%s: in the schema, refused by the parser", where, k)
		}
	}
	if schema["additionalProperties"] != false {
		t.Errorf("%s: the schema allows other keys, which the parser refuses", where)
	}
}
