package access

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// Member lifetimes. Every member expires; DefaultTTL applies when an
// invitation does not say.
const (
	MinTTL     = time.Hour
	MaxTTL     = 365 * 24 * time.Hour
	DefaultTTL = 90 * 24 * time.Hour
)

// keepEnded is how long a revoked or expired member stays listed.
const keepEnded = 30 * 24 * time.Hour

// maxMembers bounds one session's members.
const maxMembers = 256

// seenEvery bounds how often a member's last connection is written.
const seenEvery = time.Minute

// InviteRequest describes a new member.
type InviteRequest struct {
	Name    string
	Key     PublicKey
	Grants  []Grant
	TTL     time.Duration // zero means DefaultTTL
	Managed bool
}

// Store keeps a session's members in one file. Only the daemon writes it.
type Store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	loaded  bool
	members map[string]*Member
}

type storeFile struct {
	Schema  int       `json:"schema"`
	Members []*Member `json:"members"`
}

// NewStore returns the store kept in dir.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "members.json"), now: time.Now}
}

func (s *Store) load() error {
	if s.loaded {
		return nil
	}
	var f storeFile
	if err := jsonfile.Read(s.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("members %s: %w", s.path, err)
	}
	if err := jsonfile.CheckSchema(s.path, f.Schema, 1); err != nil {
		return err
	}
	s.members = map[string]*Member{}
	for _, m := range f.Members {
		if m != nil && ValidID(m.ID) {
			m.State = ""
			s.members[m.ID] = m
		}
	}
	s.loaded = true
	return nil
}

func (s *Store) save() error {
	now := s.now()
	f := storeFile{Schema: 1, Members: []*Member{}}
	for id, m := range s.members {
		if m.StateAt(now) != StateActive && now.Sub(endedAt(m)) > keepEnded {
			delete(s.members, id)
			continue
		}
		f.Members = append(f.Members, m)
	}
	slices.SortFunc(f.Members, func(a, b *Member) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return jsonfile.Write(s.path, f) // 0600: a temp file renamed into place
}

func endedAt(m *Member) time.Time {
	if m.RevokedAt != nil {
		return *m.RevokedAt
	}
	return m.ExpiresAt
}

func (s *Store) public(m *Member) Member {
	out := *m
	out.Grants = slices.Clone(m.Grants)
	out.State = m.StateAt(s.now())
	return out
}

// find returns the member whose ID or name is ref. A name finds the
// active member of that name, else the newest one.
func (s *Store) find(ref string) (*Member, error) {
	if m, ok := s.members[ref]; ok {
		return m, nil
	}
	var found *Member
	now := s.now()
	for _, m := range s.members {
		if m.Name != ref {
			continue
		}
		if m.StateAt(now) == StateActive {
			return m, nil
		}
		if found == nil || m.CreatedAt.After(found.CreatedAt) {
			found = m
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	return found, nil
}

// Invite makes a member.
func (s *Store) Invite(req InviteRequest) (Member, error) {
	req.Name = strings.TrimSpace(req.Name)
	if !validName(req.Name) {
		return Member{}, fmt.Errorf("%w: a name is 1 to 32 letters, digits, '.', '_' or '-'", ErrInvalid)
	}
	if req.Key.Type == "" {
		return Member{}, fmt.Errorf("%w: a member needs an ssh public key", ErrInvalid)
	}
	if len(req.Grants) == 0 {
		return Member{}, fmt.Errorf("%w: a member needs a role somewhere (--role with --env or --all)", ErrInvalid)
	}
	if err := validGrants(req.Grants); err != nil {
		return Member{}, err
	}
	if req.TTL == 0 {
		req.TTL = DefaultTTL
	}
	if req.TTL < MinTTL || req.TTL > MaxTTL {
		return Member{}, fmt.Errorf("%w: expiry must be between %s and %s", ErrInvalid, MinTTL, MaxTTL)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Member{}, err
	}
	now := s.now()
	active := 0
	for _, m := range s.members {
		if m.StateAt(now) != StateActive {
			continue
		}
		active++
		switch {
		case m.Name == req.Name:
			return Member{}, fmt.Errorf("%w: a member named %s already exists (revoke it first, or pick another name)", ErrConflict, req.Name)
		case m.Key == req.Key.String():
			return Member{}, fmt.Errorf("%w: member %s already has this key", ErrConflict, m.Name)
		}
	}
	if active >= maxMembers {
		return Member{}, fmt.Errorf("%w: at most %d members (revoke unused ones)", ErrInvalid, maxMembers)
	}
	grants := slices.Clone(req.Grants)
	sortGrants(grants)
	at := now.UTC().Truncate(time.Second)
	m := &Member{
		ID: newID(), Name: req.Name, Key: req.Key.String(), Fingerprint: req.Key.Fingerprint(),
		Managed: req.Managed, Grants: grants, CreatedAt: at, ExpiresAt: at.Add(req.TTL),
	}
	s.members[m.ID] = m
	if err := s.save(); err != nil {
		delete(s.members, m.ID)
		return Member{}, err
	}
	return s.public(m), nil
}

// List returns every member, oldest first.
func (s *Store) List() ([]Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, s.public(m))
	}
	slices.SortFunc(out, func(a, b Member) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

// Get returns the member whose ID or name is ref.
func (s *Store) Get(ref string) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Member{}, err
	}
	m, err := s.find(ref)
	if err != nil {
		return Member{}, err
	}
	return s.public(m), nil
}

// change applies fn to the active member ref and saves; fn's changes are
// undone if saving fails.
func (s *Store) change(ref string, fn func(m *Member) error) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Member{}, err
	}
	m, err := s.find(ref)
	if err != nil {
		return Member{}, err
	}
	if err := stateErr(m.StateAt(s.now())); err != nil {
		return s.public(m), err
	}
	before := *m
	before.Grants = slices.Clone(m.Grants)
	if err := fn(m); err != nil {
		*m = before
		return Member{}, err
	}
	if err := s.save(); err != nil {
		*m = before
		return Member{}, err
	}
	return s.public(m), nil
}

