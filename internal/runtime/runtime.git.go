package runtime

import (
	"context"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
)

// gitTracker keeps the git status of every environment's directory current
// while the daemon runs, and announces changes as environment.git events.
// Several environments may share a directory; it is watched once.
type gitTracker struct {
	git  *git.Git
	envs interface {
		List(context.Context) ([]environment.Environment, error)
	}
	events   event.Publisher
	interval time.Duration
	log      *slog.Logger

	mu      sync.Mutex
	watcher *git.Watcher           // nil until run starts, and after it ends
	refs    map[string]int         // directory → environments using it
	early   map[string]*git.Status // what watch read before the watcher existed
}

func newGitTracker(g *git.Git, envs interface {
	List(context.Context) ([]environment.Environment, error)
}, events event.Publisher, interval time.Duration, log *slog.Logger,
) *gitTracker {
	return &gitTracker{git: g, envs: envs, events: events, interval: interval, log: log, refs: map[string]int{}, early: map[string]*git.Status{}}
}

// run watches every environment until ctx ends. It is a Server task, so a
// module that is never started holds no watcher.
func (t *gitTracker) run(ctx context.Context) {
	w, err := git.NewWatcher(t.git, t.changed, git.WatcherOptions{Interval: t.interval, Logger: t.log})
	if err != nil {
		t.log.Warn("git status tracking is off", "err", err)
		return
	}
	envs, err := t.envs.List(ctx)
	if err != nil {
		t.log.Warn("list environments for git tracking", "err", err)
	}
	listed := map[string]int{}
	for _, e := range envs {
		listed[e.Path]++
	}
	// Environments created before the watcher existed were counted by
	// watch, and may be listed too: a directory counts its larger number.
	t.mu.Lock()
	t.watcher = w
	for dir, n := range listed {
		t.refs[dir] = max(t.refs[dir], n)
	}
	dirs, early := slices.Collect(maps.Keys(t.refs)), t.early
	t.early = map[string]*git.Status{}
	t.mu.Unlock()
	for _, dir := range dirs {
		// A change since watch read it was not announced yet.
		st := w.Watch(ctx, dir)
		if old, ok := early[dir]; ok && !reflect.DeepEqual(old, st) {
			t.announce(ctx, dir, st)
		}
	}
	w.Run(ctx)
	t.mu.Lock()
	t.watcher = nil
	clear(t.refs)
	t.mu.Unlock()
}

// watch starts tracking dir for one more environment, and returns its
// status. Before run starts, it is read once; run watches it.
func (t *gitTracker) watch(ctx context.Context, dir string) *git.Status {
	t.mu.Lock()
	w := t.watcher
	t.refs[dir]++
	first := t.refs[dir] == 1
	t.mu.Unlock()
	switch {
	case w == nil:
		st, _ := t.git.Status(ctx, dir)
		t.mu.Lock()
		if t.watcher == nil {
			t.early[dir] = st
		}
		t.mu.Unlock()
		return st
	case first:
		return w.Watch(ctx, dir)
	}
	return w.Status(dir)
}

// unwatch stops tracking dir for one environment.
func (t *gitTracker) unwatch(dir string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.refs[dir] == 0 {
		return
	}
	t.refs[dir]--
	if t.refs[dir] == 0 {
		delete(t.refs, dir)
		if t.watcher != nil {
			t.watcher.Unwatch(dir)
		}
	}
}

// status is the last known status of dir (nil when unknown or not a
// repository).
func (t *gitTracker) status(dir string) *git.Status {
	t.mu.Lock()
	w := t.watcher
	t.mu.Unlock()
	if w == nil {
		return nil
	}
	return w.Status(dir)
}

// changed publishes the new status for every environment in dir.
func (t *gitTracker) changed(dir string, st *git.Status) {
	t.announce(context.Background(), dir, st)
}

func (t *gitTracker) announce(ctx context.Context, dir string, st *git.Status) {
	if t.events == nil {
		return
	}
	envs, err := t.envs.List(ctx)
	if err != nil {
		return
	}
	for _, e := range envs {
		if e.Path == dir {
			t.events.Publish(event.EnvironmentGit, map[string]any{"id": e.ID, "git": st})
		}
	}
}
