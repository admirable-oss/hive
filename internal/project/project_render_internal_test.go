package project

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const roundTripFile = `version = 1
name = "shop"
toolchain = "auto"
workflows = [".hive/workflows"]
plugins = ["pr-status"]

[vars]
NODE_ENV = "development"
QUOTE = "say \"hi\"\tand\\go"

[secrets]
API_KEY = "op://dev/api/key"

[env]
required = ["DATABASE_URL"]
optional = ["STRIPE_KEY"]

[[gate]]
name = "test"
command = ["go", "test", "./..."]
timeout = "10m"

[[tab]]
name = "dev"
split = "vertical"
  [[tab.pane]]
  name = "server"
  command = ["sh", "-c", "echo 'hi'; exec sleep 1"]
  cwd = "web"
  [[tab.pane]]

[[agent]]
name = "reviewer"
kind = ["claude", "codex"]
isolate = true
base = "main"
args = ["--fast"]

[[environment]]
id = "shop-api"
path = "api"
toolchain = "mise"
  [environment.vars]
  PORT = "8080"
  [environment.secrets]
  TOKEN = "pass:shop/api"
  [environment.env]
  required = ["API_URL"]
  [[environment.tab]]
  name = "api"
    [[environment.tab.pane]]
    command = ["go", "run", "./cmd/api"]
  [[environment.agent]]
  kind = "codex"
  worktree = "api-work"
`

func specJSON(t *testing.T, s Spec) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRenderRoundTrips(t *testing.T) {
	root := filepath.FromSlash("/src/shop")
	for name, text := range map[string]string{"full": roundTripFile, "minimal": "version = 1\n"} {
		s, _, err := Parse([]byte(text), nil, root)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := Render(s.file)
		again, _, err := Parse(out, nil, root)
		if err != nil {
			t.Fatalf("%s: the rendered file does not parse: %v\n%s", name, err, out)
		}
		if a, b := specJSON(t, s), specJSON(t, again); a != b {
			t.Errorf("%s: rendering changed the project:\n%s\n%s\nrendered:\n%s", name, a, b, out)
		}
		// Rendering is canonical: twice gives the same text.
		if out2 := Render(again.file); !bytes.Equal(out2, out) {
			t.Errorf("%s: not canonical:\n%s\n---\n%s", name, out, out2)
		}
	}
}

func FuzzRenderRoundTrip(f *testing.F) {
	f.Add([]byte(roundTripFile))
	f.Add([]byte("version = 1\n[[tab]]\nname = \"a\\u0001b\"\n[[tab.pane]]\ncommand = [\"\\u00e9\", \"\\\\\"]\n"))
	root := filepath.FromSlash("/src/shop")
	f.Fuzz(func(t *testing.T, data []byte) {
		s, _, err := Parse(data, nil, root)
		if err != nil {
			return
		}
		out := Render(s.file)
		again, _, err := Parse(out, nil, root)
		if err != nil {
			t.Fatalf("a parsed file renders to one that does not parse: %v\n%s", err, out)
		}
		if a, b := specJSON(t, s), specJSON(t, again); a != b {
			t.Fatalf("rendering changed the project:\n%s\n%s", a, b)
		}
	})
}

func diffState(root string) State {
	return State{
		Environments: []EnvState{{ID: "shop", Root: root}, {ID: "wt", Root: "/wt", WorktreeRepo: root}},
		Tabs: map[string][]TabState{
			"shop": {
				{ID: "t1", Name: "dev", Split: SplitVertical, Panes: []Pane{{Name: "server", Command: []string{"npm", "start"}, Cwd: "web"}, {}}},
				{ID: "t2", Name: "scratch", Split: SplitHorizontal, Panes: []Pane{{Command: []string{"htop"}}, {Cwd: filepath.Join(root, "docs")}}},
				{ID: "t3", Name: "reviewer"},
				{ID: "t4", Name: "notes", Panes: []Pane{{Cwd: "/elsewhere"}}},
			},
		},
		Agents: []AgentState{
			{ID: "a1", Name: "reviewer", Kind: "claude", EnvironmentID: "shop", TabID: "t3"},
			{ID: "a2", Name: "helper", Kind: "codex", EnvironmentID: "wt", TabID: "t9"},
		},
		Known: []string{"claude", "codex"}, Installed: []string{"claude", "codex"},
	}
}

const diffFile = `version = 1
name = "shop"
# a comment save does not keep
[[tab]]
name = "dev"
split = "vertical"
  [[tab.pane]]
  name = "server"
  command = ["npm", "run", "dev"]
  cwd = "web"
  [[tab.pane]]
[[tab]]
name = "logs"
  [[tab.pane]]
  command = ["tail", "-f", "log"]
[[agent]]
name = "reviewer"
kind = "claude"
`