// Grant sets the member's role in env (AllEnvironments for all of them);
// RoleNone takes it away. A member keeps at least one grant: revoke it
// instead.
func (s *Store) Grant(ref, env string, role Role) (Member, error) {
	if env == "" || strings.ContainsAny(env, " \t\n") {
		return Member{}, fmt.Errorf("%w: environment %q", ErrInvalid, env)
	}
	if role != RoleNone && role.rank() == 0 {
		return Member{}, fmt.Errorf("%w: role %q", ErrInvalid, role)
	}
	return s.change(ref, func(m *Member) error {
		m.Grants = slices.DeleteFunc(m.Grants, func(g Grant) bool { return g.Environment == env })
		if role != RoleNone {
			m.Grants = append(m.Grants, Grant{Environment: env, Role: role})
		}
		if len(m.Grants) == 0 {
			return fmt.Errorf("%w: that was %s's last grant (hive access revoke %s)", ErrInvalid, m.Name, m.Name)
		}
		sortGrants(m.Grants)
		return nil
	})
}

// Renew sets the member to expire ttl from now.
func (s *Store) Renew(ref string, ttl time.Duration) (Member, error) {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return Member{}, fmt.Errorf("%w: expiry must be between %s and %s", ErrInvalid, MinTTL, MaxTTL)
	}
	return s.change(ref, func(m *Member) error {
		m.ExpiresAt = s.now().UTC().Truncate(time.Second).Add(ttl)
		return nil
	})
}

// Revoke ends the member's access now. Revoking it again changes nothing.
func (s *Store) Revoke(ref string) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Member{}, err
	}
	m, err := s.find(ref)
	if err != nil {
		return Member{}, err
	}
	if m.RevokedAt == nil {
		now := s.now().UTC()
		m.RevokedAt = &now
		if err := s.save(); err != nil {
			m.RevokedAt = nil
			return Member{}, err
		}
	}
	return s.public(m), nil
}

// Check returns member id when it is still active: what the member socket
// asks before each request.
func (s *Store) Check(id string) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Member{}, err
	}
	m, ok := s.members[id]
	if !ok {
		return Member{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return s.public(m), stateErr(m.StateAt(s.now()))
}

// Seen records that member id connected now; at most once a minute.
func (s *Store) Seen(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.load() != nil {
		return
	}
	m, ok := s.members[id]
	if !ok {
		return
	}
	now := s.now().UTC()
	if m.LastSeenAt != nil && now.Sub(*m.LastSeenAt) < seenEvery {
		return
	}
	m.LastSeenAt = &now
	_ = s.save() // a missed time is not worth failing a connection
}

func stateErr(state string) error {
	switch state {
	case StateExpired:
		return ErrExpired
	case StateRevoked:
		return ErrRevoked
	}
	return nil
}

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return idPrefix + hex.EncodeToString(b)
}
