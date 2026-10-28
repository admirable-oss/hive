//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/workflow"
)

const m26ContribPlugin = `api = 2
id = "queue"
version = "1.0.0"
min_hive_version = "1.7.0"

[permissions]
required = ["hive.write"]

[[actions]]
id = "approve"
command = ["sh", "-c", "echo approved"]

[[contributes.views]]
id = "review"
title = "Review queue"

[[contributes.steps]]
name = "stamp"
command = ["sh", "-c", "read -r in; case \"$in\" in *'\"label\":\"ok\"'*) printf '{\"outputs\":{\"stamp\":\"s-1\"}}';; *) echo 'no label' >&2; exit 1;; esac"]
[contributes.steps.inputs]
label = { type = "string", required = true }
[contributes.steps.outputs]
stamp = { type = "string" }

[[contributes.triggers]]
name = "done"
`

const m26ContribWorkflow = `version = 1
environment = "w"
[inputs.state]
[[on]]
plugin = "queue/done"
match = { state = "ok" }
[on.inputs]
state = "${{ trigger.data.state }}"
[[step]]
id = "stamp"
uses = "queue/stamp"
with = { label = "${{ inputs.state }}" }
[[step]]
id = "say"
run = ["echo", "stamped ${{ steps.stamp.outputs.stamp }}"]
`

// TestM26PluginContributions: an API 2 plugin's view shows in a pane with
// no process, set and drawn through the CLI, and its button runs the
// plugin's action; its trigger runs a workflow whose plugin step's output
// feeds the next step.
func TestM26PluginContributions(t *testing.T) {
	c := newCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte(m26ContribPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(c.home, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.home, "workflows", "stamped.toml"), []byte(m26ContribWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.cfg, []byte("[terminal]\nshell = \"/bin/sh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("daemon", "start")
	c.must("plugin", "link", dir)
	c.must("env", "create", "w", "--cwd", t.TempDir())

	// The view, in a pane.
	if out := c.must("plugin", "views"); !strings.Contains(out, "queue/review") || !strings.Contains(out, "(nothing yet)") {
		t.Fatalf("views:\n%s", out)
	}
	var tab struct{ Pane pane.Pane }
	decode(t, c.must("--json", "tab", "create", "w", "--view", "queue/review"), &tab)
	if tab.Pane.View != "queue/review" || tab.Pane.ProcessID != "" {
		t.Fatalf("the view pane: %+v", tab.Pane)
	}
	if _, stderr, code := c.run("tab", "create", "w", "--view", "queue/none"); code == 0 || !strings.Contains(stderr, "queue/none") {
		t.Fatalf("a view nobody declares: %d %s", code, stderr)
	}
	doc := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(doc, []byte(`{"root":{"kind":"column","children":[
		{"kind":"text","text":"2 waiting","style":"bold"},
		{"kind":"button","id":"ok","text":"Approve","action":"approve"},
		{"kind":"link","text":"Board","url":"https://example.org/board"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c.must("plugin", "view", "review", "--plugin", "queue", "--doc", doc)
	if out := c.must("plugin", "views", "queue/review"); !strings.Contains(out, "2 waiting") || !strings.Contains(out, "[ Approve ]") || !strings.Contains(out, "Board <https://example.org/board>") {
		t.Fatalf("the drawn view:\n%s", out)
	}
	if out := c.must("plugin", "click", "queue/review", "ok"); !strings.Contains(out, "approved") {
		t.Fatalf("click:\n%s", out)
	}
	if err := os.WriteFile(doc, []byte(`{"root":{"kind":"button","id":"x","text":"Run","action":"rm-rf"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := c.run("plugin", "view", "review", "--plugin", "queue", "--doc", doc); code == 0 || !strings.Contains(stderr, "rm-rf") {
		t.Fatalf("a button for an undeclared action: %d %s", code, stderr)
	}

	// The trigger and the step.
	if out := c.must("plugin", "trigger", "done", "--plugin", "queue", "--field", "state=late"); strings.TrimSpace(out) != "" {
		t.Fatalf("a payload the match does not fit ran: %q", out)
	}
	if _, stderr, code := c.run("plugin", "trigger", "undeclared", "--plugin", "queue"); code == 0 || !strings.Contains(stderr, "declares no trigger") {
		t.Fatalf("an undeclared trigger: %d %s", code, stderr)
	}
	run := strings.TrimSpace(c.must("plugin", "trigger", "done", "--plugin", "queue", "--field", "state=ok"))
	if run == "" {
		t.Fatal("the trigger started no run")
	}
	c.must("workflow", "wait", run)
	var r workflow.Run
	decode(t, c.must("--json", "workflow", "status", run), &r)
	if st := r.Step("stamp").Last(); st.Outputs["stamp"] != "s-1" {
		t.Fatalf("the plugin step: %+v", st)
	}
	if out := r.Step("say").Last().Output; len(out) != 1 || out[0] != "stamped s-1" {
		t.Fatalf("the next step: %v", out)
	}
}
