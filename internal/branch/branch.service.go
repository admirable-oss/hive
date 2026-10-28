package branch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/logging"
)

// Workspace is what the service needs from environments and worktrees.
type Workspace interface {
	// RemoveWorktree removes the worktree at path and the environments
	// rooted in it, stopping their agents. Without force it refuses a
	// worktree with uncommitted changes.
	RemoveWorktree(ctx context.Context, path string, force bool) error
	// Busy reports whether a process runs in an environment rooted at
	// path.
	Busy(ctx context.Context, path string) (bool, error)
}

// AgentInfo is an agent as the service sees it.
type AgentInfo struct {
	ID            string
	Kind          string
	State         string // the agent's state: working, blocked, idle, done, exited, …
	EnvironmentID string
}

// Agents is what the service needs from agents.
type Agents interface {
	// Get returns an agent by its ID.
	Get(ctx context.Context, id string) (AgentInfo, error)
	// In lists the agents of an environment.
	In(ctx context.Context, environmentID string) ([]AgentInfo, error)
	// Prompt sends text to an agent as a prompt.
	Prompt(ctx context.Context, id, text string) error
}

// Commands runs gates.
type Commands interface {
	// Run runs argv in dir until it exits, returning its exit status and
	// the last lines of its output.
	Run(ctx context.Context, dir string, argv []string, env map[string]string) (int, []string, error)
}

// Events receives what the service publishes.
type Events interface {
	Publish(typ string, data any)
}

// Ports are the service's ways out. Git is required; a nil port turns
// off what needs it (no agents: comments are kept, never sent).
type Ports struct {
	Git       *git.Git
	Workspace Workspace
	Agents    Agents
	Commands  Commands
	Events    Events
	// Forges open pull requests; nil uses DefaultForges with the git
	// binary's environment.
	Forges *Forges
	// Issues names the issue a task's branch works on, for its pull
	// request; nil names none.
	Issues Issues
}

// Issues finds the issue a branch's task works on.
type Issues interface {
	// For returns how a pull request names the issue of a plan's task (a
	// URL, or tracker:key), or "".
	For(ctx context.Context, plan, task string) string
}

// Cleanup is what happens to a branch's worktree once it has merged.
type Cleanup string

const (
	// CleanupMerged removes the worktree of a merged branch once no
	// uncommitted work and no running process is in it.
	CleanupMerged Cleanup = "merged"
	// CleanupNever leaves every worktree until a person removes it.
	CleanupNever Cleanup = "never"
)

// Config tunes the service. Zero values are the defaults.
type Config struct {
	Logger *slog.Logger
	Now    func() time.Time
	// Interval is how often branches are read again when no change was
	// noticed (default 30 seconds).
	Interval time.Duration
	// Debounce gathers changes noticed close together into one read
	// (default 500 milliseconds).
	Debounce time.Duration
	// Cleanup is the policy for merged branches' worktrees (default
	// CleanupMerged).
	Cleanup Cleanup
	// DeleteMerged also deletes the local branch once its worktree is
	// cleaned up, when git agrees it is merged.
	DeleteMerged bool
	// Keep is how many closed (cleaned or removed) branches are
	// remembered (default 100).
	Keep int
	// Gates are checked before a branch is ready.
	Gates []Gate
}

// DefaultKeep is how many closed branches are remembered by default.
const DefaultKeep = 100

func (c Config) withDefaults() Config {
	c.Logger = logging.OrDiscard(c.Logger)
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Interval <= 0 {
		c.Interval = 30 * time.Second
	}
	if c.Debounce <= 0 {
		c.Debounce = 500 * time.Millisecond
	}
	if c.Cleanup == "" {
		c.Cleanup = CleanupMerged
	}
	if c.Keep <= 0 {
		c.Keep = DefaultKeep
	}
	return c
}

// Service follows branches. It owns their records: every change happens
// under its lock and is saved and published from there. Reading a
// repository (snapshots, diffs, merges) happens outside the lock, one
// repository at a time.
type Service struct {
	cfg   Config
	store Store
	git   *git.Git
	ports Ports
	log   *slog.Logger

	mu       sync.Mutex
	branches map[string]*Branch // by ID
	rev      uint64
	merges   map[[2]string]mergeResult // conflict checks by the trees compared
	pending  map[string]bool           // repositories to read again soon
	wake     chan struct{}

	saveMu sync.Mutex
	repoMu sync.Map // repository → *sync.Mutex: one read at a time
}

// mergeResult caches a conflict check between two trees.
type mergeResult struct {
	conflicts []string
	unknown   bool
}

