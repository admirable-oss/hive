package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureDir holds the cross-language plugin API fixtures, per version.
func fixtureDir(v int) string {
	return filepath.Join("..", "..", "sdk", "protocol", "fixtures", fmt.Sprintf("v%d", v))
}

// contractManifest exercises every API 1 manifest section. It is also
// written to the fixtures as the reference manifest.
const contractManifest = `api = 1
id = "contract"
name = "Contract"
version = "1.0.0"
description = "Exercises every plugin API 1 surface."
min_hive_version = "1.0.2"
isolation = "strict"
event_concurrency = 1

[permissions]
required = ["hive.read", "hive.write"]
optional = ["network"]
[permissions.reasons]
network = "fetch build results"

[[startup]]
command = ["sh", "-c", "exec sleep 600"]
restart = true

[[actions]]
id = "show"
title = "Show the context"
command = ["sh", "-c", "cat \"$HIVE_PLUGIN_CONTEXT\""]

[[actions]]
id = "background"
command = ["sleep", "1"]
async = true

[[events]]
on = "worktree.created"
command = ["sh", "-c", "cat \"$HIVE_PLUGIN_CONTEXT\" > \"$HIVE_PLUGIN_DATA/event.json\""]
timeout = "30s"

[configuration]
version = 1
[[configuration.settings]]
name = "branch"
type = "string"
default = "main"
description = "the branch to follow"

[[contributes.cards]]
id = "build"
title = "Build"
scope = "environment"

[[contributes.status]]
id = "builds"
title = "Builds"

[[contributes.actions]]
action = "show"
contexts = ["pane", "environment"]

[[contributes.palette]]
action = "show"

[[contributes.links]]
id = "docs"
title = "Docs"
url = "https://example.com/docs"
`

// normalize replaces values that differ per run, so the rest can be held
// to the fixture.
func normalize(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	for _, k := range []string{"run", "hive_version"} {
		if _, ok := v[k]; ok {
			v[k] = "<" + k + ">"
		}
	}
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return []byte(out.String())
}

func checkFixture(t *testing.T, path string, got []byte) {
	t.Helper()
	if os.Getenv("HIVE_UPDATE_SURFACE") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(want, got) {
		t.Errorf("%s differs from what this hive produces (a plugin API %s change?); review, then HIVE_UPDATE_SURFACE=1 go test ./internal/plugin -run Contract\ngot:\n%s", path, filepath.Base(filepath.Dir(path)), got)
	}
}

// TestAPIContract holds every supported plugin API version to its
// fixtures: the manifest, the contexts commands get, and discovery.
func TestAPIContract(t *testing.T) {
	for _, v := range SupportedAPIs {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			dir := fixtureDir(v)
			checkFixture(t, filepath.Join(dir, "manifest.toml"), []byte(contractManifest))
			m, err := ParseManifest([]byte(contractManifest))
			if err != nil || m.EffectiveAPI() != v {
				t.Fatalf("reference manifest: %v", err)
			}
			h, s, _ := startHost(t, contractManifest, map[string]string{}, HostConfig{Session: "work", Isolation: IsolationStandard})
			res, err := h.Action(context.Background(), "contract", "show", Context{Environment: "api", Tab: "t1", Pane: "p1"})
			if err != nil {
				t.Fatal(err)
			}
			checkFixture(t, filepath.Join(dir, "context-action.json"), normalize(t, []byte(res.Output)))

			h.EventAt("worktree.created", 42, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), json.RawMessage(`{"environment":{"id":"feature-x"},"branch":"feature/x"}`))
			path := filepath.Join(s.DataDir("contract"), "event.json")
			waitFor(t, "the event", func() bool { b, _ := os.ReadFile(path); return len(b) > 0 })
			b, _ := os.ReadFile(path)
			checkFixture(t, filepath.Join(dir, "context-event.json"), normalize(t, b))

			d := h.Discovery("contract")
			d.Isolation = []string{"standard", "strict"} // what this system offers varies
			raw, _ := json.Marshal(d)
			checkFixture(t, filepath.Join(dir, "capabilities.json"), normalize(t, raw))

			card, _ := json.Marshal(map[string]any{
				"version": "2", "type": "request", "id": "7", "method": "plugin.card",
				"params": CardUpdate{Card: "build", Environment: "api", Rows: []CardRow{{Label: "main", Text: "passing", Tone: "success"}}, Links: []Link{{Title: "Run", URL: "https://ci.example.com/run/1"}}, TTL: 10 * time.Minute},
			})
			checkFixture(t, filepath.Join(dir, "request-plugin-card.json"), append(card, '\n'))
			if strings.Contains(string(card), "\"plugin\"") {
				t.Fatal("a plugin's own card request names no plugin: its socket says who it is")
			}
		})
	}
}
