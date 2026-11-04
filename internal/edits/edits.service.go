package edits

import (
	"cmp"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
)

// Agents lists the agents that run.
type Agents interface {
	List(ctx context.Context, all bool) ([]agent.Agent, error)
}

// Environments finds an agent's directory.
type Environments interface {
	Get(ctx context.Context, id string) (environment.Environment, error)
}

// Git reads working trees.
type Git interface {
	Root(ctx context.Context, dir string) (string, error)
	// Changed lists the files that differ from base, untracked ones
	// included, relative to the working tree's root.
	Changed(ctx context.Context, dir, base string) ([]string, error)
}

// Events receives agent.files events.
type Events interface {
	Publish(typ string, data any)
}

// Config tunes the service.
type Config struct {
	// Interval is how often working trees are read while agents work
	// (default DefaultInterval).
	Interval time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// Service follows the files agents change.
type Service struct {
	cfg    Config
	agents Agents
	envs   Environments
	git    Git
	events Events
	// changes carries agent.state events to Run, so a tree is read the
	// moment an agent starts working, before it writes anything.
	changes chan agent.Agent

	mu    sync.Mutex
	trees map[string]*tree       // by working tree root
	live  map[string]agent.Agent // the agents seen, by ID
	roots map[string]string      // environment ID → its working tree's root
}

// tree is one working tree: its changed files as last read, and who
// changed which.
type tree struct {
	files map[string]stamp            // rel → as last read; nil before the first read
	edits map[string]map[string]*Edit // rel → agent ID → edit
}

// stamp is what a file looked like.
type stamp struct {
	exists bool
	size   int64
	mod    time.Time
}

// NewService returns a service; Run makes it follow agents.
func NewService(cfg Config, agents Agents, envs Environments, g Git, events Events) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		cfg: cfg, agents: agents, envs: envs, git: g, events: events,
		changes: make(chan agent.Agent, 256),
		trees:   map[string]*tree{}, live: map[string]agent.Agent{}, roots: map[string]string{},
	}
}

// Changed tells the service an agent's state changed (an agent.state
// event). It never blocks: a change it cannot take is seen on the next
// read of the agent list.
func (s *Service) Changed(a agent.Agent) {
	select {
	case s.changes <- a:
	default:
	}
}

// Run follows agents until ctx ends. It reads working trees only while
// an agent works: an idle daemon does nothing here but wait for an
// agent.state event, and read the agent list again now and then in case
// one was missed.
func (s *Service) Run(ctx context.Context) {
	scan := time.NewTicker(s.cfg.Interval)
	defer scan.Stop()
	resync := time.NewTicker(resyncInterval)
	defer resync.Stop()
	s.Tick(ctx)
	for {
		var tick <-chan time.Time
		if s.working() {
			tick = scan.C
		}
		select {
		case <-ctx.Done():
			return
		case a := <-s.changes:
			s.observe(ctx, a)
		case <-tick:
			for root, writers := range s.writers() {
				s.scan(ctx, root, writers, nil)
			}
		case <-resync.C:
			s.Tick(ctx)
		}
	}
}

// resyncInterval is how often Run reads the agent list again.
const resyncInterval = 30 * time.Second

// working reports whether any agent works.
func (s *Service) working() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.live {
		if a.State == agent.StateWorking {
			return true
		}
	}
	return false
}

// Tick reads the agent list again and every working tree an agent works
// in. Run calls it now and then; tests call it directly.
func (s *Service) Tick(ctx context.Context) {
	list, err := s.agents.List(ctx, false)
	if err != nil {
		s.cfg.Logger.Debug("listing agents for their edits", "err", err)
		return
	}
	seen := map[string]bool{}
	for _, a := range list {
		seen[a.ID] = true
		s.observe(ctx, a)
	}
	s.mu.Lock()
	var gone []agent.Agent
	for id, a := range s.live {
		if !seen[id] {
			a.State = agent.StateExited
			gone = append(gone, a)
		}
	}
	s.mu.Unlock()
	for _, a := range gone {
		s.observe(ctx, a)
	}
	for root, writers := range s.writers() {
		s.scan(ctx, root, writers, nil)
	}
}