// NewService returns a service over store. Load reads what it kept.
func NewService(cfg Config, store Store, ports Ports) *Service {
	cfg = cfg.withDefaults()
	if ports.Git == nil {
		ports.Git = git.New()
	}
	if ports.Forges == nil {
		ports.Forges = DefaultForges(nil)
	}
	return &Service{
		cfg: cfg, store: store, git: ports.Git, ports: ports, log: cfg.Logger,
		branches: map[string]*Branch{}, merges: map[[2]string]mergeResult{},
		pending: map[string]bool{}, wake: make(chan struct{}, 1),
	}
}

// Load reads the branches a previous daemon kept. Gates that were running
// when it stopped did not finish.
func (s *Service) Load() error {
	list, err := s.store.Load()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range list {
		b := list[i]
		if b.ID == "" {
			continue
		}
		b.Checking = false
		s.branches[b.ID] = &b
		s.rev = max(s.rev, b.Rev)
	}
	return err
}

// Run reads open branches again whenever a change is noticed (Touch) and
// every Interval, and cleans up merged ones, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(s.cfg.Interval)
	defer tick.Stop()
	s.scheduleAll()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.scheduleAll()
		case <-s.wake:
			if debounce == nil {
				debounce = time.After(s.cfg.Debounce)
			}
			continue
		case <-debounce:
		}
		debounce = nil
		for _, repo := range s.takePending() {
			if ctx.Err() != nil {
				return
			}
			if err := s.Refresh(ctx, repo); err != nil && ctx.Err() == nil {
				s.log.Debug("reading branches failed", "repo", repo, "err", err)
			}
		}
	}
}

func (s *Service) scheduleAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.branches {
		if b.State.Active() {
			s.pending[b.Repo] = true
		}
	}
	if len(s.pending) > 0 {
		signal(s.wake)
	}
}

func (s *Service) takePending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.pending))
	for r := range s.pending {
		out = append(out, r)
	}
	clear(s.pending)
	slices.Sort(out)
	return out
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// scheduleLocked asks Run to read a repository again soon.
func (s *Service) scheduleLocked(repo string) {
	s.pending[repo] = true
	signal(s.wake)
}

// Touch says something changed in an environment (its files, its git
// state, or its agent's work): its branch is read again soon. working says
// an agent started working there.
func (s *Service) Touch(environmentID string, working bool) {
	if environmentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.branches {
		if b.EnvironmentID == environmentID && b.State.Active() {
			if working && !b.Started {
				b.Started = true
				if b.State == StateCreated {
					b.State = StateWorking
				}
				s.commitLocked(b)
			}
			s.scheduleLocked(b.Repo)
		}
	}
}

// TrackRequest records a worktree made for work on a branch.
type TrackRequest struct {
	// EnvironmentID is the worktree's environment; it becomes the
	// branch's ID.
	EnvironmentID string
	Path          string
	// Repo is the repository's main working tree.
	Repo string
	Name string
	// Base is what the branch merges into; empty means the branch checked
	// out in Repo (or its commit, when Repo is detached).
	Base  string
	Owner *Owner
}

// Track starts following a branch's worktree, or updates what is known
// of one already followed (its owner, its environment).
func (s *Service) Track(ctx context.Context, req TrackRequest) (Branch, error) {
	if req.EnvironmentID == "" || req.Path == "" || req.Repo == "" || req.Name == "" {
		return Branch{}, fmt.Errorf("%w: a branch needs its environment, worktree, repository and name", ErrInvalid)
	}
	s.mu.Lock()
	if b := s.activeLocked(req.Repo, req.Name); b != nil {
		changed := false
		if b.Path != req.Path || b.EnvironmentID != req.EnvironmentID {
			b.Path, b.EnvironmentID, changed = req.Path, req.EnvironmentID, true
		}
		if req.Owner != nil {
			if o := mergeOwner(b.Owner, *req.Owner); b.Owner == nil || o != *b.Owner {
				b.Owner, changed = &o, true
			}
		}
		if changed {
			s.commitLocked(b)
		}
		s.scheduleLocked(b.Repo)
		out := b.clone()
		s.mu.Unlock()
		s.persist()
		return out, nil
	}
	s.mu.Unlock()

	base, baseCommit, err := s.baseOf(ctx, req)
	if err != nil {
		return Branch{}, err
	}
	now := s.cfg.Now()
	b := &Branch{
		ID: req.EnvironmentID, Repo: req.Repo, Name: req.Name, Base: base, BaseCommit: baseCommit,
		Path: req.Path, EnvironmentID: req.EnvironmentID, State: StateCreated, CreatedAt: now,
	}
	if req.Owner != nil {
		o := *req.Owner
		b.Owner = &o
	}
	s.mu.Lock()
	if old := s.branches[b.ID]; old != nil {
		// The environment ID was used before, by a branch whose worktree
		// is gone: the new one takes the ID.
		if old.State.Active() {
			s.closeLocked(old, now)
		}
		delete(s.branches, old.ID)
		s.publishLocked(EventRemoved, map[string]any{"ids": []string{old.ID}})
	}
	s.branches[b.ID] = b
	s.commitLocked(b)
	s.scheduleLocked(b.Repo)
	out := b.clone()
	s.mu.Unlock()
	s.persist()
	s.log.Info("following branch", "branch", b.Name, "base", b.Base, "path", b.Path)
	return out, nil
}

