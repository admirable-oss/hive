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

const forgeManifest = `api = 2
id = "gitea"
version = "1.0.0"

[permissions]
required = ["network"]
[permissions.network]
hosts = ["git.example.org:443", "*.gitea.io:443"]

[[contributes.forges]]
name = "gitea"
hosts = ["git.example.org", "*.gitea.io"]
command = ["sh", "-c", "read -r in; case \"$in\" in *'\"head\":\"feature\"'*'\"op\":\"pr.create\"'*) echo '{\"url\":\"https://git.example.org/o/r/pulls/9\",\"number\":9}';; *) echo \"$in\" >&2; exit 1;; esac"]

[[contributes.trackers]]
name = "linear"
command = ["sh", "-c", "read -r in; case \"$in\" in *'\"op\":\"get\"'*) echo '{\"title\":\"Fix it\",\"state\":\"open\"}';; *'\"op\":\"search\"'*) echo '{\"issues\":[{\"key\":\"HIV-1\",\"title\":\"One\"}]}';; *) echo '{}';; esac"]
`

// TestForgesAndTrackers: a plugin's forge serves the hosts it lists (an
// exact host before a wildcard) through its command, and its tracker
// answers operations; their declarations need the network for them.
func TestForgesAndTrackers(t *testing.T) {
	h, _, _ := startHost(t, forgeManifest, map[string]string{}, HostConfig{Isolation: IsolationStandard})
	for host, want := range map[string]bool{"git.example.org": true, "code.gitea.io": true, "gitea.io": false, "github.com": false} {
		if _, ok := h.ForgeFor(host); ok != want {
			t.Errorf("forge for %s: %v", host, ok)
		}
	}
	f, _ := h.ForgeFor("GIT.example.org")
	if f.Name() != "plugin:gitea/gitea" {
		t.Fatalf("name: %s", f.Name())
	}
	raw, err := h.CallForge(context.Background(), f, ForgeOpCreate, map[string]any{"head": "feature", "base": "main"})
	var res ForgeResult
	if err != nil || json.Unmarshal(raw, &res) != nil || res.Number != 9 {
		t.Fatalf("pr.create: %s %v", raw, err)
	}
	if _, err := h.CallForge(context.Background(), f, ForgeOpCreate, map[string]any{"head": "other"}); !errors.Is(err, ErrCall) || !strings.Contains(err.Error(), "pr.create") {
		t.Fatalf("a failing forge says what it got: %v", err)
	}
	tr, ok := h.TrackerFor("linear")
	if !ok || strings.Join(h.TrackerNames(), ",") != "linear" {
		t.Fatalf("tracker: %v %v", ok, h.TrackerNames())
	}
	if raw, err := h.CallTracker(context.Background(), tr, TrackerRequest{Op: TrackerOpGet, Key: "HIV-1"}); err != nil || !strings.Contains(string(raw), "Fix it") {
		t.Fatalf("get: %s %v", raw, err)
	}

	for name, edit := range map[string][2]string{
		"no network":         {`required = ["network"]`, `required = ["hive.read"]`},
		"host not reachable": {`hosts = ["git.example.org:443", "*.gitea.io:443"]`, `hosts = ["git.example.org:443"]`},
		"a port in hosts":    {`hosts = ["git.example.org", "*.gitea.io"]`, `hosts = ["git.example.org:8443"]`},
		"github is built in": {`name = "linear"`, `name = "github"`},
		"api 1":              {`api = 2`, `api = 1`},
	} {
		if _, err := ParseManifest([]byte(strings.Replace(forgeManifest, edit[0], edit[1], 1))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A wildcard for a parent domain covers the forge's wildcard.
	parent := strings.Replace(forgeManifest, `"*.gitea.io:443"`, `"*.io:443"`, 1)
	if _, err := ParseManifest([]byte(parent)); err != nil {
		t.Fatalf("a parent wildcard: %v", err)
	}
}
