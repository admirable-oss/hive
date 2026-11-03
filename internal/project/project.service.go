package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Workspace is what the service reads and changes: the daemon's
// environments, tabs and agents.
type Workspace interface {
	State(ctx context.Context) (State, error)
	CreateEnvironment(ctx context.Context, id, root string, vars map[string]string) error
	SetVars(ctx context.Context, id string, vars map[string]string) error
	// CreateTab opens a tab whose panes get env on top of the
	// environment's, and run through wrap when it is set.
	CreateTab(ctx context.Context, envID string, t Tab, env map[string]string, wrap []string) error
	// StartAgent starts a project's agent in envID, or, when in is set, in
	// that worktree environment of it, with env on top of the
	// environment's. An agent started in a new worktree marks its
	// environment with EnvProjectAgent.
	StartAgent(ctx context.Context, envID string, a Agent, kind, in string, env map[string]string) error
	// RestartAgent starts an exited agent again, in its own pane.
	RestartAgent(ctx context.Context, id string, a Agent, env map[string]string) error
	CloseTab(ctx context.Context, id string) error
}

// Config configures the service.
type Config struct {
	// Dir keeps the trust file.
	Dir       string
	Workspace Workspace
	// Tracked says whether git tracks name in dir, which puts a
	// hive.local.toml under trust; nil tracks nothing.
	Tracked func(dir, name string) bool
	// Toolchains make the environments panes start in; nil starts every
	// pane without one.
	Toolchains *Toolchains
	// Secrets reads secret references; nil refuses them.
	Secrets SecretStore
	// Changed runs after a project's trust changes (nil: nothing).
	Changed func()
}

// Service reads, trusts, plans and applies project files.
type Service struct {
	cfg   Config
	store *Store
	// mu makes trust changes and applying one at a time.
	mu sync.Mutex
}

// NewService builds the service.
func NewService(cfg Config) *Service {
	return &Service{cfg: cfg, store: NewStore(cfg.Dir)}
}

// Find returns the directory of the project file in dir or the nearest
// directory above it.
func Find(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", invalid("%q: an absolute directory is required", dir)
	}
	dir = filepath.Clean(dir)
	for d := dir; ; d = filepath.Dir(d) {
		if info, err := os.Stat(filepath.Join(d, FileName)); err == nil && info.Mode().IsRegular() {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("%w: no %s in %s or above it", ErrNotFound, FileName, dir)
		}
	}
}

// loaded is a project file read from disk.
type loaded struct {
	Project
	spec  Spec
	local Local
	// err is why the file does not check.
	err error
}

// load reads the project at dir (or above). A file that does not parse is
// returned with its Error set.
func (s *Service) load(dir string) (loaded, error) {
	root, err := Find(dir)
	if err != nil {
		return loaded{}, err
	}
	l := loaded{Project: Project{Root: root, File: filepath.Join(root, FileName)}}
	data, err := readFile(l.File)
	if err != nil {
		return loaded{}, err
	}
	h := sha256.New()
	h.Write(data)
	localPath := filepath.Join(root, LocalFileName)
	local, err := readFile(localPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		local = nil
	case err != nil:
		return loaded{}, err
	default:
		l.LocalFile = localPath
		if s.cfg.Tracked != nil && s.cfg.Tracked(root, LocalFileName) {
			// A hive.local.toml in git is the project's, not the person's.
			fmt.Fprintf(h, "\x00%s\x00", LocalFileName)
			h.Write(local)
		}
	}
	spec, loc, err := Parse(data, local, root)
	if err != nil {
		l.Digest = hex.EncodeToString(h.Sum(nil))
		l.Error, l.err = err.Error(), err
		return l, nil
	}
	// The workflows the file brings are trusted with it, at their text.
	for _, path := range WorkflowPaths(root, spec) {
		wf, err := readFile(path)
		if err != nil {
			return loaded{}, err
		}
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "\x00workflow\x00%s\x00%d\x00", filepath.ToSlash(rel), len(wf))
		h.Write(wf)
	}
	l.Digest = hex.EncodeToString(h.Sum(nil))
	l.spec, l.local, l.Spec = spec, loc, &spec
	return l, nil
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%w: %s: larger than %d bytes", ErrInvalid, path, MaxFileBytes)
	}
	return data, nil
}