// mergeOwner keeps what a branch was made for, filling in only what was
// not known: a second agent in a worktree does not take it over.
func mergeOwner(old *Owner, o Owner) Owner {
	if old == nil {
		return o
	}
	return Owner{
		Agent: cmp.Or(old.Agent, o.Agent), Kind: cmp.Or(old.Kind, o.Kind),
		Plan: cmp.Or(old.Plan, o.Plan), Task: cmp.Or(old.Task, o.Task),
	}
}

// baseOf decides a new branch's base and the commit it started from.
func (s *Service) baseOf(ctx context.Context, req TrackRequest) (base, commit string, err error) {
	base = req.Base
	if base == "" {
		base, err = s.git.CurrentBranch(ctx, req.Repo)
		if err != nil {
			return "", "", err
		}
		if base == "" || base == req.Name {
			base, err = s.git.Resolve(ctx, req.Repo, "HEAD")
			if err != nil {
				return "", "", err
			}
		}
	}
	head, err := s.git.Resolve(ctx, req.Path, "HEAD")
	if err != nil {
		return "", "", err
	}
	baseCommit, err := s.git.Resolve(ctx, req.Repo, base)
	if err != nil {
		// A base git cannot name (a typo, a remote branch not fetched) is
		// still recorded: the branch is followed from its own head.
		return base, head, nil //nolint:nilerr // reported by Refresh as the branch's error
	}
	mb, err := s.git.MergeBase(ctx, req.Path, head, baseCommit)
	if err != nil || mb == "" {
		return base, head, nil //nolint:nilerr // unrelated histories: start at the head
	}
	return base, mb, nil
}

// WorktreeRemoved says the worktree at path is gone: its branch is
// closed, as cleaned when it had merged and as removed otherwise.
func (s *Service) WorktreeRemoved(path string) {
	s.mu.Lock()
	var hit bool
	for _, b := range s.branches {
		if b.State.Active() && samePath(b.Path, path) {
			s.closeLocked(b, s.cfg.Now())
			hit = true
		}
	}
	if hit {
		s.forgetLocked()
	}
	s.mu.Unlock()
	if hit {
		s.persist()
	}
}

// closeLocked records that b's worktree is gone.
func (s *Service) closeLocked(b *Branch, now time.Time) {
	if b.State == StateMerged {
		b.State = StateCleaned
	} else {
		b.State = StateRemoved
	}
	b.Path, b.EnvironmentID, b.Checking, b.Cleanup = "", "", false, ""
	b.Overlaps, b.InProgress = nil, false
	b.ClosedAt = &now
	s.commitLocked(b)
	// Other branches no longer overlap it.
	for _, o := range s.branches {
		if o.Repo == b.Repo && o != b {
			if n := slices.DeleteFunc(slices.Clone(o.Overlaps), func(x Overlap) bool { return x.With == b.ID }); len(n) != len(o.Overlaps) {
				o.Overlaps = n
				s.commitLocked(o)
			}
		}
	}
}

// forgetLocked drops the oldest closed branches beyond Keep.
func (s *Service) forgetLocked() {
	var closed []*Branch
	for _, b := range s.branches {
		if !b.State.Active() {
			closed = append(closed, b)
		}
	}
	if len(closed) <= s.cfg.Keep {
		return
	}
	slices.SortFunc(closed, func(a, b *Branch) int {
		return cmp.Or(closedAt(b).Compare(closedAt(a)), strings.Compare(a.ID, b.ID))
	})
	var ids []string
	for _, b := range closed[s.cfg.Keep:] {
		delete(s.branches, b.ID)
		ids = append(ids, b.ID)
	}
	slices.Sort(ids)
	s.publishLocked(EventRemoved, map[string]any{"ids": ids})
}

func closedAt(b *Branch) time.Time {
	if b.ClosedAt != nil {
		return *b.ClosedAt
	}
	return b.UpdatedAt
}

// commitLocked records a change to b and publishes it.
func (s *Service) commitLocked(b *Branch) {
	s.rev++
	b.Rev = s.rev
	b.UpdatedAt = s.cfg.Now()
	s.publishLocked(EventUpdated, b.clone())
}

