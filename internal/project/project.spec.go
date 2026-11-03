package project

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// maxPanes bounds the panes a project starts, as layout.apply does.
const maxPanes = 256

var (
	idPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	varPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// kindPattern is an agent manifest's ID.
	kindPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// The file as written. Keys mirror the documented format; anything else
// is an error.
type fileProject struct {
	Version     int               `toml:"version"`
	Name        string            `toml:"name"`
	Toolchain   string            `toml:"toolchain"`
	Workflows   []string          `toml:"workflows"`
	Plugins     []string          `toml:"plugins"`
	Vars        map[string]string `toml:"vars"`
	Secrets     map[string]string `toml:"secrets"`
	Env         fileEnv           `toml:"env"`
	Gate        []fileGate        `toml:"gate"`
	Tab         []fileTab         `toml:"tab"`
	Agent       []fileAgent       `toml:"agent"`
	Environment []fileEnvironment `toml:"environment"`
}

type fileGate struct {
	Name    string `toml:"name"`
	Command any    `toml:"command"`
	Timeout string `toml:"timeout"`
}

type fileEnv struct {
	Required []string `toml:"required"`
	Optional []string `toml:"optional"`
}

type fileEnvironment struct {
	ID        string            `toml:"id"`
	Path      string            `toml:"path"`
	Toolchain string            `toml:"toolchain"`
	Vars      map[string]string `toml:"vars"`
	Secrets   map[string]string `toml:"secrets"`
	Env       fileEnv           `toml:"env"`
	Tab       []fileTab         `toml:"tab"`
	Agent     []fileAgent       `toml:"agent"`
}

type fileTab struct {
	Name  string     `toml:"name"`
	Split string     `toml:"split"`
	Pane  []filePane `toml:"pane"`
}

type filePane struct {
	Name    string `toml:"name"`
	Command any    `toml:"command"`
	Cwd     string `toml:"cwd"`
}

type fileAgent struct {
	Name     string   `toml:"name"`
	Kind     any      `toml:"kind"`
	Isolate  bool     `toml:"isolate"`
	Worktree string   `toml:"worktree"`
	Base     string   `toml:"base"`
	Args     []string `toml:"args"`
}

type fileLocal struct {
	Version   int                       `toml:"version"`
	Toolchain string                    `toml:"toolchain"`
	Vars      map[string]string         `toml:"vars"`
	Secrets   map[string]string         `toml:"secrets"`
	Agent     map[string]fileLocalAgent `toml:"agent"`
}

type fileLocalAgent struct {
	Kind  any      `toml:"kind"`
	Args  []string `toml:"args"`
	Start *bool    `toml:"start"`
}

// Local is hive.local.toml: the person's own values and preferences.
type Local struct {
	// Vars are values for every environment, over the project's; Secrets
	// are references to the person's store, by variable.
	Vars    map[string]string
	Secrets map[string]string
	// Toolchain replaces every environment's, when set.
	Toolchain Toolchain
}

// Parse reads a project file at root (the directory it is in, absolute)
// and the hive.local.toml beside it, which may be nil. The spec it returns
// is checked: one that parses can be planned.
func Parse(data, local []byte, root string) (Spec, Local, error) {
	var f fileProject
	if err := decode(data, FileName, &f); err != nil {
		return Spec{}, Local{}, err
	}
	spec, err := f.spec(root)
	if err != nil {
		return Spec{}, Local{}, fmt.Errorf("%s: %w", FileName, err)
	}
	spec.file = f
	var loc Local
	if local != nil {
		var lf fileLocal
		if err := decode(local, LocalFileName, &lf); err != nil {
			return Spec{}, Local{}, err
		}
		if loc, err = lf.apply(&spec); err != nil {
			return Spec{}, Local{}, fmt.Errorf("%s: %w", LocalFileName, err)
		}
		spec.Local = true
	}
	return spec, loc, nil
}

// decode reads TOML strictly, with errors that point at the line.
func decode(data []byte, source string, v any) error {
	if len(data) > MaxFileBytes {
		return fmt.Errorf("%w: %s: larger than %d bytes", ErrInvalid, source, MaxFileBytes)
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if se, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return fmt.Errorf("%w: %s: %s", ErrInvalid, source, strings.TrimSpace(se.String()))
		}
		if de, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, col := de.Position()
			return fmt.Errorf("%w: %s:%d:%d: %s", ErrInvalid, source, row, col, de.Error())
		}
		return fmt.Errorf("%w: %s: %w", ErrInvalid, source, err)
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func checkVersion(v int) error {
	switch {
	case v == 0:
		return invalid("version = %d is required (the project format this file is written for)", Version)
	case v > Version:
		return invalid("written for project format %d; this hive reads format %d (update hive)", v, Version)
	case v < 0:
		return invalid("version %d", v)
	}
	return nil
}

func (f fileProject) spec(root string) (Spec, error) {
	if err := checkVersion(f.Version); err != nil {
		return Spec{}, err
	}
	name := f.Name
	if name == "" {
		name = NameFromDir(root)
	}
	if !idPattern.MatchString(name) {
		return Spec{}, invalid("name %q: letters, digits, '.', '_' and '-', at most 64, starting with a letter or digit (it is the main environment's ID)", name)
	}
	if err := checkVars(f.Vars, "vars"); err != nil {
		return Spec{}, err
	}
	if err := checkNames(f.Env, "env"); err != nil {
		return Spec{}, err
	}
	if err := checkSecrets(f.Secrets, "secrets"); err != nil {
		return Spec{}, err
	}
	tc, err := toolchain(f.Toolchain, "toolchain")
	if err != nil {
		return Spec{}, err
	}
	main := fileEnvironment{ID: name, Path: ".", Tab: f.Tab, Agent: f.Agent}
	s := Spec{Version: f.Version, Name: name, Toolchain: tc}
	if s.Gates, err = gates(f.Gate); err != nil {
		return Spec{}, err
	}
	for i, w := range f.Workflows {
		p, err := localPath(w)
		if err != nil {
			return Spec{}, invalid("workflows[%d]: %v", i, err)
		}
		if slices.Contains(s.Workflows, p) {
			return Spec{}, invalid("workflows: %q is named twice", p)
		}
		s.Workflows = append(s.Workflows, p)
	}
	for _, id := range f.Plugins {
		if !kindPattern.MatchString(id) {
			return Spec{}, invalid("plugins: %q is not a plugin ID (hive plugin search)", id)
		}
		if slices.Contains(s.Plugins, id) {
			return Spec{}, invalid("plugins: %s is named twice", id)
		}
		s.Plugins = append(s.Plugins, id)
	}
	ids, paths := map[string]bool{}, map[string]string{}
	panes := 0
	for i, fe := range append([]fileEnvironment{main}, f.Environment...) {
		where := "environment[" + fmt.Sprint(i-1) + "]"
		if i == 0 {
			where = "the main environment"
		}
		e, err := fe.environment(root, where, i == 0)
		if err != nil {
			return Spec{}, err
		}
		if ids[e.ID] {
			return Spec{}, invalid("%s: id %q is used twice", where, e.ID)
		}
		if other, ok := paths[e.Path]; ok {
			return Spec{}, invalid("%s: path %q is environment %q's already", where, e.Path, other)
		}
		ids[e.ID], paths[e.Path] = true, e.ID
		// The project's vars and names hold in every environment; an
		// environment's own come on top.
		vars := maps.Clone(f.Vars)
		if vars == nil {
			vars = map[string]string{}
		}
		maps.Copy(vars, e.Vars)
		if len(vars) > 0 {
			e.Vars = vars
		}
		secrets := maps.Clone(f.Secrets)
		if secrets == nil {
			secrets = map[string]string{}
		}
		maps.Copy(secrets, e.Secrets)
		e.Secrets = nil
		if len(secrets) > 0 {
			e.Secrets = secrets
		}
		if e.Toolchain == "" {
			e.Toolchain = s.Toolchain
		}
		e.Required = union(f.Env.Required, e.Required)
		e.Optional = slices.DeleteFunc(union(f.Env.Optional, e.Optional), func(n string) bool { return slices.Contains(e.Required, n) })
		for _, t := range e.Tabs {
			panes += len(t.Panes)
		}
		panes += len(e.Agents)
		s.Environments = append(s.Environments, e)
	}
	if panes > maxPanes {
		return Spec{}, invalid("%d panes; at most %d", panes, maxPanes)
	}
	return s, nil
}

func (fe fileEnvironment) environment(root, where string, main bool) (Environment, error) {
	e := Environment{ID: fe.ID, Path: fe.Path}
	if !main {
		switch {
		case fe.ID == "":
			return Environment{}, invalid("%s: id is required", where)
		case !idPattern.MatchString(fe.ID):
			return Environment{}, invalid("%s: id %q: letters, digits, '.', '_' and '-', at most 64, starting with a letter or digit", where, fe.ID)
		case fe.Path == "":
			return Environment{}, invalid("%s: path is required (a directory of the repository)", where)
		}
		p, err := localPath(fe.Path)
		if err != nil {
			return Environment{}, invalid("%s: path %v", where, err)
		}
		e.Path = p
		where = "environment " + fe.ID
	}
	e.Root = filepath.Join(root, filepath.FromSlash(e.Path))
	if err := checkVars(fe.Vars, where+": vars"); err != nil {
		return Environment{}, err
	}
	if err := checkNames(fe.Env, where+": env"); err != nil {
		return Environment{}, err
	}
	if err := checkSecrets(fe.Secrets, where+": secrets"); err != nil {
		return Environment{}, err
	}
	var err error
	if e.Toolchain, err = toolchain(fe.Toolchain, where+": toolchain"); err != nil {
		return Environment{}, err
	}
	if len(fe.Vars) > 0 {
		e.Vars = maps.Clone(fe.Vars)
	}
	if len(fe.Secrets) > 0 {
		e.Secrets = maps.Clone(fe.Secrets)
	}
	e.Required, e.Optional = fe.Env.Required, fe.Env.Optional
	names := map[string]string{}
	for i, ft := range fe.Tab {
		t, err := ft.tab(fmt.Sprintf("%s: tab[%d]", where, i))
		if err != nil {
			return Environment{}, err
		}
		if names[t.Name] != "" {
			return Environment{}, invalid("%s: tab %q: the name is used twice", where, t.Name)
		}
		names[t.Name] = "tab"
		e.Tabs = append(e.Tabs, t)
	}
	for i, fa := range fe.Agent {
		a, err := fa.agent(fmt.Sprintf("%s: agent[%d]", where, i))
		if err != nil {
			return Environment{}, err
		}
		if what := names[a.Name]; what != "" {
			return Environment{}, invalid("%s: agent %q: a %s has that name (agents get a tab of their own, named after them)", where, a.Name, what)
		}
		names[a.Name] = "agent"
		e.Agents = append(e.Agents, a)
	}
	return e, nil
}

func (ft fileTab) tab(where string) (Tab, error) {
	t := Tab{Name: ft.Name, Split: Split(ft.Split)}
	switch {
	case t.Name == "":
		return Tab{}, invalid("%s: name is required", where)
	case strings.TrimSpace(t.Name) != t.Name || strings.ContainsAny(t.Name, "\n\r\t"):
		return Tab{}, invalid("%s: name %q has surrounding spaces or control characters", where, t.Name)
	case len(ft.Pane) == 0:
		return Tab{}, invalid("%s (%s): at least one [[tab.pane]] is required", where, t.Name)
	}
	where = "tab " + t.Name
	switch t.Split {
	case "":
		t.Split = SplitHorizontal
	case SplitHorizontal, SplitVertical:
	default:
		return Tab{}, invalid("%s: split %q: horizontal (side by side) or vertical (stacked)", where, ft.Split)
	}
	for i, fp := range ft.Pane {
		p := Pane{Name: fp.Name}
		var err error
		if p.Command, err = command(fp.Command); err != nil {
			return Tab{}, invalid("%s: pane[%d]: command %v", where, i, err)
		}
		if fp.Cwd != "" {
			if p.Cwd, err = localPath(fp.Cwd); err != nil {
				return Tab{}, invalid("%s: pane[%d]: cwd %v", where, i, err)
			}
		}
		t.Panes = append(t.Panes, p)
	}
	return t, nil
}

func (fa fileAgent) agent(where string) (Agent, error) {
	kinds, err := kindList(fa.Kind)
	if err != nil {
		return Agent{}, invalid("%s: kind %v", where, err)
	}
	if len(kinds) == 0 {
		return Agent{}, invalid("%s: kind is required (an agent kind, or a list in order of preference)", where)
	}
	a := Agent{Name: fa.Name, Kinds: kinds, Isolate: fa.Isolate, Worktree: fa.Worktree, Base: fa.Base, Args: fa.Args}
	if a.Name == "" {
		a.Name = kinds[0]
	}
	switch {
	case !idPattern.MatchString(a.Name):
		return Agent{}, invalid("%s: name %q: letters, digits, '.', '_' and '-', at most 64, starting with a letter or digit", where, a.Name)
	case a.Isolate && a.Worktree != "":
		return Agent{}, invalid("agent %s: isolate and worktree: one or the other (worktree names the branch)", a.Name)
	case a.Base != "" && !a.Isolate && a.Worktree == "":
		return Agent{}, invalid("agent %s: base is where a worktree's branch starts: set isolate or worktree too", a.Name)
	case strings.HasPrefix(a.Worktree, "-") || strings.ContainsAny(a.Worktree, " \t\n~^:?*[\\"):
		return Agent{}, invalid("agent %s: worktree %q is not a branch name", a.Name, a.Worktree)
	}
	return a, nil
}

func (lf fileLocal) apply(s *Spec) (Local, error) {
	if err := checkVersion(lf.Version); err != nil {
		return Local{}, err
	}
	for k := range lf.Vars {
		if !varPattern.MatchString(k) {
			return Local{}, invalid("vars: %q is not a variable name", k)
		}
	}
	if err := checkSecrets(lf.Secrets, "secrets"); err != nil {
		return Local{}, err
	}
	tc, err := toolchain(lf.Toolchain, "toolchain")
	if err != nil {
		return Local{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(lf.Agent)) {
		la := lf.Agent[name]
		kinds, err := kindList(la.Kind)
		if err != nil {
			return Local{}, invalid("agent.%s: kind %v", name, err)
		}
		found := false
		for i := range s.Environments {
			for j := range s.Environments[i].Agents {
				a := &s.Environments[i].Agents[j]
				if a.Name != name {
					continue
				}
				found = true
				if len(kinds) > 0 {
					a.Kinds = kinds
				}
				if la.Args != nil {
					a.Args = la.Args
				}
				if la.Start != nil {
					a.Skip = !*la.Start
				}
			}
		}
		if !found {
			return Local{}, invalid("agent.%s: %s declares no agent of that name", name, FileName)
		}
	}
	return Local{Vars: lf.Vars, Secrets: lf.Secrets, Toolchain: tc}, nil
}

// NameFromDir is the name of a project whose file gives none: its
// directory's, made a valid environment ID.
func NameFromDir(dir string) string {
	base := filepath.Base(dir)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.TrimLeft(b.String(), "._-")
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		return "project"
	}
	return name
}

// localPath checks a path inside the repository, in slash form.
func localPath(p string) (string, error) {
	p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if strings.HasPrefix(p, "/") || !filepath.IsLocal(filepath.FromSlash(p)) {
		return "", fmt.Errorf("%q: a directory inside the repository, relative to hive.toml", p)
	}
	return p, nil
}

func command(v any) ([]string, error) {
	switch c := v.(type) {
	case nil:
		return nil, nil
	case string:
		words := strings.Fields(c)
		for i, w := range words {
			words[i] = fmt.Sprintf("%q", w)
		}
		return nil, fmt.Errorf("%q: a list of arguments, such as [%s]", c, strings.Join(words, ", "))
	case []any:
		out := make([]string, 0, len(c))
		for _, x := range c {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("has %v, which is not a string", x)
			}
			out = append(out, s)
		}
		if len(out) > 0 && out[0] == "" {
			return nil, errors.New("starts with an empty program")
		}
		return out, nil
	}
	return nil, fmt.Errorf("%v: a list of arguments", v)
}

