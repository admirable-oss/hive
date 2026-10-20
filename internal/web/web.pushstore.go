package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/observer"
)

// Subscription is one device that gets notifications for a browser
// session: a Web Push subscription. It ends with its session (sign-out,
// revocation, expiry).
type Subscription struct {
	ID        string     `json:"id"`
	Session   string     `json:"session"`
	Endpoint  string     `json:"endpoint"`
	P256dh    string     `json:"p256dh"`
	Auth      string     `json:"auth"`
	Client    string     `json:"client,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	LastOK    *time.Time `json:"last_ok,omitempty"`
	Failures  int        `json:"failures,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

// Notification details: names (agent, environment, machine) or private
// ("An agent needs you").
const (
	DetailNames   = "names"
	DetailPrivate = "private"
)

// Prefs are a browser session's notification and follow settings.
type Prefs struct {
	Blocked bool   `json:"blocked"`
	Done    bool   `json:"done"`
	Failed  bool   `json:"failed"`
	Detail  string `json:"detail"`
	// Off lists runtimes it gets no notifications from.
	Off []string `json:"off,omitempty"`
	// Mutes silence an environment or one agent, for a while or for good.
	Mutes []Mute `json:"mutes,omitempty"`
	// Follows are agents pinned to the top of Attention.
	Follows []Follow `json:"follows,omitempty"`
}

// Mute silences notifications from an environment (Agent empty) or an
// agent, until Until (nil: until unmuted).
type Mute struct {
	Runtime     string     `json:"runtime"`
	Environment string     `json:"environment,omitempty"`
	Agent       string     `json:"agent,omitempty"`
	Until       *time.Time `json:"until,omitempty"`
}

// Follow pins an agent.
type Follow struct {
	Runtime string `json:"runtime"`
	Agent   string `json:"agent"`
}

// DefaultPrefs notify about blocked and done agents, with names.
func DefaultPrefs() Prefs {
	return Prefs{Blocked: true, Done: true, Detail: DetailNames}
}

// muted reports whether an agent's notifications are silenced at now.
func (p Prefs) muted(rt, env, agent string, now time.Time) bool {
	if slices.Contains(p.Off, rt) {
		return true
	}
	for _, m := range p.Mutes {
		if m.Runtime != rt || (m.Until != nil && !now.Before(*m.Until)) {
			continue
		}
		if (m.Agent != "" && m.Agent == agent) || (m.Agent == "" && m.Environment == env) {
			return true
		}
	}
	return false
}

// Following reports whether agent in rt is followed.
func (p Prefs) Following(rt, agent string) bool {
	return slices.Contains(p.Follows, Follow{Runtime: rt, Agent: agent})
}

// Push limits.
const (
	maxDevicesPerSession = 5
	maxSubscriptions     = 1000
	maxMutes             = 100
	maxFollows           = 100
	// dropAfterFailures forgets a device whose deliveries keep failing.
	dropAfterFailures = 20
)

type pushFile struct {
	Schema        int               `json:"schema"`
	Subscriptions []*Subscription   `json:"subscriptions"`
	Prefs         map[string]*Prefs `json:"prefs"`
}

func (s *Store) pushPath() string { return filepath.Join(s.dir, "push.json") }

func (s *Store) loadPush() error {
	var f pushFile
	if err := jsonfile.Read(s.pushPath(), &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("web push: %w", err)
	}
	if err := jsonfile.CheckSchema(s.pushPath(), f.Schema, 1); err != nil {
		return err
	}
	s.subs, s.prefs = map[string]*Subscription{}, map[string]*Prefs{}
	for _, x := range f.Subscriptions {
		if x != nil && x.ID != "" {
			s.subs[x.ID] = x
		}
	}
	for sid, p := range f.Prefs {
		if p != nil {
			s.prefs[sid] = p
		}
	}
	return nil
}

// savePush writes push.json, dropping what belongs to sessions that are
// gone or no longer active. s.mu is held.
func (s *Store) savePush() error {
	now := s.now()
	f := pushFile{Schema: 1, Prefs: map[string]*Prefs{}}
	for id, x := range s.subs {
		if !s.activeLocked(x.Session, now) {
			delete(s.subs, id)
			continue
		}
		f.Subscriptions = append(f.Subscriptions, x)
	}
	for sid, p := range s.prefs {
		if _, ok := s.sessions[sid]; !ok {
			delete(s.prefs, sid)
			continue
		}
		f.Prefs[sid] = p
	}
	slices.SortFunc(f.Subscriptions, func(a, b *Subscription) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return jsonfile.Write(s.pushPath(), f)
}

// activeLocked reports whether session sid can still observe. s.mu is held.
func (s *Store) activeLocked(sid string, now time.Time) bool {
	x, ok := s.sessions[sid]
	if !ok || x.RevokedAt != nil || !now.Before(x.ExpiresAt) {
		return false
	}
	g := s.grants[x.Grant]
	return g != nil && g.stateAt(now) == observer.StateActive
}

// Active returns session sid and its grant when it can still observe:
// what every notification re-checks before it is sent.
func (s *Store) Active(sid string) (Session, Grant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Session{}, Grant{}, false
	}
	now := s.now()
	if !s.activeLocked(sid, now) {
		return Session{}, Grant{}, false
	}
	x := *s.sessions[sid]
	x.Hash = ""
	return x, public(s.grants[x.Grant], now), true
}

// Subscribe adds (or renews) a device for session sid.
func (s *Store) Subscribe(sid string, sub Subscription, allowAnyEndpoint bool) (Subscription, error) {
	if err := checkEndpoint(sub.Endpoint, allowAnyEndpoint); err != nil {
		return Subscription{}, err
	}
	if sub.P256dh == "" || sub.Auth == "" || len(sub.Endpoint) > 2048 || len(sub.P256dh) > 200 || len(sub.Auth) > 100 {
		return Subscription{}, fmt.Errorf("%w: push subscription", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Subscription{}, err
	}
	if !s.activeLocked(sid, s.now()) {
		return Subscription{}, ErrNoSession
	}
	mine := 0
	for id, x := range s.subs {
		if x.Endpoint == sub.Endpoint {
			delete(s.subs, id) // the same device again: replace it
			continue
		}
		if x.Session == sid {
			mine++
		}
	}
	if mine >= maxDevicesPerSession || len(s.subs) >= maxSubscriptions {
		return Subscription{}, fmt.Errorf("%w: too many devices", ErrInvalid)
	}
	sub.ID, sub.Session, sub.CreatedAt = randomHex(6), sid, s.now().UTC()
	sub.LastOK, sub.Failures, sub.LastError = nil, 0, ""
	if len(sub.Client) > 80 {
		sub.Client = sub.Client[:80]
	}
	x := sub
	s.subs[x.ID] = &x
	if err := s.savePush(); err != nil {
		delete(s.subs, x.ID)
		return Subscription{}, err
	}
	return x, nil
}

// Unsubscribe removes session sid's device with endpoint ("" removes all).
func (s *Store) Unsubscribe(sid, endpoint string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return 0, err
	}
	n := 0
	for id, x := range s.subs {
		if x.Session == sid && (endpoint == "" || x.Endpoint == endpoint) {
			delete(s.subs, id)
			n++
		}
	}
	return n, s.savePush()
}

// Subscriptions lists every device, oldest first; with sid, that
// session's only.
func (s *Store) Subscriptions(sid string) ([]Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	out := []Subscription{}
	for _, x := range s.subs {
		if sid == "" || x.Session == sid {
			out = append(out, *x)
		}
	}
	slices.SortFunc(out, func(a, b Subscription) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

// Delivered records how a delivery to subscription id went: a device the
// push service says is gone, or one that keeps failing, is forgotten.
func (s *Store) Delivered(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.load() != nil {
		return
	}
	x, ok := s.subs[id]
	if !ok {
		return
	}
	switch {
	case err == nil:
		t := s.now().UTC()
		x.LastOK, x.Failures, x.LastError = &t, 0, ""
	case errors.Is(err, errGone):
		delete(s.subs, id)
	default:
		x.Failures++
		x.LastError = err.Error()
		if x.Failures >= dropAfterFailures {
			delete(s.subs, id)
		}
	}
	_ = s.savePush()
}

// PrefsOf returns session sid's preferences (the defaults until set).
func (s *Store) PrefsOf(sid string) Prefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.load() != nil {
		return DefaultPrefs()
	}
	if p, ok := s.prefs[sid]; ok {
		return clonePrefs(*p)
	}
	return DefaultPrefs()
}

func clonePrefs(p Prefs) Prefs {
	p.Off, p.Mutes, p.Follows = slices.Clone(p.Off), slices.Clone(p.Mutes), slices.Clone(p.Follows)
	return p
}

// SetPrefs stores session sid's preferences, dropping expired mutes.
func (s *Store) SetPrefs(sid string, p Prefs) error {
	if p.Detail != DetailNames && p.Detail != DetailPrivate {
		return fmt.Errorf("%w: detail is %s or %s", ErrInvalid, DetailNames, DetailPrivate)
	}
	now := s.now()
	p.Mutes = slices.DeleteFunc(p.Mutes, func(m Mute) bool { return m.Until != nil && !now.Before(*m.Until) })
	if len(p.Mutes) > maxMutes || len(p.Follows) > maxFollows {
		return fmt.Errorf("%w: too many mutes or follows", ErrInvalid)
	}
	p.Off = sortedUnique(p.Off)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	if !s.activeLocked(sid, now) {
		return ErrNoSession
	}
	c := clonePrefs(p)
	s.prefs[sid] = &c
	return s.savePush()
}

// pushKey identifies a device for logs: its push service and ID.
func (x Subscription) String() string {
	return x.ID + " via " + strings.TrimPrefix(hostOf(x.Endpoint), "www.")
}