// Get reads the project at dir, and says whether its text is trusted.
func (s *Service) Get(_ context.Context, dir string) (Project, error) {
	l, err := s.load(dir)
	if err != nil {
		return Project{}, err
	}
	trust, err := s.store.Load()
	l.Trusted = err == nil && trust[l.Root] == l.Digest
	return l.Project, nil
}

// List returns the projects trusted at some text, as they are now.
func (s *Service) List(ctx context.Context) ([]Project, error) {
	trust, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, root := range slices.Sorted(maps.Keys(trust)) {
		p, err := s.Get(ctx, root)
		if err != nil {
			// The file is gone: the record says where it was.
			p = Project{Root: root, File: filepath.Join(root, FileName), Error: err.Error()}
		}
		out = append(out, p)
	}
	return out, nil
}

// Trust trusts the project at dir at its current text, or revokes its
// trust.
func (s *Service) Trust(_ context.Context, dir string, revoke bool) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.load(dir)
	if err != nil {
		return Project{}, err
	}
	if l.Error != "" && !revoke {
		return Project{}, fmt.Errorf("%w: %s does not check, so it cannot be trusted: %s", ErrState, l.File, l.Error)
	}
	trust, err := s.store.Load()
	if err != nil {
		return Project{}, err
	}
	trust = maps.Clone(trust)
	if revoke {
		delete(trust, l.Root)
	} else {
		trust[l.Root] = l.Digest
	}
	if err := s.store.Save(trust); err != nil {
		return Project{}, err
	}
	if s.cfg.Changed != nil {
		s.cfg.Changed()
	}
	l.Trusted = !revoke
	return l.Project, nil
}

// Plan says what up (or, with down, down) would do, and changes nothing.
// It needs no trust: it is what a person reads before trusting.
func (s *Service) Plan(ctx context.Context, dir string, down bool, vars map[string]string) (Plan, error) {
	l, err := s.parsed(dir)
	if err != nil {
		return Plan{}, err
	}
	st, err := s.state(ctx)
	if err != nil {
		return Plan{}, err
	}
	if down {
		return PlanDown(l.spec, st), nil
	}
	return PlanUp(l.spec, l.local, vars, st), nil
}

// Up makes the workspace match the project file, which must be trusted at
// its current text. Nothing changes while the plan has problems; applying
// stops at the first failure.
func (s *Service) Up(ctx context.Context, dir string, vars map[string]string) (Applied, error) {
	return s.apply(ctx, dir, func(l loaded, st State) Plan { return PlanUp(l.spec, l.local, vars, st) })
}

// Down stops the agents and closes the tabs the project file declares.
func (s *Service) Down(ctx context.Context, dir string) (Applied, error) {
	return s.apply(ctx, dir, func(l loaded, st State) Plan { return PlanDown(l.spec, st) })
}

func (s *Service) apply(ctx context.Context, dir string, plan func(loaded, State) Plan) (Applied, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.parsed(dir)
	if err != nil {
		return Applied{}, err
	}
	trust, err := s.store.Load()
	if err != nil {
		return Applied{}, err
	}
	if trust[l.Root] != l.Digest {
		what := "is not trusted"
		if trust[l.Root] != "" {
			what = "changed since it was trusted"
		}
		return Applied{}, fmt.Errorf("%w: %s %s: read it, then run hive project trust", ErrUntrusted, l.File, what)
	}
	st, err := s.state(ctx)
	if err != nil {
		return Applied{}, err
	}
	res := Applied{Plan: plan(l, st)}
	if len(res.Plan.Problems) > 0 {
		return res, fmt.Errorf("%w: %s", ErrState, strings.Join(res.Plan.Problems, "; "))
	}
	if len(res.Plan.Plugins) > 0 {
		return res, fmt.Errorf("%w: the project needs plugins that are not installed: %s (hive up installs them, or hive plugin install)", ErrState, strings.Join(res.Plan.Plugins, ", "))
	}
	// Toolchains and secrets are made and read before anything changes,
	// so a failure leaves the workspace as it was.
	prep, err := s.prepare(ctx, res.Plan.Actions)
	if err != nil {
		return res, err
	}
	for i, a := range res.Plan.Actions {
		if err := s.do(ctx, a, prep[i]); err != nil {
			return res, fmt.Errorf("%s %s%s (after %d of %d steps): %w", a.Op, a.Environment, nameSuffix(a.Name), res.Done, len(res.Plan.Actions), err)
		}
		res.Done++
	}
	return res, nil
}