// writers returns the agents working in each working tree.
func (s *Service) writers() map[string][]agent.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]agent.Agent{}
	for _, a := range s.live {
		if a.State == agent.StateWorking {
			if root := s.roots[a.EnvironmentID]; root != "" {
				out[root] = append(out[root], a)
			}
		}
	}
	return out
}

// observe takes an agent's new state: a tree is read without blame when
// its first agent starts working (what was changed before is not the
// agent's), read with blame when an agent stops (its last writes), and an
// agent that exits is no longer changing anything.
func (s *Service) observe(ctx context.Context, a agent.Agent) {
	root := s.rootOf(ctx, a.EnvironmentID)
	s.mu.Lock()
	prev, known := s.live[a.ID]
	if a.State == agent.StateExited {
		delete(s.live, a.ID)
	} else {
		s.live[a.ID] = a
	}
	othersWorking := false
	for id, o := range s.live {
		if id != a.ID && o.State == agent.StateWorking && s.roots[o.EnvironmentID] == root {
			othersWorking = true
		}
	}
	changed := s.restateLocked(a)
	s.mu.Unlock()
	for _, r := range changed {
		s.publish(r)
	}
	if root == "" {
		return
	}
	wasWorking := known && prev.State == agent.StateWorking
	switch {
	case a.State == agent.StateWorking && !wasWorking && !othersWorking:
		s.scan(ctx, root, nil, &a)
	case wasWorking && a.State != agent.StateWorking:
		writers := []agent.Agent{prev}
		for _, w := range s.writers()[root] {
			if w.ID != a.ID {
				writers = append(writers, w)
			}
		}
		s.scan(ctx, root, writers, nil)
	}
}

// restateLocked updates the state on an agent's edits, or drops them
// when it exited, and returns the trees that changed.
func (s *Service) restateLocked(a agent.Agent) []string {
	var changed []string
	for root, t := range s.trees {
		touched := false
		for rel, by := range t.edits {
			e, ok := by[a.ID]
			if !ok {
				continue
			}
			switch {
			case a.State == agent.StateExited:
				delete(by, a.ID)
				if len(by) == 0 {
					delete(t.edits, rel)
				}
				touched = true
			case e.State != string(a.State) || e.Name != a.Name:
				e.State, e.Name = string(a.State), a.Name
				touched = true
			}
		}
		if touched {
			changed = append(changed, root)
		}
	}
	return changed
}

