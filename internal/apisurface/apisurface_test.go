package apisurface

import (
	"slices"
	"testing"
)

func TestCompare(t *testing.T) {
	frozen := []string{"method a", "a params.x: string", "a result.y: int"}
	current := []string{"method a", "a params.x: integer", "a result.y: int", "method b"}
	breaking, added := Compare(frozen, current)
	if !slices.Equal(breaking, []string{"- a params.x: string"}) {
		t.Fatalf("breaking = %q", breaking)
	}
	if !slices.Equal(added, []string{"+ a params.x: integer", "+ method b"}) {
		t.Fatalf("added = %q", added)
	}
}

func TestSchemaLines(t *testing.T) {
	lines, err := SchemaLines([]byte(`{"methods":[{"name":"m","pipe":true,"params":{"type":"object","properties":{"id":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}},"required":["id"]},"result":{"$ref":"#/$defs/p.T"}}],"$defs":{"p.T":{"type":"object","properties":{"env":{"type":"object","additionalProperties":{"type":"string"}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pipe method m", "m params: object", "m params.id: string", "m params.tags: []string", "m result: p.T", "type p.T: object", "type p.T.env: map[string]string"}
	if !slices.Equal(lines, want) {
		t.Fatalf("lines =\n%q\nwant\n%q", lines, want)
	}
}
