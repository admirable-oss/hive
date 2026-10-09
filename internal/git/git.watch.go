package git

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/admirable-oss/hive/internal/logging"
)

// Watcher keeps the status of watched directories current. Changes in a
// repository's git directory (HEAD, index, refs) are noticed through the
// filesystem within a moment; edits to working-tree files are noticed by a
// periodic refresh, because watching every file of a large tree costs more
// than it saves.
type Watcher struct {
	git      *Git
	onChange func(dir string, st *Status)
	interval time.Duration
	debounce time.Duration
	log      *slog.Logger

	fsw *fsnotify.Watcher

	mu      sync.Mutex
	dirs    map[string]*watched // by watched directory
	byPath  map[string][]string // fs path → watched directories it affects
	pending map[string]bool     // directories to refresh after the debounce
	timer   *time.Timer
}

type watched struct {
	status *Status
	paths  []string
}

// WatcherOptions tunes a Watcher. Zero values use defaults.
type WatcherOptions struct {
	// Interval is the periodic refresh (default 5s).
	Interval time.Duration
	// Debounce groups bursts of filesystem events (default 250ms).
	Debounce time.Duration
	Logger   *slog.Logger
}

// NewWatcher returns a watcher calling onChange whenever a watched
// directory's status changes (st is nil when it stopped being a repository).
func NewWatcher(g *Git, onChange func(dir string, st *Status), opts WatcherOptions) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	if opts.Debounce <= 0 {
		opts.Debounce = 250 * time.Millisecond
	}
	return &Watcher{
		git: g, onChange: onChange, interval: opts.Interval, debounce: opts.Debounce,
		log: logging.OrDiscard(opts.Logger), fsw: fsw,
		dirs: map[string]*watched{}, byPath: map[string][]string{}, pending: map[string]bool{},
	}, nil
}

// Watch starts tracking dir and returns its current status (nil when it is
// not a repository; it is still refreshed periodically, in case one is
// created).
func (w *Watcher) Watch(ctx context.Context, dir string) *Status {
	st, _ := w.git.Status(ctx, dir)
	var paths []string
	if gitDir, common, err := w.git.Dirs(ctx, dir); err == nil {
		paths = uniq(gitDir, common, filepath.Join(common, "refs", "heads"))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if old, ok := w.dirs[dir]; ok {
		w.unwatchLocked(dir, old)
	}
	w.dirs[dir] = &watched{status: st, paths: paths}
	for _, p := range paths {
		if len(w.byPath[p]) == 0 {
			if err := w.fsw.Add(p); err != nil {
				w.log.Debug("cannot watch git directory; relying on periodic refresh", "path", p, "err", err)
			}
		}
		w.byPath[p] = append(w.byPath[p], dir)
	}
	return st
}

// Unwatch stops tracking dir.
func (w *Watcher) Unwatch(dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if old, ok := w.dirs[dir]; ok {
		w.unwatchLocked(dir, old)
		delete(w.dirs, dir)
	}
}

func (w *Watcher) unwatchLocked(dir string, old *watched) {
	for _, p := range old.paths {
		list := w.byPath[p]
		for i, d := range list {
			if d == dir {
				list = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			delete(w.byPath, p)
			_ = w.fsw.Remove(p)
		} else {
			w.byPath[p] = list
		}
	}
}

// Status returns the last known status of dir.
func (w *Watcher) Status(dir string) *Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	if d, ok := w.dirs[dir]; ok {
		return d.status
	}
	return nil
}

// Run processes filesystem events and periodic refreshes until ctx ends,
// then releases the watcher.
func (w *Watcher) Run(ctx context.Context) {
	defer w.fsw.Close()
	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	flush := make(chan struct{}, 1)
	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			if w.timer != nil {
				w.timer.Stop()
			}
			w.mu.Unlock()
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.schedule(filepath.Dir(ev.Name), ev.Name, flush)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.log.Debug("git watcher error", "err", err)
		case <-flush:
			w.refresh(ctx, w.takePending())
		case <-tick.C:
			w.refresh(ctx, w.allDirs())
		}
	}
}

// schedule marks the directories affected by an event for a refresh after
// the debounce period.
func (w *Watcher) schedule(parent, name string, flush chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, p := range []string{parent, name} {
		for _, d := range w.byPath[p] {
			w.pending[d] = true
		}
	}
	if len(w.pending) == 0 || w.timer != nil {
		return
	}
	w.timer = time.AfterFunc(w.debounce, func() {
		select {
		case flush <- struct{}{}:
		default:
		}
	})
}

func (w *Watcher) takePending() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer = nil
	var out []string
	for d := range w.pending {
		out = append(out, d)
	}
	clear(w.pending)
	return out
}

func (w *Watcher) allDirs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.dirs))
	for d := range w.dirs {
		out = append(out, d)
	}
	return out
}

// refresh re-reads the status of dirs and reports the ones that changed.
func (w *Watcher) refresh(ctx context.Context, dirs []string) {
	for _, dir := range dirs {
		st, err := w.git.Status(ctx, dir)
		if err != nil {
			st = nil
		}
		w.mu.Lock()
		d, ok := w.dirs[dir]
		changed := ok && !reflect.DeepEqual(d.status, st)
		if changed {
			d.status = st
		}
		w.mu.Unlock()
		if changed && w.onChange != nil {
			w.onChange(dir, st)
		}
	}
}

func uniq(paths ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
