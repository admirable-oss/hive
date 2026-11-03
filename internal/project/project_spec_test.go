package project_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/project"
)

// rfcExample is RFC 0009's example file.
const rfcExample = `version = 1
name = "shop"

[vars]
NODE_ENV = "development"

[env]
required = ["DATABASE_URL"]
optional = ["STRIPE_KEY"]

[[tab]]
name = "dev"
split = "horizontal"

  [[tab.pane]]
  name = "server"
  command = ["npm", "run", "dev"]
  cwd = "web"

  [[tab.pane]]
  name = "shell"

[[agent]]
name = "reviewer"
kind = ["claude", "codex"]
isolate = true
args = []

[[environment]]
id = "shop-api"
path = "api"
  [[environment.tab]]
  name = "api"
    [[environment.tab.pane]]
    command = ["go", "run", "./cmd/api"]
`

var root = filepath.FromSlash("/src/shop")

func parse(t *testing.T, text string) project.Spec {
	t.Helper()
	s, _, err := project.Parse([]byte(text), nil, root)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func TestParseRFCExample(t *testing.T) {
	s := parse(t, rfcExample)
	if s.Name != "shop" || len(s.Environments) != 2 {
		t.Fatalf("spec = %+v", s)
	}
	main, api := s.Environments[0], s.Environments[1]
	if main.ID != "shop" || main.Path != "." || main.Root != root {
		t.Errorf("main environment = %+v", main)
	}
	if api.ID != "shop-api" || api.Path != "api" || api.Root != filepath.Join(root, "api") {
		t.Errorf("api environment = %+v", api)
	}
	// The project's vars and names hold in every environment.
	for _, e := range s.Environments {
		if e.Vars["NODE_ENV"] != "development" || !slices.Equal(e.Required, []string{"DATABASE_URL"}) || !slices.Equal(e.Optional, []string{"STRIPE_KEY"}) {
			t.Errorf("environment %s: vars %v, required %v, optional %v", e.ID, e.Vars, e.Required, e.Optional)
		}
	}
	tab := main.Tabs[0]
	if tab.Name != "dev" || tab.Split != project.SplitHorizontal || len(tab.Panes) != 2 ||
		!slices.Equal(tab.Panes[0].Command, []string{"npm", "run", "dev"}) || tab.Panes[0].Cwd != "web" || tab.Panes[1].Command != nil {
		t.Errorf("tab = %+v", tab)
	}
	a := main.Agents[0]
	if a.Name != "reviewer" || !slices.Equal(a.Kinds, []string{"claude", "codex"}) || !a.Isolate {
		t.Errorf("agent = %+v", a)
	}
}

func TestParseDefaults(t *testing.T) {
	s := parse(t, "version = 1\n[[agent]]\nkind = \"codex\"\n[[tab]]\nname = \"t\"\n[[tab.pane]]\n")
	if s.Name != "shop" {
		t.Errorf("name = %q, want the directory's", s.Name)
	}
	if a := s.Environments[0].Agents[0]; a.Name != "codex" || !slices.Equal(a.Kinds, []string{"codex"}) {
		t.Errorf("agent = %+v, want named after its kind", a)
	}
	if tab := s.Environments[0].Tabs[0]; tab.Split != project.SplitHorizontal {
		t.Errorf("split = %q", tab.Split)
	}
}

func TestNameFromDir(t *testing.T) {
	for dir, want := range map[string]string{
		"/src/shop":     "shop",
		"/src/my shop!": "my-shop-",
		"/src/.hidden":  "hidden",
		"/":             "project",
	} {
		if got := project.NameFromDir(filepath.FromSlash(dir)); got != want {
			t.Errorf("NameFromDir(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"no version":         {"name = \"x\"", "version = 1 is required"},
		"newer format":       {"version = 2", "update hive"},
		"unknown key":        {"version = 1\ncolour = \"red\"", "colour"},
		"bad toml":           {"version = 1\n[[tab]\n", "hive.toml:2"},
		"bad name":           {"version = 1\nname = \"-x\"", "name \"-x\""},
		"env id twice":       {"version = 1\nname = \"a\"\n[[environment]]\nid = \"a\"\npath = \"b\"", "used twice"},
		"env path twice":     {"version = 1\n[[environment]]\nid = \"b\"\npath = \".\"", "already"},
		"env without path":   {"version = 1\n[[environment]]\nid = \"b\"", "path is required"},
		"path outside":       {"version = 1\n[[environment]]\nid = \"b\"\npath = \"../other\"", "inside the repository"},
		"absolute path":      {"version = 1\n[[environment]]\nid = \"b\"\npath = \"/etc\"", "inside the repository"},
		"cwd outside":        {"version = 1\n[[tab]]\nname = \"t\"\n[[tab.pane]]\ncwd = \"../..\"", "inside the repository"},
		"tab without panes":  {"version = 1\n[[tab]]\nname = \"t\"", "at least one"},
		"tab without name":   {"version = 1\n[[tab]]\n[[tab.pane]]", "name is required"},
		"tab twice":          {"version = 1\n[[tab]]\nname = \"t\"\n[[tab.pane]]\n[[tab]]\nname = \"t\"\n[[tab.pane]]", "used twice"},
		"bad split":          {"version = 1\n[[tab]]\nname = \"t\"\nsplit = \"diagonal\"\n[[tab.pane]]", "split"},
		"command string":     {"version = 1\n[[tab]]\nname = \"t\"\n[[tab.pane]]\ncommand = \"npm run dev\"", `["npm", "run", "dev"]`},
		"empty program":      {"version = 1\n[[tab]]\nname = \"t\"\n[[tab.pane]]\ncommand = [\"\"]", "empty program"},
		"agent without kind": {"version = 1\n[[agent]]\nname = \"a\"", "kind is required"},
		"bad kind":           {"version = 1\n[[agent]]\nkind = \"Not A Kind\"", "not an agent kind"},
		"agent and tab":      {"version = 1\n[[tab]]\nname = \"a\"\n[[tab.pane]]\n[[agent]]\nname = \"a\"\nkind = \"codex\"", "tab has that name"},
		"isolate and branch": {"version = 1\n[[agent]]\nkind = \"codex\"\nisolate = true\nworktree = \"b\"", "one or the other"},
		"base alone":         {"version = 1\n[[agent]]\nkind = \"codex\"\nbase = \"main\"", "base"},
		"bad branch":         {"version = 1\n[[agent]]\nkind = \"codex\"\nworktree = \"a b\"", "not a branch name"},
		"bad var name":       {"version = 1\n[vars]\n\"A-B\" = \"x\"", "not a variable name"},
		"name twice":         {"version = 1\n[env]\nrequired = [\"A\"]\noptional = [\"A\"]", "named twice"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := project.Parse([]byte(tc.text), nil, root)
			if err == nil || !errors.Is(err, project.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrInvalid with %q", err, tc.want)
			}
		})
	}
}

func TestParseRefusesSecrets(t *testing.T) {
	for value, why := range map[string]string{
		"ghp_" + strings.Repeat("a", 36):              "GitHub",
		"sk-" + strings.Repeat("b", 40):               "API key",
		"AKIA" + strings.Repeat("C", 16):              "AWS",
		"postgres://me:hunter2@db/x":                  "password",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc":    "private key",
		"xoxb-123456789012-abcdefghij":                "Slack",
		"eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.signature": "web token",
	} {
		_, _, err := project.Parse([]byte("version = 1\n[vars]\nX = '''"+value+"'''"), nil, root)
		if err == nil || !strings.Contains(err.Error(), why) || !strings.Contains(err.Error(), project.LocalFileName) {
			t.Errorf("%q: err = %v, want refused as %s", value, err, why)
		}
	}
	_, _, err := project.Parse([]byte("version = 1\n[vars]\nGITHUB_TOKEN = \"anything\""), nil, root)
	if err == nil || !strings.Contains(err.Error(), "its name") {
		t.Errorf("a token by name: err = %v", err)
	}
	// Plain settings pass, and so does a name with an empty value.
	parse(t, "version = 1\n[vars]\nNODE_ENV = \"development\"\nDATABASE_HOST = \"localhost\"\nAPI_TOKEN = \"\"")
}

func TestLocalFile(t *testing.T) {
	local := `version = 1
[vars]
DATABASE_URL = "postgres://localhost/shop"
[agent.reviewer]
kind = "codex"
args = ["--fast"]
start = false
`
	s, loc, err := project.Parse([]byte(rfcExample), []byte(local), root)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Local || loc.Vars["DATABASE_URL"] != "postgres://localhost/shop" {
		t.Errorf("local = %v, vars %v", s.Local, loc.Vars)
	}
	a := s.Environments[0].Agents[0]
	if !slices.Equal(a.Kinds, []string{"codex"}) || !slices.Equal(a.Args, []string{"--fast"}) || !a.Skip {
		t.Errorf("agent = %+v", a)
	}
	// Local values are the person's: they are not in the spec.
	if s.Environments[0].Vars["DATABASE_URL"] != "" {
		t.Error("a local value is in the spec")
	}

	for text, want := range map[string]string{
		"[vars]\nX = \"1\"":                          "version = 1 is required",
		"version = 1\n[agent.nobody]\nstart = false": "no agent of that name",
		"version = 1\nname = \"x\"":                  "name",
	} {
		if _, _, err := project.Parse([]byte(rfcExample), []byte(text), root); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), project.LocalFileName) {
			t.Errorf("local %q: err = %v, want %q", text, err, want)
		}
	}
}

