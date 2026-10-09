package runtime

import (
	"context"
	"log/slog"
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
	watcher *git.Watcher   // nil until run starts, and after it ends
	refs    map[string]int // directory → environments using it
}

func newGitTracker(g *git.Git, envs interface {
	List(context.Context) ([]environment.Environment, error)
}, events event.Publisher, interval time.Duration, log *slog.Logger,
) *gitTracker {
	return &gitTracker{git: g, envs: envs, events: events, interval: interval, log: log, refs: map[string]int{}}
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
	t.mu.Lock()
	t.watcher = w
	t.mu.Unlock()
	for _, e := range envs {
		t.watch(ctx, e.Path)
	}
	w.Run(ctx)
	t.mu.Lock()
	t.watcher = nil
	clear(t.refs)
	t.mu.Unlock()
}

// watch starts tracking dir for one more environment.
func (t *gitTracker) watch(ctx context.Context, dir string) {
	t.mu.Lock()
	w := t.watcher
	t.refs[dir]++
	first := t.refs[dir] == 1
	t.mu.Unlock()
	if w != nil && first {
		w.Watch(ctx, dir)
	}
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
	if t.events == nil {
		return
	}
	envs, err := t.envs.List(context.Background())
	if err != nil {
		return
	}
	for _, e := range envs {
		if e.Path == dir {
			t.events.Publish(event.EnvironmentGit, map[string]any{"id": e.ID, "git": st})
		}
	}
}
