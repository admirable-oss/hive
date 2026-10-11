// Package metadata keeps short labels that agents, plugins and scripts put
// on panes and environments for the sidebar to show: tokens ("PR #12 ✓",
// "tests 3/40") and a title that names a pane instead of its program. Each
// expires after its TTL unless reported again, so a plugin that stops
// leaves nothing stale behind.
package metadata

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// DefaultTTL is how long a report holds when it does not say.
const DefaultTTL = 30 * time.Minute

// Limits keep the sidebar readable and the store small.
const (
	MaxTokens   = 8  // per target
	MaxKeyLen   = 32 // a token's key
	MaxTextLen  = 48 // a token's text, a title
	MaxTTL      = 7 * 24 * time.Hour
	EventChange = "metadata.changed" // data: Entry (empty when everything expired)
)

// ErrInvalid is returned for a report that cannot be kept.
var ErrInvalid = errors.New("invalid metadata")

// Target is what metadata is about: one pane, or one environment.
type Target struct {
	Pane        string `json:"pane,omitempty"`
	Environment string `json:"environment,omitempty"`
}

func (t Target) key() string { return t.Pane + "\x00" + t.Environment }

// Report sets metadata on a target.
type Report struct {
	Target
	// Tokens sets tokens by key; an empty text removes that token.
	Tokens map[string]string `json:"tokens,omitempty"`
	// Title, when set, names the target ("" removes the title).
	Title *string `json:"title,omitempty"`
	// TTL is how long the report holds (default DefaultTTL).
	TTL time.Duration `json:"ttl,omitempty"`
	// Clear removes everything on the target first.
	Clear bool `json:"clear,omitempty"`
	// Source names who reports (a plugin's ID), for `hive … list`.
	Source string `json:"source,omitempty"`
}

// Token is one label.
type Token struct {
	Key    string    `json:"key"`
	Text   string    `json:"text"`
	Source string    `json:"source,omitempty"`
	Until  time.Time `json:"until"`
}

// Entry is everything on one target.
type Entry struct {
	Target
	Title      string    `json:"title,omitempty"`
	TitleUntil time.Time `json:"title_until,omitzero"`
	Tokens     []Token   `json:"tokens"`
}

// Empty reports whether nothing is left on the entry.
func (e Entry) Empty() bool { return e.Title == "" && len(e.Tokens) == 0 }

// Store keeps metadata in memory: it describes what runs now, so it is not
// worth keeping across daemon restarts (reporters report again).
type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry
	now     func() time.Time
}

// NewStore returns an empty store; now is the clock (nil: time.Now).
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{entries: map[string]*Entry{}, now: now}
}

// Report applies r and returns the target's metadata after it.
func (s *Store) Report(r Report) (Entry, error) {
	if (r.Pane == "") == (r.Environment == "") {
		return Entry{}, fmt.Errorf("%w: name a pane or an environment", ErrInvalid)
	}
	ttl := r.TTL
	switch {
	case ttl == 0:
		ttl = DefaultTTL
	case ttl < 0 || ttl > MaxTTL:
		return Entry{}, fmt.Errorf("%w: ttl %s is outside 0..%s", ErrInvalid, ttl, MaxTTL)
	}
	for k, v := range r.Tokens {
		if k == "" || len(k) > MaxKeyLen || strings.ContainsFunc(k, func(c rune) bool { return !keyRune(c) }) {
			return Entry{}, fmt.Errorf("%w: token key %q (letters, digits, '-', '_' and '.', up to %d)", ErrInvalid, k, MaxKeyLen)
		}
		r.Tokens[k] = clean(v)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	e := s.entries[r.key()]
	if e == nil || r.Clear {
		e = &Entry{Target: r.Target}
	}
	s.expireLocked(e, now)
	until := now.Add(ttl)
	for _, k := range slices.Sorted(maps.Keys(r.Tokens)) {
		e.Tokens = slices.DeleteFunc(e.Tokens, func(t Token) bool { return t.Key == k })
		if text := r.Tokens[k]; text != "" {
			e.Tokens = append(e.Tokens, Token{Key: k, Text: text, Source: r.Source, Until: until})
		}
	}
	if len(e.Tokens) > MaxTokens {
		return Entry{}, fmt.Errorf("%w: more than %d tokens on one target", ErrInvalid, MaxTokens)
	}
	if r.Title != nil {
		e.Title, e.TitleUntil = clean(*r.Title), until
		if e.Title == "" {
			e.TitleUntil = time.Time{}
		}
	}
	if e.Empty() {
		delete(s.entries, r.key())
	} else {
		s.entries[r.key()] = e
	}
	return clone(*e), nil
}

func keyRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}

// clean makes text safe for one sidebar line.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > MaxTextLen {
		s = string(r[:MaxTextLen-1]) + "…"
	}
	return s
}

// List returns every target's current metadata.
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := []Entry{}
	for _, k := range slices.Sorted(maps.Keys(s.entries)) {
		e := s.entries[k]
		s.expireLocked(e, now)
		if e.Empty() {
			delete(s.entries, k)
			continue
		}
		out = append(out, clone(*e))
	}
	return out
}

// Expire drops what ran out and returns the targets that changed.
func (s *Store) Expire() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var changed []Entry
	for _, k := range slices.Sorted(maps.Keys(s.entries)) {
		e := s.entries[k]
		if s.expireLocked(e, now) {
			changed = append(changed, clone(*e))
			if e.Empty() {
				delete(s.entries, k)
			}
		}
	}
	return changed
}

// Forget drops a target's metadata (its pane closed, its environment went).
func (s *Store) Forget(t Target) {
	s.mu.Lock()
	delete(s.entries, t.key())
	s.mu.Unlock()
}

// expireLocked drops e's expired title and tokens; it reports a change.
func (s *Store) expireLocked(e *Entry, now time.Time) bool {
	n := len(e.Tokens)
	e.Tokens = slices.DeleteFunc(e.Tokens, func(t Token) bool { return !now.Before(t.Until) })
	changed := len(e.Tokens) != n
	if e.Title != "" && !now.Before(e.TitleUntil) {
		e.Title, e.TitleUntil, changed = "", time.Time{}, true
	}
	return changed
}

func clone(e Entry) Entry {
	e.Tokens = slices.Clone(e.Tokens)
	if e.Tokens == nil {
		e.Tokens = []Token{}
	}
	return e
}
