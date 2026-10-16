package observer

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
)

// refreshEvery bounds how often an Index re-reads every pane after a miss.
const refreshEvery = time.Second

// Index remembers where processes and panes are, learnt from pane events,
// so translating an event rarely needs a lookup. A miss re-reads every pane
// (at most once per refreshEvery).
type Index struct {
	refresh func(ctx context.Context) ([]pane.Pane, error)

	mu        sync.Mutex
	byProcess map[string]Location
	byPane    map[string]Location
	refreshed time.Time
}

// NewIndex returns an index that reads panes with refresh on a miss.
func NewIndex(refresh func(ctx context.Context) ([]pane.Pane, error)) *Index {
	return &Index{refresh: refresh, byProcess: map[string]Location{}, byPane: map[string]Location{}}
}

// Learn updates the index from a daemon event.
func (x *Index) Learn(ev event.Event) {
	switch ev.Type {
	case pane.EventPaneCreated, pane.EventPaneMoved, pane.EventPaneUpdated, pane.EventPaneFocused, pane.EventPaneClosed:
	default:
		return
	}
	var p pane.Pane
	if json.Unmarshal(ev.Data, &p) != nil || p.ID == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if ev.Type == pane.EventPaneClosed {
		// The process keeps its place: its exit is usually told after.
		delete(x.byPane, p.ID)
		return
	}
	x.put(p)
}

func (x *Index) put(p pane.Pane) {
	loc := Location{Environment: p.EnvironmentID, Tab: p.TabID, Pane: p.ID}
	x.byPane[p.ID] = loc
	if p.ProcessID != "" {
		x.byProcess[p.ProcessID] = loc
	}
}

// Process implements Locator.
func (x *Index) Process(id string) (Location, bool) {
	return x.lookup(func() (Location, bool) { l, ok := x.byProcess[id]; return l, ok })
}

// Pane implements Locator.
func (x *Index) Pane(id string) (Location, bool) {
	return x.lookup(func() (Location, bool) { l, ok := x.byPane[id]; return l, ok })
}

func (x *Index) lookup(find func() (Location, bool)) (Location, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if l, ok := find(); ok {
		return l, true
	}
	if x.refresh == nil || time.Since(x.refreshed) < refreshEvery {
		return Location{}, false
	}
	x.refreshed = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	panes, err := x.refresh(ctx)
	if err != nil {
		return Location{}, false
	}
	x.byProcess, x.byPane = map[string]Location{}, map[string]Location{}
	for _, p := range panes {
		x.put(p)
	}
	return find()
}

// RecordEvents feeds the bus's events, translated, into h until ctx ends.
func RecordEvents(ctx context.Context, bus *event.Bus, x *Index, h *History) {
	sub := bus.Subscribe(4 * event.DefaultBuffer)
	defer sub.Close()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if ev.Type == event.Lost {
			h.Lost(ev.Seq)
			continue
		}
		x.Learn(ev)
		if oev, ok := Translate(ev, x); ok {
			h.Record(oev)
		}
	}
}
