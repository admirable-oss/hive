package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// Schema describes the API: every method, with JSON Schemas of its params
// and result. Types shared between methods are in Defs, referred to as
// {"$ref": "#/$defs/<package>.<Type>"}.
type Schema struct {
	APILevel int            `json:"api_level"`
	Methods  []MethodSchema `json:"methods"`
	Defs     map[string]any `json:"$defs"`
}

// MethodSchema is one method. Pipe methods answer, then carry a byte
// stream on the same connection (attach, logs, events).
type MethodSchema struct {
	Name   string `json:"name"`
	Params any    `json:"params,omitempty"`
	Result any    `json:"result,omitempty"`
	Pipe   bool   `json:"pipe,omitempty"`
}

// Schema describes every registered method. Methods registered with a
// plain Handler have no params or result schema.
func (r *Router) Schema() Schema {
	g := SchemaGenerator{Defs: map[string]any{}}
	s := Schema{APILevel: APILevel, Defs: g.Defs}
	for _, name := range r.Methods() {
		m := MethodSchema{Name: name}
		if t, ok := r.handlers[name].(typedHandler); ok {
			m.Params, m.Result, m.Pipe = g.Of(t.params), g.Of(t.result), t.pipe
		}
		s.Methods = append(s.Methods, m)
	}
	return s
}

// SchemaGenerator writes JSON Schemas of Go types as encoding/json encodes
// them. Named structs go into Defs once, so recursive types work. A
// field's `desc:"…"` tag becomes its description.
type SchemaGenerator struct {
	Defs map[string]any
	// Inline writes named structs in place instead of in Defs (for
	// consumers without $ref support); recursion then stops at {}.
	Inline bool
	seen   map[reflect.Type]bool
}

var (
	timeType      = reflect.TypeFor[time.Time]()
	durationType  = reflect.TypeFor[time.Duration]()
	rawType       = reflect.TypeFor[json.RawMessage]()
	marshalerType = reflect.TypeFor[json.Marshaler]()
)

// Of returns t's schema.
func (g *SchemaGenerator) Of(t reflect.Type) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case durationType:
		return map[string]any{"type": "integer", "description": "nanoseconds"}
	case rawType:
		return map[string]any{}
	}
	if t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) {
		if t.Kind() != reflect.String {
			return map[string]any{} // its own encoding
		}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": g.Of(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.Of(t.Elem())}
	case reflect.Struct:
		return g.structRef(t)
	}
	return map[string]any{}
}

func (g *SchemaGenerator) structRef(t reflect.Type) any {
	if t.Name() == "" {
		return g.structOf(t)
	}
	name := defName(t)
	if g.Inline {
		if g.seen[t] {
			return map[string]any{"type": "object"} // recursion
		}
		if g.seen == nil {
			g.seen = map[reflect.Type]bool{}
		}
		g.seen[t] = true
		defer delete(g.seen, t)
		return g.structOf(t)
	}
	if _, ok := g.Defs[name]; !ok {
		g.Defs[name] = map[string]any{} // placeholder: recursion refers to it
		g.Defs[name] = g.structOf(t)
	}
	return map[string]any{"$ref": "#/$defs/" + name}
}

func defName(t reflect.Type) string {
	pkg := t.PkgPath()
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		pkg = pkg[i+1:]
	}
	if pkg == "" {
		return t.Name()
	}
	return pkg + "." + t.Name()
}

func (g *SchemaGenerator) structOf(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string
	g.fields(t, props, &required)
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// fields adds t's JSON fields, flattening embedded structs as
// encoding/json does.
func (g *SchemaGenerator) fields(t reflect.Type, props map[string]any, required *[]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			g.fields(ft, props, required)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		prop := g.Of(f.Type)
		if d := f.Tag.Get("desc"); d != "" {
			if m, ok := prop.(map[string]any); ok {
				withDesc := map[string]any{"description": d}
				for k, v := range m {
					withDesc[k] = v
				}
				prop = withDesc
			}
		}
		props[name] = prop
		if !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") && f.Type.Kind() != reflect.Pointer {
			*required = append(*required, name)
		}
	}
}