func (s *Service) publishLocked(typ string, data any) {
	if s.ports.Events != nil {
		s.ports.Events.Publish(typ, data)
	}
}

// persist saves every branch. Saves are serialized, and each saves the
// state as it is when it starts, so the last write is the newest.
func (s *Service) persist() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	list := make([]Branch, 0, len(s.branches))
	for _, b := range s.branches {
		list = append(list, b.clone())
	}
	s.mu.Unlock()
	slices.SortFunc(list, func(a, b Branch) int { return strings.Compare(a.ID, b.ID) })
	if err := s.store.Save(list); err != nil {
		s.log.Warn("saving branches failed", "err", err)
	}
}

// activeLocked returns the active branch named name in repo.
func (s *Service) activeLocked(repo, name string) *Branch {
	for _, b := range s.branches {
		if b.State.Active() && b.Name == name && samePath(b.Repo, repo) {
			return b
		}
	}
	return nil
}

// ListRequest filters List.
type ListRequest struct {
	// Repo keeps the branches of one repository (any of its directories
	// that is its main working tree).
	Repo string `json:"repo,omitempty" desc:"only this repository's branches (its main working tree)"`
	// All includes closed branches (cleaned and removed).
	All bool `json:"all,omitempty" desc:"include cleaned and removed branches"`
}

// List returns branches, active ones first, then newest first.
func (s *Service) List(req ListRequest) []Branch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Branch{}
	for _, b := range s.branches {
		if (!req.All && !b.State.Active()) || (req.Repo != "" && !samePath(b.Repo, req.Repo)) {
			continue
		}
		out = append(out, b.clone())
	}
	slices.SortFunc(out, func(a, b Branch) int {
		if a.State.Active() != b.State.Active() {
			if a.State.Active() {
				return -1
			}
			return 1
		}
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return out
}

// Get finds a branch by reference (see Find).
func (s *Service) Get(ctx context.Context, ref string) (Branch, error) {
	b, err := s.find(ctx, ref)
	if err != nil {
		return Branch{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return b.clone(), nil
}

// find resolves a reference to a branch, trying in turn: its ID, its
// environment, its owner (an agent's ID, or plan/task), the environment
// of an agent working in it, and its name. Active branches win over
// closed ones.
func (s *Service) find(ctx context.Context, ref string) (*Branch, error) {
	if ref == "" {
		return nil, fmt.Errorf("%w: name a branch (its ID, environment, agent, task or name)", ErrInvalid)
	}
	s.mu.Lock()
	b, err := s.findLocked(ref)
	s.mu.Unlock()
	if err == nil || !errors.Is(err, ErrNotFound) || s.ports.Agents == nil {
		return b, err
	}
	a, aerr := s.ports.Agents.Get(ctx, ref)
	if aerr != nil || a.EnvironmentID == "" {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.branches {
		if x.State.Active() && x.EnvironmentID == a.EnvironmentID {
			return x, nil
		}
	}
	return nil, err
}

func (s *Service) findLocked(ref string) (*Branch, error) {
	matchers := []func(b *Branch) bool{
		func(b *Branch) bool { return b.ID == ref },
		func(b *Branch) bool { return b.EnvironmentID == ref },
		func(b *Branch) bool { return b.Owner != nil && b.Owner.Agent == ref },
		func(b *Branch) bool {
			return b.Owner != nil && b.Owner.Plan != "" && b.Owner.Plan+"/"+b.Owner.Task == ref
		},
		func(b *Branch) bool { return b.Name == ref },
	}
	for _, active := range []bool{true, false} {
		for _, match := range matchers {
			var hits []*Branch
			for _, b := range s.branches {
				if b.State.Active() == active && match(b) {
					hits = append(hits, b)
				}
			}
			switch {
			case len(hits) == 1:
				return hits[0], nil
			case len(hits) > 1 && active:
				ids := make([]string, len(hits))
				for i, h := range hits {
					ids[i] = h.ID
				}
				slices.Sort(ids)
				return nil, fmt.Errorf("%w: %q is %s; use an ID", ErrAmbiguous, ref, strings.Join(ids, ", "))
			case len(hits) > 1:
				// Closed: the most recently closed.
				slices.SortFunc(hits, func(a, b *Branch) int { return closedAt(b).Compare(closedAt(a)) })
				return hits[0], nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
}

// lockRepo serializes reads and writes of one repository.
func (s *Service) lockRepo(repo string) func() {
	m, _ := s.repoMu.LoadOrStore(filepath.Clean(repo), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// samePath compares directories, resolving symlinks (on macOS /tmp and
// /var are links, and git reports resolved paths).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
