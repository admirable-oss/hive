package observer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// History limits.
const (
	// DefaultHistory is how many events are kept for observers to catch
	// up with.
	DefaultHistory = 2048
	// DefaultPage and MaxPage bound one page of history.
	DefaultPage = 100
	MaxPage     = 1000
	// FeedBuffer is how many events wait for a slow subscriber before it
	// is told to resynchronise.
	FeedBuffer = 256
)

// History keeps the latest observer events of one daemon run (an epoch)
// and feeds new ones to subscribers. Recording never blocks: a subscriber
// that falls behind loses its queue and gets a Resync event instead.
type History struct {
	epoch string
	limit int
	now   func() time.Time

	mu     sync.Mutex
	events []Event // a ring of up to limit events, oldest at start
	start  int
	floor  uint64 // events up to floor are gone: evicted, or lost before recording
	last   uint64 // the newest position recorded
	feeds  map[*Feed]struct{}
}

// NewHistory keeps up to limit events (DefaultHistory when not positive).
func NewHistory(limit int) *History {
	if limit <= 0 {
		limit = DefaultHistory
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &History{epoch: hex.EncodeToString(b), limit: limit, now: time.Now, feeds: map[*Feed]struct{}{}}
}

// Epoch names this daemon run's event stream. Sequence numbers are only
// comparable within one epoch.
func (h *History) Epoch() string { return h.epoch }

// Last returns the newest position recorded: a snapshot taken after it
// reflects every event up to it.
func (h *History) Last() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

// Record adds ev and passes it to the subscribers that may see it.
func (h *History) Record(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ev.Seq <= h.floor {
		return // queued before a loss that every subscriber resyncs over
	}
	if len(h.events) < h.limit {
		h.events = append(h.events, ev)
	} else {
		h.floor = max(h.floor, h.events[h.start].Seq)
		h.events[h.start] = ev
		h.start = (h.start + 1) % h.limit
	}
	h.last = max(h.last, ev.Seq)
	for f := range h.feeds {
		if f.lost || !ev.Visible(f.scope) {
			continue
		}
		select {
		case f.ch <- ev:
		default:
			f.markLost()
		}
	}
}

// Lost says events up to seq may not have been recorded: whoever relies on
// them resynchronises.
func (h *History) Lost(seq uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.floor = max(h.floor, seq)
	h.last = max(h.last, seq)
	for f := range h.feeds {
		f.markLost()
	}
}

// each calls fn on retained events after since, oldest first, until it
// returns false. h.mu is held.
func (h *History) each(since uint64, fn func(Event) bool) {
	for i := range h.events {
		ev := h.events[(h.start+i)%len(h.events)]
		if ev.Seq > since && !fn(ev) {
			return
		}
	}
}

// resyncReason says why a client at (epoch, since) cannot catch up from
// history, or "" when it can. h.mu is held.
func (h *History) resyncReason(epoch string, since uint64) string {
	switch {
	case epoch != h.epoch:
		return "epoch"
	case since < h.floor || since > h.last:
		return "history"
	}
	return ""
}

// Page is a page of history.
type Page struct {
	Epoch  string  `json:"epoch"`
	Events []Event `json:"events"`
	// Next is the position to ask from for the following page; More says
	// there is one.
	Next uint64 `json:"next"`
	More bool   `json:"more,omitempty"`
	// Last is the newest position the daemon has.
	Last uint64 `json:"last"`
	// Resync, when set, says the position asked for cannot be caught up
	// from: take a snapshot instead. Events is then empty.
	Resync *ResyncData `json:"resync,omitempty"`
}

// Page returns up to limit events in scope after since, in epoch.
func (h *History) Page(s Scope, epoch string, since uint64, limit int) Page {
	if limit <= 0 {
		limit = DefaultPage
	}
	limit = min(limit, MaxPage)
	h.mu.Lock()
	defer h.mu.Unlock()
	p := Page{Epoch: h.epoch, Events: []Event{}, Last: h.last}
	if reason := h.resyncReason(epoch, since); reason != "" {
		p.Resync = &ResyncData{Reason: reason, Epoch: h.epoch}
		return p
	}
	p.Next = h.last
	h.each(since, func(ev Event) bool {
		if len(p.Events) == limit {
			p.More = true
			return false
		}
		p.Next = ev.Seq
		if ev.Visible(s) {
			p.Events = append(p.Events, ev)
		}
		return true
	})
	if !p.More {
		p.Next = h.last
	}
	return p
}

// Feed delivers events in one scope as they are recorded.
type Feed struct {
	h       *History
	scope   Scope
	ch      chan Event
	backlog []Event
	lost    bool          // guarded by h.mu
	wake    chan struct{} // signalled when lost is set
}

// Subscribe starts a feed of events in scope s. With since set, it first
// replays retained events after it (or a Resync when that position cannot
// be caught up from); otherwise it starts after the newest event. It
// returns the position the feed starts after.
func (h *History) Subscribe(s Scope, epoch string, since *uint64) (*Feed, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := &Feed{h: h, scope: s, ch: make(chan Event, FeedBuffer), wake: make(chan struct{}, 1)}
	if since != nil {
		if reason := h.resyncReason(epoch, *since); reason != "" {
			f.backlog = []Event{resyncEvent(h.last, reason, h.epoch, h.now())}
		} else {
			h.each(*since, func(ev Event) bool {
				if ev.Visible(s) {
					f.backlog = append(f.backlog, ev)
				}
				return true
			})
		}
	}
	h.feeds[f] = struct{}{}
	return f, h.last
}

// Next waits for the feed's next event. After it fell behind, its queue is
// dropped and the next event is a Resync.
func (f *Feed) Next(ctx context.Context) (Event, error) {
	if len(f.backlog) > 0 {
		ev := f.backlog[0]
		f.backlog = f.backlog[1:]
		return ev, nil
	}
	for {
		f.h.mu.Lock()
		if f.lost {
			f.lost = false
			for len(f.ch) > 0 {
				<-f.ch
			}
			ev := resyncEvent(f.h.last, "lost", f.h.epoch, f.h.now())
			f.h.mu.Unlock()
			return ev, nil
		}
		f.h.mu.Unlock()
		select {
		case ev := <-f.ch:
			return ev, nil
		case <-f.wake:
		case <-ctx.Done():
			return Event{}, ctx.Err()
		}
	}
}

// markLost makes f's next event a Resync. h.mu is held.
func (f *Feed) markLost() {
	f.lost = true
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Close ends the feed.
func (f *Feed) Close() {
	f.h.mu.Lock()
	delete(f.h.feeds, f)
	f.h.mu.Unlock()
}
