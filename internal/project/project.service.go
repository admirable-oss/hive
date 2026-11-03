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
	CreateTab(ctx context.Context, envID string, t Tab) error
	// StartAgent starts a project's agent in envID, or, when in is set, in
	// that worktree environment of it. An agent started in a new worktree
	// marks its environment with EnvProjectAgent.
	StartAgent(ctx context.Context, envID string, a Agent, kind, in string) error
	// RestartAgent starts an exited agent again, in its own pane.
	RestartAgent(ctx context.Context, id string, a Agent) error
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
	l.Digest = hex.EncodeToString(h.Sum(nil))
	spec, loc, err := Parse(data, local, root)
	if err != nil {
		l.Error, l.err = err.Error(), err
		return l, nil
	}
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
	st, err := s.cfg.Workspace.State(ctx)
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
	st, err := s.cfg.Workspace.State(ctx)
	if err != nil {
		return Applied{}, err
	}
	res := Applied{Plan: plan(l, st)}
	if len(res.Plan.Problems) > 0 {
		return res, fmt.Errorf("%w: %s", ErrState, strings.Join(res.Plan.Problems, "; "))
	}
	for _, a := range res.Plan.Actions {
		if err := s.do(ctx, a); err != nil {
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

func (s *Service) do(ctx context.Context, a Action) error {
	w := s.cfg.Workspace
	switch a.Op {
	case OpCreateEnvironment:
		return w.CreateEnvironment(ctx, a.Environment, a.Root, a.values)
	case OpSetVars:
		return w.SetVars(ctx, a.Environment, a.values)
	case OpCreateTab:
		return w.CreateTab(ctx, a.Environment, *a.tab)
	case OpStartAgent:
		return w.StartAgent(ctx, a.Environment, *a.agent, a.Kind, a.In)
	case OpRestartAgent:
		return w.RestartAgent(ctx, a.ID, *a.agent)
	case OpStopAgent:
		return w.CloseTab(ctx, a.tabID)
	case OpCloseTab:
		return w.CloseTab(ctx, a.ID)
	}
	return fmt.Errorf("unknown step %q", a.Op)
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
