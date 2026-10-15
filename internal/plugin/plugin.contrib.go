package plugin

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits on what a plugin sets at run time.
const (
	MaxCardRows   = 6
	MaxCardLinks  = 3
	MaxLabel      = 16
	MaxText       = 48
	DefaultTTL    = 30 * time.Minute
	MaxContentTTL = 7 * 24 * time.Hour
)

// Tones colour a row or an indicator.
var Tones = []string{"", "info", "success", "warning", "error"}

// ErrContribution is returned for content that cannot be shown.
var ErrContribution = errors.New("invalid contribution")

// CardRow is one line of a card.
type CardRow struct {
	Label string `json:"label,omitempty"`
	Text  string `json:"text"`
	Tone  string `json:"tone,omitempty"`
}

// CardContent is what a plugin shows on one of its cards, for one
// environment (environment scope) or one agent's pane (agent scope).
type CardContent struct {
	Plugin      string    `json:"plugin"`
	Card        string    `json:"card"`
	Title       string    `json:"title"`
	Environment string    `json:"environment,omitempty"`
	Pane        string    `json:"pane,omitempty"`
	Rows        []CardRow `json:"rows"`
	Links       []Link    `json:"links,omitempty"`
	Until       time.Time `json:"until"`
}

// StatusContent is a status indicator's text.
type StatusContent struct {
	Plugin    string    `json:"plugin"`
	Indicator string    `json:"indicator"`
	Text      string    `json:"text"`
	Tone      string    `json:"tone,omitempty"`
	Until     time.Time `json:"until"`
}

// CardUpdate sets (or with no rows, clears) a card (plugin.card).
type CardUpdate struct {
	Plugin      string        `json:"plugin,omitempty"`
	Card        string        `json:"card"`
	Environment string        `json:"environment,omitempty"`
	Pane        string        `json:"pane,omitempty"`
	Rows        []CardRow     `json:"rows,omitempty"`
	Links       []Link        `json:"links,omitempty"`
	TTL         time.Duration `json:"ttl,omitempty"`
}

// StatusUpdate sets (or with no text, clears) an indicator (plugin.status).
type StatusUpdate struct {
	Plugin    string        `json:"plugin,omitempty"`
	Indicator string        `json:"indicator"`
	Text      string        `json:"text,omitempty"`
	Tone      string        `json:"tone,omitempty"`
	TTL       time.Duration `json:"ttl,omitempty"`
}

// Contribution is one declared UI contribution.
type Contribution struct {
	Plugin string `json:"plugin"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	// Key names it for [plugins] hidden: plugin:kind:id.
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Action   string   `json:"action,omitempty"`
	Contexts []string `json:"contexts,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	URL      string   `json:"url,omitempty"`
	// Hidden is set when the user turned it off.
	Hidden bool `json:"hidden,omitempty"`
	// Available is false while the plugin cannot run (failed): the UI
	// shows it but does not offer it.
	Available bool `json:"available"`
}

// Contributions is everything plugins add to the UI now.
type Contributions struct {
	Declared []Contribution  `json:"declared"`
	Cards    []CardContent   `json:"cards"`
	Status   []StatusContent `json:"status"`
}

type cardKey struct{ plugin, card, env, pane string }

type contentStore struct {
	mu     sync.Mutex
	cards  map[cardKey]CardContent
	status map[[2]string]StatusContent
}

func (s *contentStore) setCard(c CardContent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cards == nil {
		s.cards = map[cardKey]CardContent{}
	}
	k := cardKey{c.Plugin, c.Card, c.Environment, c.Pane}
	if len(c.Rows) == 0 && len(c.Links) == 0 {
		delete(s.cards, k)
		return
	}
	s.cards[k] = c
}

func (s *contentStore) setStatus(c StatusContent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == nil {
		s.status = map[[2]string]StatusContent{}
	}
	k := [2]string{c.Plugin, c.Indicator}
	if c.Text == "" {
		delete(s.status, k)
		return
	}
	s.status[k] = c
}

// expire drops what ran out; it returns the plugins whose content changed.
func (s *contentStore) expire(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := map[string]bool{}
	for k, c := range s.cards {
		if now.After(c.Until) {
			delete(s.cards, k)
			changed[c.Plugin] = true
		}
	}
	for k, c := range s.status {
		if now.After(c.Until) {
			delete(s.status, k)
			changed[c.Plugin] = true
		}
	}
	return slices.Sorted(maps.Keys(changed))
}

// forget drops a plugin's content; it reports whether it had any.
func (s *contentStore) forget(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	had := false
	for k := range s.cards {
		if k.plugin == id {
			delete(s.cards, k)
			had = true
		}
	}
	for k := range s.status {
		if k[0] == id {
			delete(s.status, k)
			had = true
		}
	}
	return had
}