func kindList(v any) ([]string, error) {
	var kinds []string
	switch k := v.(type) {
	case nil:
		return nil, nil
	case string:
		kinds = []string{k}
	case []any:
		for _, x := range k {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("has %v, which is not a string", x)
			}
			kinds = append(kinds, s)
		}
	default:
		return nil, fmt.Errorf("%v: an agent kind, or a list of them", v)
	}
	for _, k := range kinds {
		if !kindPattern.MatchString(k) {
			return nil, fmt.Errorf("%q is not an agent kind (see hive agent manifests)", k)
		}
	}
	return kinds, nil
}

func checkVars(vars map[string]string, where string) error {
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		if !varPattern.MatchString(k) {
			return invalid("%s: %q is not a variable name", where, k)
		}
		if why := secretLike(k, vars[k]); why != "" {
			return invalid("%s: %s looks like a secret (%s): declare it in [env] and set its value in %s or your shell", where, k, why, LocalFileName)
		}
	}
	return nil
}

func checkNames(e fileEnv, where string) error {
	seen := map[string]bool{}
	for _, n := range append(slices.Clone(e.Required), e.Optional...) {
		if !varPattern.MatchString(n) {
			return invalid("%s: %q is not a variable name", where, n)
		}
		if seen[n] {
			return invalid("%s: %s is named twice", where, n)
		}
		seen[n] = true
	}
	return nil
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// secretPatterns are token formats that never belong in a file in git.
var secretPatterns = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}`), "a GitHub token"},
	{regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`), "a GitLab token"},
	{regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`), "a Slack token"},
	{regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), "an AWS access key"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), "a Google API key"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}|\b[rs]k_(live|test)_[A-Za-z0-9]{10,}`), "an API key"},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "a private key"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.`), "a JSON web token"},
	{regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`), "a URL with a password"},
}

