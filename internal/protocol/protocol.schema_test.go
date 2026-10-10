package protocol

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type schemaNode struct {
	Name     string        `json:"name" desc:"its name"`
	Children []*schemaNode `json:"children,omitempty"`
}

type schemaBase struct {
	ID string `json:"id"`
}

type schemaParams struct {
	schemaBase
	Node    schemaNode        `json:"node"`
	When    time.Time         `json:"when"`
	Wait    time.Duration     `json:"wait,omitempty"`
	Data    []byte            `json:"data,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Secret  string            `json:"-"`
	private int
}

var _ = schemaParams{}.private // only its absence from the schema matters

func TestSchemaDescribesMethods(t *testing.T) {
	r := NewRouter()
	r.MustRegister("x.get", Method(func(context.Context, schemaParams) ([]schemaNode, error) { return nil, nil }))
	r.MustRegister("x.raw", HandlerFunc(func(context.Context, Request) Response { return Response{} }))
	s := r.Schema()
	if s.APILevel != APILevel || len(s.Methods) != 2 || s.Methods[1].Params != nil {
		t.Fatalf("schema = %+v", s)
	}
	data, _ := json.Marshal(s)
	got := string(data)
	for _, want := range []string{
		`"$ref":"#/$defs/protocol.schemaParams"`,
		// Embedded fields are flattened; recursive types refer to themselves.
		`"id":{"type":"string"}`,
		`"children":{"items":{"$ref":"#/$defs/protocol.schemaNode"},"type":"array"}`,
		`"description":"its name"`,
		`"when":{"format":"date-time","type":"string"}`,
		`"data":{"contentEncoding":"base64","type":"string"}`,
		`"required":["id","node","when"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("schema lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Secret") || strings.Contains(got, "private") {
		t.Errorf("schema shows hidden fields:\n%s", got)
	}

	inline := SchemaGenerator{Inline: true}
	data, _ = json.Marshal(inline.Of(reflect.TypeFor[schemaNode]()))
	if strings.Contains(string(data), "$ref") || !strings.Contains(string(data), `"children"`) {
		t.Errorf("inline schema = %s", data)
	}
}
