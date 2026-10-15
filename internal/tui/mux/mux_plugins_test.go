package mux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
)

const uiPlugin = `api = 1
id = "ci"
name = "CI"
version = "1.0.0"

[permissions]
required = ["hive.read"]

[[actions]]
id = "rerun"
title = "Rerun the pipeline"
command = ["sh", "-c", "echo rerun for $(sed -n 's/.*\"environment\":\"\\([^\"]*\\)\".*/\\1/p' \"$HIVE_PLUGIN_CONTEXT\")"]

[[actions]]
id = "agent-only"
command = ["true"]

[[contributes.cards]]
id = "pipeline"
title = "Pipeline"

[[contributes.status]]
id = "build"
title = "Build"

[[contributes.actions]]
action = "rerun"
contexts = ["environment"]

[[contributes.actions]]
action = "agent-only"
title = "Only for agents"
contexts = ["agent"]

[[contributes.palette]]
action = "rerun"

[[contributes.links]]
id = "dash"
title = "Dashboard"
url = "https://ci.example.com"

[configuration]
version = 1
[[configuration.settings]]
name = "branch"
type = "string"
default = "main"
description = "the branch to watch"
[[configuration.settings]]
name = "verbose"
type = "boolean"
default = false
`

// pluginHarness is the harness with plugin "ci" linked before the daemon
// starts.
func pluginHarness(t *testing.T, w, h int) *harness {
	t.Helper()
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, plugin.ManifestFile), []byte(uiPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plugin.NewStore(filepath.Join(dir, "plugins")).Link(context.Background(), src, "dev"); err != nil {
		t.Fatal(err)
	}
	c := startDaemonIn(t, dir)
	ctx := context.Background()
	work := t.TempDir()
	if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: "api", Root: work}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NewWorkspace(c).TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "api", Name: "main"}); err != nil {
		t.Fatal(err)
	}
	return startApp(t, c, w, h, work, Options{})
}

// TestPluginContributionsInTheUI: a plugin's card shows in the sidebar for
// its environment, its indicator on the tab bar, and the palette offers
// what applies here; running an action passes where the user is.
func TestPluginContributionsInTheUI(t *testing.T) {
	h := pluginHarness(t, 140, 30)
	p := client.NewPlugins(h.c)
	ctx := context.Background()
	if _, err := p.SetCard(ctx, plugin.CardUpdate{
		Plugin: "ci", Card: "pipeline", Environment: "api", Rows: []plugin.CardRow{{Label: "main", Text: "passing", Tone: "success"}},
		Links: []plugin.Link{{Title: "Run 88", URL: "https://ci.example.com/run/88"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetStatus(ctx, plugin.StatusUpdate{Plugin: "ci", Indicator: "build", Text: "build green", Tone: "success"}); err != nil {
		t.Fatal(err)
	}
	h.waitText("main passing")
	scr := h.screen()
	for _, want := range []string{"Plugins", "▪ Pipeline", "↗ Run 88", "build green"} {
		if !strings.Contains(scr, want) {
			t.Fatalf("screen lacks %q:\n%s", want, scr)
		}
	}

	// The palette offers the environment action once (it is also a palette
	// entry), the links and the settings; not the agent-only action.
	h.press("ctrl+b", ":")
	pal, ok := h.a.ui.overlay.(*palette)
	if !ok {
		t.Fatalf("overlay = %T", h.a.ui.overlay)
	}
	var titles []string
	for _, it := range pal.items {
		titles = append(titles, it.title)
	}
	got := strings.Join(titles, "|")
	if got != "Rerun the pipeline|Dashboard|Configure CI|Run 88" {
		t.Fatalf("palette items = %q", got)
	}
	h.typeText("rerun")
	h.press("enter")
	h.waitFor("the action's toast", func() bool {
		for _, ts := range h.a.ui.toasts {
			if strings.Contains(ts.text, "rerun for api") {
				return true
			}
		}
		return false
	})

	// A narrow screen drops the indicator, never the tabs.
	h.a.width = 90
	if spans := h.a.pluginStatusSpans(); len(spans) != 0 {
		t.Fatalf("indicator on a narrow screen: %v", spans)
	}
	h.a.width = 140

	// A plugin's card goes when it is cleared.
	if _, err := p.SetCard(ctx, plugin.CardUpdate{Plugin: "ci", Card: "pipeline", Environment: "api"}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the card to go", func() bool { return !strings.Contains(h.screen(), "Pipeline") })
}

// TestPluginSettingsForm: the palette's settings entry opens a form built
// from the declared settings; editing one saves it through the daemon,
// typed by its schema.
func TestPluginSettingsForm(t *testing.T) {
	h := pluginHarness(t, 140, 30)
	h.waitFor("contributions", func() bool { return len(h.a.plugins.contrib.Declared) > 0 })
	h.a.openPluginConfig("ci")
	h.waitFor("the form", func() bool { _, ok := h.a.ui.overlay.(*configForm); return ok })
	h.waitText("branch = main")
	h.press("enter")  // edit branch
	h.press("ctrl+u") // clear the input
	h.typeText("release")
	h.press("enter")
	h.waitText("branch = release")
	h.press("down", "space") // toggle verbose
	h.waitText("verbose = true")
	v, err := client.NewPlugins(h.c).Config(context.Background(), "ci")
	if err != nil || v.Values["branch"] != "release" || v.Values["verbose"] != true {
		t.Fatalf("saved = %+v, %v", v.Values, err)
	}
	h.press("up", "d") // back to the default
	h.waitText("branch = main")
}

func TestPaletteItemsFollowContextAndAvailability(t *testing.T) {
	a := &App{}
	a.plugins.contrib = plugin.Contributions{Declared: []plugin.Contribution{
		{Plugin: "x", Kind: plugin.ContribAction, Action: "a", Title: "Env action", Contexts: []string{"environment"}, Available: true},
		{Plugin: "x", Kind: plugin.ContribAction, Action: "s", Title: "Session action", Contexts: []string{"session"}, Available: true},
		{Plugin: "x", Kind: plugin.ContribPalette, Action: "h", Title: "Hidden", Hidden: true, Available: true},
		{Plugin: "y", Kind: plugin.ContribPalette, Action: "f", Title: "Failed plugin", Available: false},
		{Plugin: "x", Kind: plugin.ContribCard, ID: "c", Title: "A card", Available: true},
	}}
	var titles []string
	for _, it := range a.paletteItems() {
		titles = append(titles, it.title)
	}
	if strings.Join(titles, "|") != "Session action" {
		t.Fatalf("no environment, items = %v", titles)
	}
}
