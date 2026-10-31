// Package presence is who works in a runtime right now (RFC 0007): each
// connection's person and where it is looking, and who holds each
// terminal's input.
//
// A terminal takes input from one person at a time. Input from someone
// who does not hold it takes it when it is free; while another holds it,
// the input is refused, so two people never type into one prompt at once.
// The holder keeps it until they release it, leave, or stop typing for the
// idle time. The holder is a person, not a connection: the runtime's own
// user is one person however many clients they run, so working alone is
// unchanged.
package presence

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// OwnerID is the runtime's own user, on its control socket.
const OwnerID = "owner"

// DefaultIdle is how long a holder that stopped typing keeps a terminal.
const DefaultIdle = 10 * time.Second

// Person is who acts: a member, or the runtime's own user (OwnerID).
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Owner reports whether p is the runtime's own user.
func (p Person) Owner() bool { return p.ID == OwnerID }

// Spot is where a connection is looking.
type Spot struct {
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

// Entry is one connection's presence.
type Entry struct {
	// Connection tells a person's connections apart.
	Connection string `json:"connection"`
	Person     Person `json:"person"`
	// Client is the program the connection says it is.
	Client string `json:"client,omitempty"`
	Spot
	Since time.Time `json:"since"`
}

// Hold is who holds a terminal's input.
type Hold struct {
	Process     string    `json:"process_id"`
	Pane        string    `json:"pane,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Holder      Person    `json:"holder"`
	Since       time.Time `json:"since"`
	// Until is when it lapses unless the holder types again.
	Until time.Time `json:"until"`
}

// HeldError is input refused because someone else holds the terminal.
type HeldError struct{ Holder Person }

func (e *HeldError) Error() string {
	return fmt.Sprintf("%s is typing here: input is taken by one person at a time (wait, or `hive pane claim --force`)", e.Holder.Name)
}

// ErrHeld matches every *HeldError.
var ErrHeld = errors.New("presence: held by someone else")

func (e *HeldError) Is(target error) bool { return target == ErrHeld }

// Change kinds passed to OnChange.
const (
	ChangedPresence = "presence"
	ChangedFloor    = "floor"
)

// Registry keeps the presence of a runtime's connections and the holders
// of its terminals.
type Registry struct {
	idle     time.Duration
	now      func() time.Time
	onChange func(kind string, h *Hold, process string)

	mu      sync.Mutex
	entries map[any]*Entry
	next    int
	holds   map[string]*hold
}

type hold struct {
	Hold
	timer *time.Timer
}

// New returns a registry. idle is how long a silent holder keeps a
// terminal (zero: DefaultIdle). onChange, when set, is told of every
// change: of presence, or of a terminal's holder (h nil once nobody holds
// process).
func New(idle time.Duration, onChange func(kind string, h *Hold, process string)) *Registry {
	if idle <= 0 {
		idle = DefaultIdle
	}
	return &Registry{idle: idle, now: time.Now, onChange: onChange, entries: map[any]*Entry{}, holds: map[string]*hold{}}
}

func (r *Registry) changed(kind string, h *Hold, process string) {
	if r.onChange != nil {
		r.onChange(kind, h, process)
	}
}

// Set records where connection conn (any comparable key, one per
// connection) of who is looking. It reports whether conn is new, for the
// caller to Leave when it ends.
func (r *Registry) Set(conn any, who Person, client string, at Spot) bool {
	r.mu.Lock()
	e, ok := r.entries[conn]
	if !ok {
		r.next++
		e = &Entry{Connection: fmt.Sprintf("c%d", r.next)}
		r.entries[conn] = e
	}
	same := ok && e.Person == who && e.Client == client && e.Spot == at
	if !same {
		e.Person, e.Client, e.Spot, e.Since = who, client, at, r.now().UTC()
	}
	r.mu.Unlock()
	if !same {
		r.changed(ChangedPresence, nil, "")
	}
	return !ok
}

// Leave forgets connection conn.
func (r *Registry) Leave(conn any) {
	r.mu.Lock()
	_, ok := r.entries[conn]
	delete(r.entries, conn)
	r.mu.Unlock()
	if ok {
		r.changed(ChangedPresence, nil, "")
	}
}

// List returns every connection's presence, the runtime's own user first,
// then by person and connection.
func (r *Registry) List() []Entry {
	r.mu.Lock()
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, *e)
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b Entry) int {
		switch {
		case a.Person.Owner() != b.Person.Owner():
			if a.Person.Owner() {
				return -1
			}
			return 1
		case a.Person.Name != b.Person.Name:
			return strings.Compare(a.Person.Name, b.Person.Name)
		}
		return strings.Compare(a.Connection, b.Connection)
	})
	return out
}

// Use is input from who into process (shown in pane, in environment env):
// it takes the terminal when it is free or who holds it already, and
// refuses it with a *HeldError while someone else does.
func (r *Registry) Use(process, pane, env string, who Person) error {
	return r.take(process, pane, env, who, false)
}

// Claim takes process's input for who on purpose; with force, from
// whoever holds it. It returns the previous holder, if another.
func (r *Registry) Claim(process, pane, env string, who Person, force bool) (*Person, error) {
	r.mu.Lock()
	var prev *Person
	if h := r.holds[process]; h != nil && h.Holder.ID != who.ID {
		p := h.Holder
		prev = &p
	}
	r.mu.Unlock()
	return prev, r.take(process, pane, env, who, force)
}

func (r *Registry) take(process, pane, env string, who Person, force bool) error {
	now := r.now().UTC()
	r.mu.Lock()
	h := r.holds[process]
	if h != nil && h.Holder.ID != who.ID && !force {
		holder := h.Holder
		r.mu.Unlock()
		return &HeldError{Holder: holder}
	}
	fresh := h == nil || h.Holder.ID != who.ID
	if fresh {
		if h != nil {
			h.timer.Stop()
		}
		h = &hold{Hold: Hold{Process: process, Pane: pane, Environment: env, Holder: who, Since: now}}
		r.holds[process] = h
		h.timer = time.AfterFunc(r.idle, func() { r.lapse(process, h) })
	} else {
		h.timer.Reset(r.idle)
	}
	h.Until = now.Add(r.idle)
	snapshot := h.Hold
	r.mu.Unlock()
	if fresh {
		r.changed(ChangedFloor, &snapshot, process)
	}
	return nil
}

// lapse lets go of h when its holder has been silent for the idle time.
func (r *Registry) lapse(process string, h *hold) {
	r.mu.Lock()
	if r.holds[process] != h {
		r.mu.Unlock()
		return
	}
	delete(r.holds, process)
	r.mu.Unlock()
	r.changed(ChangedFloor, nil, process)
}

// Release lets go of process's input when who holds it, and reports
// whether it did.
func (r *Registry) Release(process string, who Person) bool {
	r.mu.Lock()
	h := r.holds[process]
	if h == nil || h.Holder.ID != who.ID {
		r.mu.Unlock()
		return false
	}
	h.timer.Stop()
	delete(r.holds, process)
	r.mu.Unlock()
	r.changed(ChangedFloor, nil, process)
	return true
}

// ReleaseIf lets go of every terminal held by a person gone(id) says left.
func (r *Registry) ReleaseIf(gone func(id string) bool) {
	r.mu.Lock()
	var released []string
	for process, h := range r.holds {
		if gone(h.Holder.ID) {
			h.timer.Stop()
			delete(r.holds, process)
			released = append(released, process)
		}
	}
	r.mu.Unlock()
	slices.Sort(released)
	for _, process := range released {
		r.changed(ChangedFloor, nil, process)
	}
}

// Holds returns who holds which terminal, by process.
func (r *Registry) Holds() []Hold {
	r.mu.Lock()
	out := make([]Hold, 0, len(r.holds))
	for _, h := range r.holds {
		out = append(out, h.Hold)
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b Hold) int { return strings.Compare(a.Process, b.Process) })
	return out
}

// Holder returns who holds process's input, if anyone.
func (r *Registry) Holder(process string) (Person, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h := r.holds[process]; h != nil {
		return h.Holder, true
	}
	return Person{}, false
}
