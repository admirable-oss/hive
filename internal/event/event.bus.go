package event

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Event types. Data is the JSON of the named value.
const (
	EnvironmentCreated = "environment.created" // environment.Environment
	EnvironmentRemoved = "environment.removed" // {"id": …}
	EnvironmentUpdated = "environment.updated" // environment.Environment (its variables changed)
	EnvironmentGit     = "environment.git"     // {"id": …, "git": git.Status or null}
	ProcessStarted     = "process.started"     // process.Process
	ProcessExited      = "process.exited"      // process.Process (final record)
	ProcessRecovered   = "process.recovered"   // process.Process, re-attached after a daemon restart
	// ProcessOutput says an agent's screen changed: {"id": …}, at most once
	// a second per agent while it keeps printing.
	ProcessOutput = "process.output"
	// Lost is sent to a subscriber that missed events; Data is
	// {"missed": n}. Re-read state after it.
	Lost = "events_lost"
)

// DefaultBuffer is a subscriber's queue length.
const DefaultBuffer = 256

// Event is one thing that happened. Seq increases by one per published
// event, across all types.
type Event struct {
	Seq  uint64          `json:"seq"`
	Type string          `json:"type"`
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Publisher is what domain packages need from the bus.
type Publisher interface {
	Publish(typ string, data any)
}

// Bus fans events out to subscribers.
type Bus struct {
	log *slog.Logger
	now func() time.Time

	mu   sync.Mutex
	seq  uint64
	subs map[*Subscription]struct{}
}

// NewBus returns an empty bus. A nil logger discards.
func NewBus(log *slog.Logger) *Bus {
	return &Bus{log: logging.OrDiscard(log), now: time.Now, subs: make(map[*Subscription]struct{})}
}

// Publish sends an event to every subscriber whose filter accepts typ. It
// never blocks: a full subscriber loses the event.
func (b *Bus) Publish(typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		b.log.Error("event data cannot be encoded; dropped", "type", typ, "err", err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	ev := Event{Seq: b.seq, Type: typ, Time: b.now(), Data: raw}
	for s := range b.subs {
		if !s.accepts(typ) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			s.lost++
		}
	}
}

// Seq returns the sequence number of the last published event.
func (b *Bus) Seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Subscribe returns a subscription to events whose type starts with one of
// prefixes (all events when none are given).
func (b *Bus) Subscribe(buffer int, prefixes ...string) *Subscription {
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	s := &Subscription{bus: b, prefixes: prefixes, ch: make(chan Event, buffer), done: make(chan struct{})}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Subscription receives events from a Bus.
type Subscription struct {
	bus      *Bus
	prefixes []string
	ch       chan Event
	lost     uint64 // guarded by bus.mu
	once     sync.Once
	done     chan struct{}
}

func (s *Subscription) accepts(typ string) bool {
	if len(s.prefixes) == 0 {
		return true
	}
	for _, p := range s.prefixes {
		if strings.HasPrefix(typ, p) {
			return true
		}
	}
	return false
}

// Next waits for the next event. After events were dropped it first returns
// a Lost event saying how many.
func (s *Subscription) Next(ctx context.Context) (Event, error) {
	s.bus.mu.Lock()
	lost := s.lost
	s.lost = 0
	seq := s.bus.seq
	s.bus.mu.Unlock()
	if lost > 0 {
		raw, _ := json.Marshal(map[string]uint64{"missed": lost})
		return Event{Seq: seq, Type: Lost, Time: s.bus.now(), Data: raw}, nil
	}
	select {
	case ev := <-s.ch:
		return ev, nil
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-s.done:
		return Event{}, context.Canceled
	}
}

// Close unsubscribes. It is safe to call more than once.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs, s)
		s.bus.mu.Unlock()
		close(s.done)
	})
}
