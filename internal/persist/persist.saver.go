package persist

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Defaults for a Saver.
const (
	DefaultDebounce = 2 * time.Second
	DefaultInterval = 30 * time.Second
)

// Saver writes snapshots: shortly after the workspace changes (a burst of
// changes makes one snapshot, taken a debounce after the first), on a
// timer (for what changes without telling, like a shell's directory), and
// when asked. A snapshot identical to the last one written is not written
// again.
type Saver struct {
	store   *Store
	collect func(ctx context.Context) (Snapshot, error)
	// debounce is how long after a change the snapshot is taken; interval
	// is the timer. Zero means the defaults; a negative interval turns the
	// timer off.
	debounce, interval time.Duration
	log                *slog.Logger

	poke chan struct{}

	mu   sync.Mutex // one save at a time
	last []byte     // the last snapshot written, without its time
}

// NewSaver returns a saver that writes what collect returns to store.
func NewSaver(store *Store, collect func(ctx context.Context) (Snapshot, error), debounce, interval time.Duration, log *slog.Logger) *Saver {
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	if interval == 0 {
		interval = DefaultInterval
	}
	return &Saver{
		store: store, collect: collect, debounce: debounce, interval: interval,
		log:  logging.OrDiscard(log),
		poke: make(chan struct{}, 1),
	}
}

// Changed says the workspace changed; a snapshot follows once it settles.
func (w *Saver) Changed() {
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

// Run saves snapshots until ctx ends.
func (w *Saver) Run(ctx context.Context) {
	var tick <-chan time.Time
	if w.interval > 0 {
		t := time.NewTicker(w.interval)
		defer t.Stop()
		tick = t.C
	}
	debounce := time.NewTimer(0)
	<-debounce.C
	pending := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.poke:
			// Agents change often; waiting for quiet could wait forever.
			if !pending {
				debounce.Reset(w.debounce)
				pending = true
			}
			continue
		case <-debounce.C:
			pending = false
		case <-tick:
			if pending {
				continue // the debounce saves soon
			}
		}
		if _, err := w.Save(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("session snapshot failed", "err", err)
		}
	}
}

// Save collects and writes a snapshot now, unless it equals the last one
// written. It reports whether it wrote one.
func (w *Saver) Save(ctx context.Context) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	snap, err := w.collect(ctx)
	if err != nil {
		return false, err
	}
	snap.Schema, snap.SavedAt = Schema, time.Time{}
	content, err := json.Marshal(snap)
	if err != nil {
		return false, err
	}
	if bytes.Equal(content, w.last) {
		return false, nil
	}
	snap.SavedAt = time.Now()
	if err := w.store.Save(snap); err != nil {
		return false, err
	}
	w.last = content
	w.log.Debug("session snapshot saved", "path", w.store.Path(), "environments", len(snap.Environments))
	return true, nil
}