// rootOf is the working tree root of an environment's directory, or ""
// when it is not in one.
func (s *Service) rootOf(ctx context.Context, envID string) string {
	if envID == "" {
		return ""
	}
	s.mu.Lock()
	root, ok := s.roots[envID]
	s.mu.Unlock()
	if ok {
		return root
	}
	env, err := s.envs.Get(ctx, envID)
	if err != nil || env.Path == "" {
		return ""
	}
	root, err = s.git.Root(ctx, env.Path)
	if err != nil {
		return "" // not a repository (yet): asked again next time
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	s.mu.Lock()
	s.roots[envID] = root
	s.mu.Unlock()
	return root
}

// scan reads a working tree's changed files. With writers, files that
// appeared, changed or disappeared since the last read are theirs;
// without, the read is a baseline, in which only files modified since
// starter started working are its (it may write before the tree is
// read). Either way, files that differ from HEAD no more are nobody's.
func (s *Service) scan(ctx context.Context, root string, writers []agent.Agent, starter *agent.Agent) {
	rels, err := s.git.Changed(ctx, root, "HEAD")
	if err != nil {
		s.cfg.Logger.Debug("reading a working tree's changes", "root", root, "err", err)
		return
	}
	if len(rels) > MaxScanned {
		rels = rels[:MaxScanned]
	}
	cur := make(map[string]stamp, len(rels))
	for _, rel := range rels {
		st := stamp{}
		if fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			st = stamp{exists: true, size: fi.Size(), mod: fi.ModTime()}
		}
		cur[rel] = st
	}
	now := s.cfg.Now()

	s.mu.Lock()
	t := s.trees[root]
	if t == nil {
		t = &tree{edits: map[string]map[string]*Edit{}}
		s.trees[root] = t
	}
	touched := false
	switch {
	case writers != nil && t.files != nil:
		for rel, st := range cur {
			if old, ok := t.files[rel]; ok && old == st {
				continue
			}
			for _, w := range writers {
				s.attributeLocked(t, root, rel, st, w, now)
			}
			touched = true
		}
	case starter != nil && !starter.Since.IsZero():
		for rel, st := range cur {
			if st.exists && !st.mod.Before(starter.Since) {
				s.attributeLocked(t, root, rel, st, *starter, now)
				touched = true
			}
		}
	}
	for rel := range t.edits {
		if _, dirty := cur[rel]; !dirty {
			delete(t.edits, rel)
			touched = true
		}
	}
	t.files = cur
	if len(t.edits) == 0 && len(s.writersLocked(root)) == 0 {
		delete(s.trees, root) // nothing to remember until an agent works here again
	}
	s.mu.Unlock()
	if touched {
		s.publish(root)
	}
}

func (s *Service) writersLocked(root string) []string {
	var ids []string
	for id, a := range s.live {
		if a.State == agent.StateWorking && s.roots[a.EnvironmentID] == root {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Service) attributeLocked(t *tree, root, rel string, st stamp, w agent.Agent, now time.Time) {
	by := t.edits[rel]
	if by == nil {
		by = map[string]*Edit{}
		t.edits[rel] = by
	}
	e := by[w.ID]
	if e == nil {
		e = &Edit{
			Path: filepath.Join(root, filepath.FromSlash(rel)), Worktree: root, Rel: rel,
			Agent: w.ID, Kind: w.Kind, PaneID: w.PaneID, EnvironmentID: w.EnvironmentID, Since: now,
		}
		by[w.ID] = e
	}
	e.Name, e.State, e.Last, e.Deleted = w.Name, string(w.State), now, !st.exists
	if live, ok := s.live[w.ID]; ok {
		e.State = string(live.State)
	}
}

// publish announces a working tree's edits.
func (s *Service) publish(root string) {
	if s.events == nil {
		return
	}
	s.events.Publish(EventFiles, Files{Worktree: root, Files: s.list(func(e Edit) bool { return e.Worktree == root })})
}

// List returns the files agents are changing.
func (s *Service) List(ctx context.Context, req ListRequest) Files {
	keep := func(Edit) bool { return true }
	switch {
	case req.EnvironmentID != "":
		root := s.rootOf(ctx, req.EnvironmentID)
		keep = func(e Edit) bool { return root != "" && e.Worktree == root }
	case req.Dir != "":
		dir := filepath.Clean(req.Dir)
		if d, err := filepath.EvalSymlinks(dir); err == nil {
			dir = d
		}
		keep = func(e Edit) bool { return within(e.Path, dir) || within(e.Worktree, dir) }
	}
	return Files{Files: s.list(keep)}
}

func (s *Service) list(keep func(Edit) bool) []Edit {
	s.mu.Lock()
	out := []Edit{}
	for _, t := range s.trees {
		for _, by := range t.edits {
			for _, e := range by {
				if keep(*e) {
					out = append(out, *e)
				}
			}
		}
	}
	s.mu.Unlock()
	slices.SortFunc(out, func(a, b Edit) int { return cmp.Or(strings.Compare(a.Path, b.Path), strings.Compare(a.Agent, b.Agent)) })
	return out
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