func (s *contentStore) snapshot() ([]CardContent, []StatusContent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cards := slices.Collect(maps.Values(s.cards))
	status := slices.Collect(maps.Values(s.status))
	slices.SortFunc(cards, func(a, b CardContent) int {
		return strings.Compare(a.Plugin+a.Card+a.Environment+a.Pane, b.Plugin+b.Card+b.Environment+b.Pane)
	})
	slices.SortFunc(status, func(a, b StatusContent) int {
		return strings.Compare(a.Plugin+a.Indicator, b.Plugin+b.Indicator)
	})
	return cards, status
}

// cleanText keeps a line short and printable.
func cleanText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if utf8.RuneCountInString(s) > limit {
		s = string([]rune(s)[:limit-1]) + "…"
	}
	return s
}

func ttlOf(d time.Duration) (time.Duration, error) {
	switch {
	case d == 0:
		return DefaultTTL, nil
	case d < 0 || d > MaxContentTTL:
		return 0, fmt.Errorf("%w: ttl must be positive and at most %s", ErrContribution, MaxContentTTL)
	}
	return d, nil
}

// loadedFor returns the loaded plugin id, refusing one that cannot run.
func (h *Host) loadedFor(id string) (*loaded, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.plugins[id]
	switch {
	case l == nil:
		return nil, fmt.Errorf("%w %q (or it is disabled)", ErrNotFound, id)
	case l.problem != "":
		return nil, fmt.Errorf("plugin %s is not running: %s", id, l.problem)
	}
	return l, nil
}

// SetCard shows (or clears) a card of plugin u.Plugin. Only cards its
// manifest declares are accepted, about a target of the card's scope.
func (h *Host) SetCard(u CardUpdate) (CardContent, error) {
	l, err := h.loadedFor(u.Plugin)
	if err != nil {
		return CardContent{}, err
	}
	i := slices.IndexFunc(l.Manifest.Contributes.Cards, func(c Card) bool { return c.ID == u.Card })
	if i < 0 {
		return CardContent{}, fmt.Errorf("%w: %s declares no card %q", ErrContribution, u.Plugin, u.Card)
	}
	card := l.Manifest.Contributes.Cards[i]
	switch {
	case card.Scope == "agent" && (u.Pane == "" || u.Environment != ""):
		return CardContent{}, fmt.Errorf("%w: card %s is about an agent: give its pane", ErrContribution, u.Card)
	case card.Scope == "environment" && (u.Environment == "" || u.Pane != ""):
		return CardContent{}, fmt.Errorf("%w: card %s is about an environment: give its id", ErrContribution, u.Card)
	case len(u.Rows) > MaxCardRows:
		return CardContent{}, fmt.Errorf("%w: at most %d rows", ErrContribution, MaxCardRows)
	case len(u.Links) > MaxCardLinks:
		return CardContent{}, fmt.Errorf("%w: at most %d links", ErrContribution, MaxCardLinks)
	}
	ttl, err := ttlOf(u.TTL)
	if err != nil {
		return CardContent{}, err
	}
	c := CardContent{Plugin: u.Plugin, Card: u.Card, Title: card.Title, Environment: u.Environment, Pane: u.Pane, Until: time.Now().Add(ttl)}
	for _, r := range u.Rows {
		if !slices.Contains(Tones, r.Tone) {
			return CardContent{}, fmt.Errorf("%w: tone %q (want info, success, warning or error)", ErrContribution, r.Tone)
		}
		row := CardRow{Label: cleanText(r.Label, MaxLabel), Text: cleanText(r.Text, MaxText), Tone: r.Tone}
		if row.Text == "" && row.Label == "" {
			continue
		}
		c.Rows = append(c.Rows, row)
	}
	for _, link := range u.Links {
		if !SafeURL(link.URL) {
			return CardContent{}, fmt.Errorf("%w: link %q must be http or https", ErrContribution, link.Title)
		}
		c.Links = append(c.Links, Link{ID: link.ID, Title: cleanText(link.Title, MaxTitle), URL: link.URL})
	}
	h.content.setCard(c)
	h.cfg.Publish(EventContributions, map[string]string{"plugin": u.Plugin})
	return c, nil
}

