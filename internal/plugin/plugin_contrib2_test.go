//go:build !windows

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestViewsSetClearAndClick: a plugin sets a declared view, a person's
// click runs the button's action with the view and node in its context,
// and clearing it leaves the view empty.
func TestViewsSetClearAndClick(t *testing.T) {
	h, _, pub := startHost(t, viewManifest, map[string]string{}, HostConfig{Isolation: IsolationStandard})
	if _, err := h.SetView(ViewUpdate{Plugin: "queue", View: "nope", Doc: &ViewDoc{Root: ViewNode{Kind: NodeText}}}); !errors.Is(err, ErrContribution) {
		t.Fatalf("an undeclared view: %v", err)
	}
	doc := ViewDoc{Root: ViewNode{Kind: NodeButton, ID: "ok", Text: "Approve", Action: "approve"}}
	if _, err := h.SetView(ViewUpdate{Plugin: "queue", View: "review", Doc: &doc}); err != nil {
		t.Fatal(err)
	}
	if pub.count(EventView) != 1 {
		t.Fatal("setting a view is announced")
	}
	views := h.Views()
	if len(views) != 1 || views[0].Doc == nil || views[0].Title != "Review queue" {
		t.Fatalf("views: %+v", views)
	}
	res, err := h.Click(context.Background(), ViewClick{Plugin: "queue", View: "review", Node: "ok", Pane: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	var c Context
	if err := json.Unmarshal([]byte(res.Output), &c); err != nil || c.View != "review" || c.Node != "ok" || c.Action != "approve" || c.Pane != "p1" {
		t.Fatalf("the action's context: %+v %v\n%s", c, err, res.Output)
	}
	if _, err := h.Click(context.Background(), ViewClick{Plugin: "queue", View: "review", Node: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a click on no button: %v", err)
	}
	if _, err := h.SetView(ViewUpdate{Plugin: "queue", View: "review"}); err != nil {
		t.Fatal(err)
	}
	if v := h.Views(); v[0].Doc != nil {
		t.Fatal("a cleared view shows nothing")
	}
}

// TestInvoke: a typed call sends one JSON document on stdin and reads one
// from stdout; a failing command, output that is not JSON and a timeout
// are errors.
func TestInvoke(t *testing.T) {
	manifest := `api = 2
id = "typed"
version = "1.0.0"

[permissions]
required = ["hive.read", "process.exec"]

[[actions]]
id = "noop"
command = ["true"]
`
	h, _, _ := startHost(t, manifest, map[string]string{}, HostConfig{Isolation: IsolationStandard})
	got, err := h.Invoke(context.Background(), "typed", ContribStep, []string{"sh", "-c", `read -r line; printf '{"echo":%s}' "$line"`}, map[string]any{"n": 1}, time.Minute)
	if err != nil || string(got) != `{"echo":{"n":1}}` {
		t.Fatalf("echo: %s %v", got, err)
	}
	for name, argv := range map[string][]string{
		"exit 1":   {"sh", "-c", "echo broke >&2; exit 1"},
		"not json": {"sh", "-c", "echo hello"},
	} {
		if _, err := h.Invoke(context.Background(), "typed", ContribStep, argv, nil, time.Minute); !errors.Is(err, ErrCall) {
			t.Errorf("%s: %v", name, err)
		} else if name == "exit 1" && !strings.Contains(err.Error(), "broke") {
			t.Errorf("the failure says why: %v", err)
		}
	}
	if _, err := h.Invoke(context.Background(), "typed", ContribStep, []string{"sleep", "5"}, nil, 200*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout: %v", err)
	}
}

// TestRunStep: a plugin step reads its typed inputs on stdin and answers
// its outputs; inputs and outputs are checked against their types.
func TestRunStep(t *testing.T) {
	manifest := `api = 2
id = "deploy"
version = "1.0.0"

[permissions]
required = ["process.exec"]

[[contributes.steps]]
name = "preview"
command = ["sh", "-c", "read -r in; case \"$in\" in *'\"wait\":true'*) w=yes;; *) w=no;; esac; printf '{\"outputs\":{\"url\":\"https://p.example.org\",\"waited\":\"%s\"},\"summary\":\"deployed\"}' \"$w\""]
[contributes.steps.inputs]
environment = { type = "string", required = true }
wait = { type = "boolean", default = true }
retries = { type = "integer" }
[contributes.steps.outputs]
url = { type = "string" }
waited = { type = "string" }

[[contributes.steps]]
name = "liar"
command = ["sh", "-c", "echo '{\"outputs\":{\"count\":\"many\"}}'"]
[contributes.steps.outputs]
count = { type = "integer" }
`
	h, _, _ := startHost(t, manifest, map[string]string{}, HostConfig{Isolation: IsolationStandard})
	ctx := context.Background()
	out, summary, err := h.RunStep(ctx, StepCall{Plugin: "deploy", Step: "preview", With: map[string]string{"environment": "pr-7"}})
	if err != nil || out["url"] != "https://p.example.org" || out["waited"] != "yes" || summary != "deployed" {
		t.Fatalf("the step: %v %q %v", out, summary, err)
	}
	if out, _, err := h.RunStep(ctx, StepCall{Plugin: "deploy", Step: "preview", With: map[string]string{"environment": "x", "wait": "false"}}); err != nil || out["waited"] != "no" {
		t.Fatalf("a typed input: %v %v", out, err)
	}
	for name, with := range map[string]map[string]string{
		"missing required": {},
		"wrong type":       {"environment": "x", "retries": "a few"},
		"unknown":          {"environment": "x", "colour": "red"},
	} {
		if _, _, err := h.RunStep(ctx, StepCall{Plugin: "deploy", Step: "preview", With: with}); err == nil {
			t.Errorf("%s ran", name)
		}
	}
	if _, _, err := h.RunStep(ctx, StepCall{Plugin: "deploy", Step: "liar"}); !errors.Is(err, ErrCall) {
		t.Fatalf("an output of the wrong type: %v", err)
	}
	// Before it runs: templates are checked by name only.
	if err := h.CheckStep("deploy", "preview", map[string]string{"environment": "${{ inputs.env }}", "retries": "${{ steps.a.outputs.n }}"}); err != nil {
		t.Fatal(err)
	}
	if err := h.CheckStep("deploy", "preview", map[string]string{"retries": "2"}); err == nil {
		t.Fatal("a missing required input passed the check")
	}
	if err := h.CheckStep("deploy", "prod", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown step: %v", err)
	}
}