// secretSuffixes name variables that hold secrets.
var secretSuffixes = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "PRIVATE_KEY", "CREDENTIALS"}

// secretLike says why a variable looks like a secret, or "".
func secretLike(name, value string) string {
	for _, p := range secretPatterns {
		if p.re.MatchString(value) {
			return p.what
		}
	}
	upper := strings.ToUpper(name)
	for _, s := range secretSuffixes {
		if value != "" && (upper == s || strings.HasSuffix(upper, "_"+s)) {
			return "its name"
		}
	}
	return ""
}

func toolchain(v, where string) (Toolchain, error) {
	t := Toolchain(v)
	if v == "" || slices.Contains(ToolchainKinds, t) {
		return t, nil
	}
	names := make([]string, len(ToolchainKinds))
	for i, x := range ToolchainKinds {
		names[i] = string(x)
	}
	return "", invalid("%s %q: one of %s", where, v, strings.Join(names, ", "))
}

func gates(fg []fileGate) ([]Gate, error) {
	var out []Gate
	for i, g := range fg {
		where := fmt.Sprintf("gate[%d]", i)
		switch {
		case g.Name == "":
			return nil, invalid("%s: name is required", where)
		case strings.TrimSpace(g.Name) != g.Name || strings.ContainsAny(g.Name, "\n\r\t"):
			return nil, invalid("%s: name %q has surrounding spaces or control characters", where, g.Name)
		case slices.ContainsFunc(out, func(x Gate) bool { return x.Name == g.Name }):
			return nil, invalid("gate %q: the name is used twice", g.Name)
		}
		cmd, err := command(g.Command)
		if err != nil {
			return nil, invalid("gate %s: command %v", g.Name, err)
		}
		if len(cmd) == 0 {
			return nil, invalid("gate %s: command is required", g.Name)
		}
		gate := Gate{Name: g.Name, Command: cmd}
		if g.Timeout != "" {
			d, err := time.ParseDuration(g.Timeout)
			if err != nil || d < time.Second || d > 24*time.Hour {
				return nil, invalid("gate %s: timeout %q: a duration from 1s to 24h, such as \"10m\"", g.Name, g.Timeout)
			}
			gate.TimeoutMS = d.Milliseconds()
		}
		out = append(out, gate)
	}
	return out, nil
}

func checkSecrets(refs map[string]string, where string) error {
	for _, k := range slices.Sorted(maps.Keys(refs)) {
		if !varPattern.MatchString(k) {
			return invalid("%s: %q is not a variable name", where, k)
		}
		if err := CheckSecretRef(refs[k]); err != nil {
			return invalid("%s: %s: %v", where, k, err)
		}
	}
	return nil
}