func nameSuffix(name string) string {
	if name == "" {
		return ""
	}
	return "/" + name
}

// state is the workspace as plans see it.
func (s *Service) state(ctx context.Context) (State, error) {
	st, err := s.cfg.Workspace.State(ctx)
	if err != nil {
		return State{}, err
	}
	if s.cfg.Toolchains != nil {
		st.Toolchain = s.cfg.Toolchains.Resolve
	}
	return st, nil
}

// prepare makes each step's toolchain and reads its secrets: what its new
// panes start with, by the step's index.
func (s *Service) prepare(ctx context.Context, actions []Action) ([]Prepared, error) {
	out := make([]Prepared, len(actions))
	made := map[string]Prepared{}
	values := map[string]string{}
	for i, a := range actions {
		if a.tc.Kind != "" && a.tc.Kind != ToolchainNone {
			k := string(a.tc.Kind) + "\x00" + a.tc.Dir
			p, ok := made[k]
			if !ok {
				if s.cfg.Toolchains == nil {
					return nil, fmt.Errorf("%w: toolchains do not run in this daemon", ErrState)
				}
				var err error
				if p, err = s.cfg.Toolchains.Prepare(ctx, a.tc); err != nil {
					return nil, fmt.Errorf("environment %s: %w", a.Environment, err)
				}
				made[k] = p
			}
			out[i] = Prepared{Env: maps.Clone(p.Env), Wrap: p.Wrap}
		}
		for _, name := range slices.Sorted(maps.Keys(a.refs)) {
			ref := a.refs[name]
			v, ok := values[ref]
			if !ok {
				if s.cfg.Secrets == nil {
					return nil, fmt.Errorf("%w: secrets are not read in this daemon", ErrState)
				}
				var err error
				if v, err = s.cfg.Secrets.Resolve(ctx, ref); err != nil {
					// The error names the reference, never a value.
					return nil, fmt.Errorf("environment %s: secret %s (%s): %w", a.Environment, name, ref, err)
				}
				values[ref] = v
			}
			if out[i].Env == nil {
				out[i].Env = map[string]string{}
			}
			out[i].Env[name] = v
		}
	}
	return out, nil
}

func (s *Service) do(ctx context.Context, a Action, p Prepared) error {
	w := s.cfg.Workspace
	switch a.Op {
	case OpCreateEnvironment:
		return w.CreateEnvironment(ctx, a.Environment, a.Root, a.values)
	case OpSetVars:
		return w.SetVars(ctx, a.Environment, a.values)
	case OpCreateTab:
		return w.CreateTab(ctx, a.Environment, *a.tab, p.Env, p.Wrap)
	case OpStartAgent:
		return w.StartAgent(ctx, a.Environment, *a.agent, a.Kind, a.In, p.Env)
	case OpRestartAgent:
		return w.RestartAgent(ctx, a.ID, *a.agent, p.Env)
	case OpStopAgent:
		return w.CloseTab(ctx, a.tabID)
	case OpCloseTab:
		return w.CloseTab(ctx, a.ID)
	}
	return fmt.Errorf("unknown step %q", a.Op)
}

// Diff says how the running workspace differs from the project file.
func (s *Service) Diff(ctx context.Context, dir string, vars map[string]string) (Diff, error) {
	l, err := s.parsed(dir)
	if err != nil {
		return Diff{}, err
	}
	st, err := s.state(ctx)
	if err != nil {
		return Diff{}, err
	}
	return DiffState(l.spec, l.local, vars, st), nil
}