// SetStatus shows (or clears) a status indicator of plugin u.Plugin.
func (h *Host) SetStatus(u StatusUpdate) (StatusContent, error) {
	l, err := h.loadedFor(u.Plugin)
	if err != nil {
		return StatusContent{}, err
	}
	if !slices.ContainsFunc(l.Manifest.Contributes.Status, func(s StatusIndicator) bool { return s.ID == u.Indicator }) {
		return StatusContent{}, fmt.Errorf("%w: %s declares no status indicator %q", ErrContribution, u.Plugin, u.Indicator)
	}
	if !slices.Contains(Tones, u.Tone) {
		return StatusContent{}, fmt.Errorf("%w: tone %q (want info, success, warning or error)", ErrContribution, u.Tone)
	}
	ttl, err := ttlOf(u.TTL)
	if err != nil {
		return StatusContent{}, err
	}
	c := StatusContent{Plugin: u.Plugin, Indicator: u.Indicator, Text: cleanText(u.Text, 24), Tone: u.Tone, Until: time.Now().Add(ttl)}
	h.content.setStatus(c)
	h.cfg.Publish(EventContributions, map[string]string{"plugin": u.Plugin})
	return c, nil
}

// Contributions lists every loaded plugin's declared contributions and
// what they show now. Hidden contributions are listed (marked) but their
// content is left out.
func (h *Host) Contributions() Contributions {
	out := Contributions{Declared: []Contribution{}, Cards: []CardContent{}, Status: []StatusContent{}}
	hidden := map[string]bool{}
	h.mu.Lock()
	ids := slices.Sorted(maps.Keys(h.plugins))
	for _, id := range ids {
		l := h.plugins[id]
		if l.Manifest == nil {
			continue
		}
		avail := l.problem == ""
		add := func(c Contribution) {
			c.Plugin, c.Available = id, avail
			c.Key = id + ":" + c.Kind + ":" + c.ID
			c.Hidden = h.isHidden(c.Key)
			if c.Hidden {
				hidden[c.Key] = true
			}
			out.Declared = append(out.Declared, c)
		}
		m := l.Manifest
		for _, c := range m.Contributes.Cards {
			add(Contribution{Kind: ContribCard, ID: c.ID, Title: c.Title, Scope: c.Scope})
		}
		for _, s := range m.Contributes.Status {
			add(Contribution{Kind: ContribStatus, ID: s.ID, Title: s.Title})
		}
		for _, a := range m.Contributes.Actions {
			add(Contribution{Kind: ContribAction, ID: a.Action, Action: a.Action, Title: a.Title, Contexts: a.Contexts})
		}
		for _, p := range m.Contributes.Palette {
			add(Contribution{Kind: ContribPalette, ID: p.Action, Action: p.Action, Title: p.Title})
		}
		for _, link := range m.Contributes.Links {
			add(Contribution{Kind: ContribLink, ID: link.ID, Title: link.Title, URL: link.URL})
		}
		if len(m.Configuration.Settings) > 0 {
			add(Contribution{Kind: ContribConfig, ID: "settings", Title: "Configure " + m.Name})
		}
	}
	h.mu.Unlock()
	cards, status := h.content.snapshot()
	for _, c := range cards {
		if !hidden[c.Plugin+":"+ContribCard+":"+c.Card] {
			out.Cards = append(out.Cards, c)
		}
	}
	for _, s := range status {
		if !hidden[s.Plugin+":"+ContribStatus+":"+s.Indicator] {
			out.Status = append(out.Status, s)
		}
	}
	return out
}

// isHidden matches key (plugin:kind:id) against the user's hidden list,
// whose entries may name a whole plugin or a whole kind.
func (h *Host) isHidden(key string) bool {
	for _, x := range h.cfg.Hidden {
		if key == x || strings.HasPrefix(key, x+":") {
			return true
		}
	}
	return false
}

// Request records that a plugin asks for an optional capability. It is
// never granted here: the user grants it (`hive plugin grant`).
func (h *Host) Request(id, capability, reason string) (PermissionRequest, error) {
	h.mu.Lock()
	l := h.plugins[id]
	if l == nil {
		h.mu.Unlock()
		return PermissionRequest{}, fmt.Errorf("%w %q", ErrNotFound, id)
	}
	if !slices.Contains(l.Manifest.OptionalCapabilities(), capability) {
		h.mu.Unlock()
		return PermissionRequest{}, fmt.Errorf("%s may only ask for capabilities its manifest lists as optional; %q is not one", id, capability)
	}
	req := PermissionRequest{Plugin: id, Capability: capability, Reason: cleanText(reason, 200), At: time.Now()}
	if slices.Contains(l.grant.Effective, capability) {
		h.mu.Unlock()
		return req, nil // already held
	}
	i := slices.IndexFunc(l.health.Requests, func(r PermissionRequest) bool { return r.Capability == capability })
	if i >= 0 {
		l.health.Requests[i] = req
	} else {
		l.health.Requests = append(l.health.Requests, req)
	}
	h.mu.Unlock()
	h.cfg.Publish(EventPermission, req)
	return req, nil
}
