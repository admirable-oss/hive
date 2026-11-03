package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// TemplateInfo describes a built-in template.
type TemplateInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Templates are the built-in project templates. Plugins add theirs
// ([[contributes.templates]]), and any project file can be one.
var Templates = []TemplateInfo{
	{"blank", "a tab with a shell (and the dev server, when there is one), the toolchain and the tests as a gate"},
	{"agents", "blank, with a reviewer agent and a fixer agent in a worktree of its own"},
	{"test-watcher", "blank, with the tests running in a tab of their own and a fixer agent beside them"},
	{"monorepo", "an environment per package, each with a shell and an agent"},
}

// templateHeader opens every file a template writes.
const templateHeader = `# Hive project file. ` + "`hive up`" + ` starts what it declares, ` + "`hive down`" + ` stops it,
# and ` + "`hive up --plan`" + ` shows what it would do. Your own values go in
# hive.local.toml, which git should ignore. See: hive project --help
`

// Template writes the built-in template name for a project at root,
// fitted to what is there: its language's test and dev commands, its
// toolchain, its packages.
func Template(name, root string) ([]byte, error) {
	f := fileProject{Version: Version}
	d := detect(root)
	if d.toolchain {
		f.Toolchain = string(ToolchainAuto)
	}
	if len(d.test) > 0 {
		f.Gate = []fileGate{{Name: "test", Command: d.test}}
	}
	dev := fileTab{Name: "dev", Pane: []filePane{{Name: "shell"}}}
	if len(d.dev) > 0 {
		dev.Pane = append([]filePane{{Name: "server", Command: d.dev}}, dev.Pane...)
	}
	reviewer := fileAgent{Name: "reviewer", Kind: []any{"claude", "codex"}}
	fixer := fileAgent{Name: "fixer", Kind: []any{"codex", "claude"}, Isolate: true}
	switch name {
	case "blank":
		f.Tab = []fileTab{dev}
	case "agents":
		f.Tab = []fileTab{dev}
		f.Agent = []fileAgent{reviewer, fixer}
	case "test-watcher":
		f.Tab = []fileTab{dev}
		if watch := d.watch(); len(watch) > 0 {
			f.Tab = append(f.Tab, fileTab{Name: "tests", Pane: []filePane{{Name: "tests", Command: watch}}})
		}
		f.Agent = []fileAgent{fixer}
	case "monorepo":
		f.Tab = []fileTab{{Name: "dev", Pane: []filePane{{Name: "shell"}}}}
		used := map[string]bool{NameFromDir(root): true}
		for _, pkg := range d.packages {
			id := NameFromDir(root) + "-" + NameFromDir(pkg)
			for n := 2; used[id]; n++ {
				id = fmt.Sprintf("%s-%s-%d", NameFromDir(root), NameFromDir(pkg), n)
			}
			used[id] = true
			f.Environment = append(f.Environment, fileEnvironment{
				ID: id, Path: pkg,
				Tab:   []fileTab{{Name: "shell", Pane: []filePane{{}}}},
				Agent: []fileAgent{{Name: "agent", Kind: []any{"claude", "codex"}}},
			})
		}
		if len(f.Environment) == 0 {
			return nil, fmt.Errorf("%w: monorepo: no packages found under %s (directories with go.mod, package.json, Cargo.toml or pyproject.toml)", ErrInvalid, root)
		}
	default:
		return nil, fmt.Errorf("%w: no template %q (built in: %s)", ErrInvalid, name, templateNames())
	}
	text := append([]byte(templateHeader+"\n"), Render(f)...)
	// What a template writes must check, as hive up reads it.
	if _, _, err := Parse(text, nil, root); err != nil {
		return nil, fmt.Errorf("template %s does not check (a bug): %w", name, err)
	}
	return text, nil
}

func templateNames() string {
	names := make([]string, len(Templates))
	for i, t := range Templates {
		names[i] = t.Name
	}
	return strings.Join(names, ", ")
}

// detected is what a directory says about the project in it.
type detected struct {
	test, dev []string
	toolchain bool
	lang      string
	packages  []string
}

func (d detected) watch() []string {
	switch d.lang {
	case "node":
		return []string{"npm", "test", "--", "--watch"}
	case "rust":
		return []string{"cargo", "watch", "-x", "test"}
	}
	if len(d.test) == 0 {
		return nil
	}
	return []string{"sh", "-c", "while true; do " + CommandLine(d.test) + "; sleep 10; done"}
}

func detect(root string) detected {
	var d detected
	has := func(name string) bool { return isFile(filepath.Join(root, name)) }
	switch {
	case has("go.mod"):
		d.lang, d.test = "go", []string{"go", "test", "./..."}
	case has("package.json"):
		d.lang = "node"
		scripts := packageScripts(filepath.Join(root, "package.json"))
		if _, ok := scripts["test"]; ok {
			d.test = []string{"npm", "test"}
		}
		if _, ok := scripts["dev"]; ok {
			d.dev = []string{"npm", "run", "dev"}
		}
	case has("Cargo.toml"):
		d.lang, d.test = "rust", []string{"cargo", "test"}
	case has("pyproject.toml"), has("pytest.ini"), has("setup.py"):
		d.lang, d.test = "python", []string{"pytest"}
	case has("Makefile") && makeHasTest(filepath.Join(root, "Makefile")):
		d.test = []string{"make", "test"}
	}
	for _, m := range markers {
		if slices.ContainsFunc(m.files, func(f string) bool { return has(filepath.FromSlash(f)) }) {
			d.toolchain = true
		}
	}
	d.packages = findPackages(root)
	return d
}

func packageScripts(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	_ = json.Unmarshal(data, &pkg)
	return pkg.Scripts
}

func makeHasTest(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "test:") {
			return true
		}
	}
	return false
}

// packageDirs are where monorepos keep their packages, besides the root.
var packageDirs = []string{"packages", "apps", "services", "libs", "crates", "modules"}

// findPackages are a monorepo's packages: directories with a manifest of
// their own, one level under the root or under the usual parents, in
// slash form, at most 32.
func findPackages(root string) []string {
	manifests := []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml"}
	isPackage := func(dir string) bool {
		return slices.ContainsFunc(manifests, func(m string) bool { return isFile(filepath.Join(dir, m)) })
	}
	var out []string
	scan := func(parent string) {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(parent)))
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "target" {
				continue
			}
			rel := name
			if parent != "" {
				rel = parent + "/" + name
			}
			if len(out) < 32 && isPackage(filepath.Join(root, filepath.FromSlash(rel))) && !slices.Contains(out, rel) {
				out = append(out, rel)
			}
		}
	}
	scan("")
	for _, p := range packageDirs {
		scan(p)
	}
	slices.Sort(out)
	return out
}