// Saved is what project.save wrote, or would write.
type Saved struct {
	Project Project  `json:"project"`
	Text    string   `json:"text"`
	Changes []Change `json:"changes"`
	Written bool     `json:"written"`
}

// Save writes the running tabs back to the project file (see SaveText);
// without write it only says what it would write. A file trusted before
// stays trusted at its new text: the person asked for it.
func (s *Service) Save(ctx context.Context, dir string, write bool) (Saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.parsed(dir)
	if err != nil {
		return Saved{}, err
	}
	st, err := s.state(ctx)
	if err != nil {
		return Saved{}, err
	}
	text, changes := SaveText(l.spec, st)
	res := Saved{Project: l.Project, Text: string(text), Changes: changes}
	if changes == nil {
		res.Changes = []Change{}
	}
	if !write || len(changes) == 0 {
		return res, nil
	}
	trust, err := s.store.Load()
	if err != nil {
		return Saved{}, err
	}
	wasTrusted := trust[l.Root] == l.Digest
	if err := writeAtomic(l.File, text); err != nil {
		return Saved{}, err
	}
	res.Written = true
	n, err := s.load(l.Root)
	if err != nil {
		return Saved{}, err
	}
	if n.err != nil {
		return Saved{}, fmt.Errorf("the written file does not check (a bug): %w", n.err)
	}
	if wasTrusted {
		trust = maps.Clone(trust)
		trust[n.Root] = n.Digest
		if err := s.store.Save(trust); err != nil {
			return Saved{}, err
		}
		n.Trusted = true
	}
	res.Project = n.Project
	return res, nil
}

// writeAtomic replaces path with data, keeping its mode.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hive.toml-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// WorkflowFile is a workflow file a trusted project brings, at the digest
// trusted with it.
type WorkflowFile struct {
	Path    string
	Digest  string
	Project string
}

// WorkflowPaths are the workflow files spec declares, absolute and in
// order: each directory's *.toml, and files as named. Missing ones are
// left out.
func WorkflowPaths(root string, spec Spec) []string {
	var out []string
	for _, w := range spec.Workflows {
		p := filepath.Join(root, filepath.FromSlash(w))
		info, err := os.Stat(p)
		switch {
		case err != nil:
			continue
		case info.IsDir():
			matches, _ := filepath.Glob(filepath.Join(p, "*.toml"))
			slices.Sort(matches)
			for _, m := range matches {
				if !slices.Contains(out, m) {
					out = append(out, m)
				}
			}
		case !slices.Contains(out, p):
			out = append(out, p)
		}
	}
	return out
}

// trusted are the projects trusted at their current text.
func (s *Service) trusted() []loaded {
	trust, err := s.store.Load()
	if err != nil {
		return nil
	}
	var out []loaded
	for _, root := range slices.Sorted(maps.Keys(trust)) {
		l, err := s.load(root)
		if err != nil || l.err != nil || l.Root != root || trust[root] != l.Digest {
			continue
		}
		out = append(out, l)
	}
	return out
}

// WorkflowFiles are the workflows trusted projects bring, with their
// digests: the workflow service lets them fire while they have that text.
func (s *Service) WorkflowFiles() []WorkflowFile {
	var out []WorkflowFile
	for _, l := range s.trusted() {
		for _, path := range WorkflowPaths(l.Root, l.spec) {
			data, err := readFile(path)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(data)
			out = append(out, WorkflowFile{Path: path, Digest: hex.EncodeToString(sum[:]), Project: l.Root})
		}
	}
	return out
}

// Gates are the gates the trusted project whose root is repo declares,
// and that root.
func (s *Service) Gates(repo string) ([]Gate, string) {
	for _, l := range s.trusted() {
		if samePath(l.Root, repo) {
			return l.spec.Gates, l.Root
		}
	}
	return nil, ""
}

// parsed loads the project at dir and refuses one that does not check.
func (s *Service) parsed(dir string) (loaded, error) {
	l, err := s.load(dir)
	if err != nil {
		return loaded{}, err
	}
	if l.err != nil {
		return loaded{}, l.err
	}
	return l, nil
}