func TestCommandLine(t *testing.T) {
	got := project.CommandLine([]string{"sh", "-c", "echo hi; it's", "a=b"})
	if want := `sh -c 'echo hi; it'\''s' a=b`; got != want {
		t.Errorf("CommandLine = %s, want %s", got, want)
	}
}

func FuzzParseProject(f *testing.F) {
	f.Add([]byte(rfcExample), []byte(nil))
	f.Add([]byte("version = 1\n[[agent]]\nkind = [\"a\", \"b\"]\nworktree = \"x\""), []byte("version = 1\n[agent.a]\nstart = false"))
	f.Add([]byte("version = 1\n[[environment]]\nid = \"x\"\npath = \"a/../b\""), []byte("version = 1\n[vars]\nA = \"1\""))
	f.Fuzz(func(t *testing.T, data, local []byte) {
		s, _, err := project.Parse(data, local, root)
		if err != nil {
			if !errors.Is(err, project.ErrInvalid) {
				t.Fatalf("a parse error that is not ErrInvalid: %v", err)
			}
			return
		}
		if len(s.Environments) == 0 || s.Environments[0].Root != root {
			t.Fatalf("no main environment at the root: %+v", s)
		}
		for _, e := range s.Environments {
			rel, err := filepath.Rel(root, e.Root)
			if err != nil || !filepath.IsLocal(rel) && rel != "." {
				t.Fatalf("environment %s outside the project: %s", e.ID, e.Root)
			}
		}
	})
}
