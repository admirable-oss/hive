package editor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Config sets up a bridge.
type Config struct {
	Hive    Hive
	Name    string // the bridge's name (hive)
	Version string // its version
	// LookPath finds agents' commands, for agent.kinds (default
	// exec.LookPath).
	LookPath func(string) (string, error)
	// Now is the clock (default time.Now).
	Now    func() time.Time
	Logger *slog.Logger
	// Reconnect is how long the bridge waits before subscribing again
	// after the daemon went away (default 500ms, doubling to 5s).
	Reconnect time.Duration
}

// Server is one editor's bridge.
type Server struct {
	cfg    Config
	hive   Hive
	router *protocol.Router

	mu      sync.Mutex
	conn    *conn
	folders []string
	client  Client
	level   int
	views   map[string]*view
	nextID  int
	cat     *catalog
	catAt   time.Time
	states  map[string]string // agent ID → its last state, for notices
	checks  map[string]bool   // branch ID → its gates were running
	files   []FileEdit        // what file.changed last said
	filesAt time.Time
}

// New returns a bridge.
func New(cfg Config) *Server {
	if cfg.Name == "" {
		cfg.Name = "hive"
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Reconnect <= 0 {
		cfg.Reconnect = 500 * time.Millisecond
	}
	s := &Server{cfg: cfg, hive: cfg.Hive, views: map[string]*view{}, states: map[string]string{}, checks: map[string]bool{}}
	s.router = protocol.NewRouter()
	s.register(s.router)
	return s
}

// Router is the bridge's methods, for its schema.
func (s *Server) Router() *protocol.Router { return s.router }

// Serve speaks the editor protocol (one JSON message per line) on r and
// w until the editor closes r or ctx ends. Agents keep running.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	return s.serve(ctx, newLineCodec(r, w), routerHandler(s.router))
}

func (s *Server) serve(ctx context.Context, c codec, h handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cn := newConn(c, h)
	s.mu.Lock()
	s.conn = cn
	s.mu.Unlock()
	pumped := make(chan struct{})
	go func() { defer close(pumped); s.pump(ctx) }()
	err := cn.serve(ctx)
	cancel()
	<-pumped
	s.closeViews("closed")
	return err
}

// notify sends a notification to the editor, if one is connected.
func (s *Server) notify(method string, params any) {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		_ = c.notify(method, params)
	}
}

func (s *Server) register(r *protocol.Router) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }
	m("initialize", protocol.Method(s.initialize))
	m("ping", protocol.Method(func(context.Context, Empty) (Empty, error) { return Empty{}, nil }))
	m("editor.folders", protocol.Method(func(_ context.Context, p FoldersParams) (Empty, error) {
		s.setFolders(p.Folders)
		return Empty{}, nil
	}))
	m("editor.schema", protocol.Method(func(context.Context, Empty) (protocol.Schema, error) {
		return s.router.Schema(), nil
	}))
	s.registerAgents(m)
	s.registerTerminals(m)
	s.registerReview(m)
	s.registerFiles(m)
}

func (s *Server) initialize(ctx context.Context, p InitializeParams) (InitializeResult, error) {
	s.mu.Lock()
	s.client = p.Client
	s.mu.Unlock()
	s.setFolders(p.Folders)
	st, err := s.hive.Status(ctx)
	if err != nil {
		return InitializeResult{}, unavailable(err)
	}
	s.mu.Lock()
	s.level = st.APILevel
	s.mu.Unlock()
	return InitializeResult{
		Protocol: ProtocolVersion,
		Server:   ServerInfo{Name: s.cfg.Name, Version: s.cfg.Version},
		Daemon:   DaemonInfo{Version: st.Version, APILevel: st.APILevel},
		Missing:  missing(st.APILevel),
	}, nil
}

// missing lists the features a daemon at that API level lacks.
func missing(level int) []string {
	out := []string{}
	if level < 13 {
		out = append(out, FeatureReview)
	}
	if level < 19 {
		out = append(out, FeatureFiles, FeatureHunks)
	}
	return out
}

func (s *Server) has(feature string) bool {
	s.mu.Lock()
	level := s.level
	s.mu.Unlock()
	return level == 0 || !slices.Contains(missing(level), feature) // 0: not initialized yet, try
}

func (s *Server) setFolders(folders []string) {
	var clean []string
	for _, f := range folders {
		if f = cleanPath(f); f != "" && !slices.Contains(clean, f) {
			clean = append(clean, f)
		}
	}
	s.mu.Lock()
	s.folders = clean
	s.mu.Unlock()
}

func (s *Server) foldersFor(folder string) []string {
	if folder != "" {
		return []string{cleanPath(folder)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.folders)
}

// cleanPath makes an absolute, cleaned path with links resolved; "" for a
// relative one.
func cleanPath(p string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// catalog is what the bridge knows about the workspace, to describe
// agents: environments, branches and tasks.
type catalog struct {
	envs     map[string]hiveapi.Environment
	branches []hiveapi.Branch
	byEnv    map[string]hiveapi.Branch // a branch by its worktree's environment
	tasks    map[string]TaskRef        // by agent ID
}

// catalogTTL is how long a catalog is good for when no event says it
// changed.
const catalogTTL = 2 * time.Second

func (s *Server) catalog(ctx context.Context) *catalog {
	s.mu.Lock()
	c, at := s.cat, s.catAt
	s.mu.Unlock()
	if c != nil && s.cfg.Now().Sub(at) < catalogTTL {
		return c
	}
	c = &catalog{envs: map[string]hiveapi.Environment{}, byEnv: map[string]hiveapi.Branch{}, tasks: map[string]TaskRef{}}
	if envs, err := s.hive.Environments(ctx); err == nil {
		for _, e := range envs {
			c.envs[e.ID] = e
		}
	}
	if s.has(FeatureReview) {
		if list, err := s.hive.Branches(ctx, "", false); err == nil {
			c.branches = list
			for _, b := range list {
				if b.EnvironmentID != "" {
					c.byEnv[b.EnvironmentID] = b
				}
			}
		}
	}
	if plans, err := s.hive.Plans(ctx); err == nil {
		for _, p := range plans {
			if p.State != hiveapi.PlanRunning {
				continue
			}
			full, err := s.hive.Plan(ctx, p.ID)
			if err != nil {
				continue
			}
			for _, t := range full.Tasks {
				if t.AgentID != "" {
					c.tasks[t.AgentID] = TaskRef{Plan: full.ID, ID: t.Spec.ID, Title: t.Spec.Title, State: string(t.State)}
				}
			}
		}
	}
	s.mu.Lock()
	s.cat, s.catAt = c, s.cfg.Now()
	s.mu.Unlock()
	return c
}

// forget drops the catalog, after an event said it changed.
func (s *Server) forget() {
	s.mu.Lock()
	s.cat = nil
	s.mu.Unlock()
}

// unavailable says the daemon cannot be reached.
func unavailable(err error) error {
	if errors.Is(err, hiveapi.ErrUnavailable) {
		return protocol.NewError(protocol.ErrorCodeUnavailable, fmt.Errorf("no Hive daemon answers: run `hive daemon start` (%w)", err))
	}
	return err
}

// invalid is an invalid_params error.
func invalid(format string, args ...any) error {
	return protocol.NewError(protocol.ErrorCodeInvalidParams, fmt.Errorf(format, args...))
}

// notFound is a not_found error.
func notFound(format string, args ...any) error {
	return protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf(format, args...))
}