func TestDiffState(t *testing.T) {
	root := t.TempDir()
	s, loc, err := Parse([]byte(diffFile), nil, root)
	if err != nil {
		t.Fatal(err)
	}
	d := DiffState(s, loc, nil, diffState(root))
	var got []string
	for _, c := range d.Changes {
		got = append(got, c.Kind+" "+c.What+" "+c.Environment+"/"+c.Name+": "+c.Detail)
	}
	want := []string{
		"missing tab shop/logs: ",
		"changed tab shop/dev: pane 1 runs npm start, declared npm run dev",
		"extra tab shop/scratch: htop | a shell",
		"extra tab shop/notes: a shell",
		"extra agent wt/helper: codex",
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSaveText(t *testing.T) {
	root := t.TempDir()
	s, _, err := Parse([]byte(diffFile), nil, root)
	if err != nil {
		t.Fatal(err)
	}
	text, changes := SaveText(s, diffState(root))
	saved, _, err := Parse(text, nil, root)
	if err != nil {
		t.Fatalf("saved text does not parse: %v\n%s", err, text)
	}
	var names []string
	for _, tab := range saved.Environments[0].Tabs {
		names = append(names, tab.Name)
	}
	// Declared tabs stay (logs is not running: kept), opened ones are
	// added, and an agent's tab is not a tab of the file.
	if want := []string{"dev", "logs", "scratch", "notes"}; !slices.Equal(names, want) {
		t.Errorf("tabs = %v, want %v\n%s", names, want, text)
	}
	dev := saved.Environments[0].Tabs[0]
	if !slices.Equal(dev.Panes[0].Command, []string{"npm", "start"}) || dev.Panes[0].Cwd != "web" || dev.Split != SplitVertical {
		t.Errorf("dev = %+v", dev)
	}
	scratch := saved.Environments[0].Tabs[2]
	if scratch.Panes[1].Cwd != "docs" {
		t.Errorf("scratch's second pane is in %q, want docs (relative)", scratch.Panes[1].Cwd)
	}
	if notes := saved.Environments[0].Tabs[3]; notes.Panes[0].Cwd != "" {
		t.Errorf("a directory outside the project was written: %q", notes.Panes[0].Cwd)
	}
	if len(changes) != 3 || strings.Contains(string(text), "a comment") {
		t.Errorf("changes = %+v; text:\n%s", changes, text)
	}
	// Nothing to save when the file says what runs.
	same, _, _ := Parse(text, nil, root)
	if _, changes := SaveText(same, diffState(root)); len(changes) != 0 {
		t.Errorf("saving again changes %+v", changes)
	}
}

func TestTemplates(t *testing.T) {
	write := func(dir, name, text string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	parse := func(dir string, text []byte) Spec {
		t.Helper()
		s, _, err := Parse(text, nil, dir)
		if err != nil {
			t.Fatalf("%v\n%s", err, text)
		}
		return s
	}

	goRepo := t.TempDir()
	write(goRepo, "go.mod", "module x\n")
	write(goRepo, "mise.toml", "")
	text, err := Template("blank", goRepo)
	if err != nil {
		t.Fatal(err)
	}
	s := parse(goRepo, text)
	if s.Toolchain != ToolchainAuto || len(s.Gates) != 1 || !slices.Equal(s.Gates[0].Command, []string{"go", "test", "./..."}) || !strings.HasPrefix(string(text), "# Hive project file") {
		t.Errorf("blank for Go:\n%s", text)
	}

	node := t.TempDir()
	write(node, "package.json", `{"scripts": {"dev": "vite", "test": "vitest"}}`)
	for _, name := range []string{"blank", "agents", "test-watcher"} {
		text, err := Template(name, node)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := parse(node, text)
		dev := s.Environments[0].Tabs[0]
		if !slices.Equal(dev.Panes[0].Command, []string{"npm", "run", "dev"}) {
			t.Errorf("%s: dev tab = %+v", name, dev)
		}
		switch name {
		case "agents":
			if len(s.Environments[0].Agents) != 2 {
				t.Errorf("agents: %+v", s.Environments[0].Agents)
			}
		case "test-watcher":
			if len(s.Environments[0].Tabs) != 2 || !slices.Equal(s.Environments[0].Tabs[1].Panes[0].Command, []string{"npm", "test", "--", "--watch"}) {
				t.Errorf("test-watcher: %+v", s.Environments[0].Tabs)
			}
		}
	}

	mono := t.TempDir()
	write(mono, "packages/web/package.json", "{}")
	write(mono, "services/api/go.mod", "module api\n")
	write(mono, "node_modules/x/package.json", "{}")
	text, err = Template("monorepo", mono)
	if err != nil {
		t.Fatal(err)
	}
	s = parse(mono, text)
	var paths []string
	for _, e := range s.Environments[1:] {
		paths = append(paths, e.Path)
	}
	if want := []string{"packages/web", "services/api"}; !slices.Equal(paths, want) {
		t.Errorf("monorepo environments at %v, want %v", paths, want)
	}
	if _, err := Template("monorepo", t.TempDir()); err == nil {
		t.Error("a monorepo template without packages")
	}
	if _, err := Template("nope", t.TempDir()); err == nil || !strings.Contains(err.Error(), "blank, agents") {
		t.Errorf("unknown template: %v", err)
	}
}
