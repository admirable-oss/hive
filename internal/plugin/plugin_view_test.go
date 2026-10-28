package plugin

import (
	"errors"
	"strings"
	"testing"
)

const viewManifest = `api = 2
id = "queue"
version = "1.0.0"

[permissions]
required = ["hive.write", "process.exec"]

[[actions]]
id = "approve"
command = ["sh", "-c", "cat \"$HIVE_PLUGIN_CONTEXT\""]

[[contributes.views]]
id = "review"
title = "Review queue"
`

func TestViewDocChecks(t *testing.T) {
	m, err := ParseManifest([]byte(viewManifest))
	if err != nil {
		t.Fatal(err)
	}
	good := ViewDoc{Root: ViewNode{Kind: NodeColumn, Children: []ViewNode{
		{Kind: NodeText, Text: "3 waiting", Style: "bold"},
		{Kind: NodeTable, Columns: []string{"PR", "State"}, Rows: [][]string{{"#12", "open"}}},
		{Kind: NodeProgress, Value: 0.5},
		{Kind: NodeBadge, Text: "CI", Style: "success"},
		{Kind: NodeDivider},
		{Kind: NodeRow, Children: []ViewNode{{Kind: NodeButton, ID: "ok", Text: "Approve", Action: "approve"}, {Kind: NodeLink, Text: "Open", URL: "https://example.org/12"}}},
	}}}
	if err := CheckViewDoc(m, good); err != nil {
		t.Fatal(err)
	}
	deep := ViewNode{Kind: NodeText}
	for range MaxViewDepth + 2 {
		deep = ViewNode{Kind: NodeColumn, Children: []ViewNode{deep}}
	}
	many := ViewNode{Kind: NodeList}
	for range MaxViewNodes + 1 {
		many.Children = append(many.Children, ViewNode{Kind: NodeDivider})
	}
	for name, root := range map[string]ViewNode{
		"unknown kind":           {Kind: "script"},
		"html is text":           {Kind: NodeText, Style: "html"},
		"button without action":  {Kind: NodeButton, ID: "x"},
		"button, unknown action": {Kind: NodeButton, ID: "x", Action: "rm-rf"},
		"plain http link":        {Kind: NodeLink, URL: "http://example.org"},
		"javascript link":        {Kind: NodeLink, URL: "javascript:alert(1)"},
		"children of text":       {Kind: NodeText, Children: []ViewNode{{Kind: NodeText}}},
		"progress over 1":        {Kind: NodeProgress, Value: 2},
		"too deep":               deep,
		"too many nodes":         many,
		"long text":              {Kind: NodeText, Text: strings.Repeat("x", MaxViewText+1)},
	} {
		if err := CheckViewDoc(m, ViewDoc{Root: root}); !errors.Is(err, ErrContribution) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Views are plugin API 2.
	v1 := strings.Replace(viewManifest, "api = 2", "api = 1", 1)
	if _, err := ParseManifest([]byte(v1)); err == nil {
		t.Fatal("an API 1 plugin declared a view")
	}
}

func TestStepParams(t *testing.T) {
	specs := map[string]ParamSpec{
		"environment": {Type: "string", Required: true},
		"wait":        {Type: "boolean", Default: true},
		"retries":     {Type: "integer"},
	}
	got, err := CheckParams(specs, map[string]any{"environment": "preview"})
	if err != nil || got["wait"] != true || got["environment"] != "preview" {
		t.Fatalf("defaults: %v %v", got, err)
	}
	for name, values := range map[string]map[string]any{
		"missing required": {},
		"unknown":          {"environment": "x", "colour": "red"},
		"wrong type":       {"environment": 3},
	} {
		if _, err := CheckParams(specs, values); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDrawView(t *testing.T) {
	doc := ViewDoc{Root: ViewNode{Kind: NodeColumn, Children: []ViewNode{
		{Kind: NodeText, Text: "three pull requests wait for review", Style: "bold"},
		{Kind: NodeTable, Columns: []string{"PR", "State"}, Rows: [][]string{{"#12", "open"}, {"#7", "draft"}}},
		{Kind: NodeProgress, Text: "CI", Value: 0.5},
		{Kind: NodeRow, Children: []ViewNode{{Kind: NodeButton, ID: "ok", Text: "Approve", Action: "approve"}, {Kind: NodeBadge, Text: "2 failing", Style: "error"}}},
		{Kind: NodeList, Children: []ViewNode{{Kind: NodeText, Text: "one"}, {Kind: NodeText, Text: "two"}}},
		{Kind: NodeDivider},
	}}}
	lines := DrawView(doc, 20)
	var text []string
	for _, l := range lines {
		if l.Width() > 20 {
			t.Errorf("line wider than 20: %q", l.String())
		}
		text = append(text, strings.TrimRight(l.String(), " "))
	}
	want := []string{
		"three pull requests", "wait for review",
		"PR   State", "#12  open", "#7   draft",
		"CI ██████░░░░░░  50%",
		"[ Approve ]",
		"[2 failing]",
		"• one", "• two",
		strings.Repeat("─", 20),
	}
	if strings.Join(text, "\n") != strings.Join(want, "\n") {
		t.Fatalf("drawn:\n%s\nwant:\n%s", strings.Join(text, "\n"), strings.Join(want, "\n"))
	}
	// The button's span says which button it is.
	if b := lines[6][0]; b.Button != "ok" || b.Style != "button" {
		t.Fatalf("button span: %+v", b)
	}
	// A row that fits stays on one line.
	row := DrawView(ViewDoc{Root: doc.Root.Children[3]}, 40)
	if len(row) != 1 || row[0].String() != "[ Approve ]  [2 failing]" {
		t.Fatalf("row: %q", row)
	}
}
