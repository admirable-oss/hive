package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/observer"
)

// Runtime is a Hive daemon the web observer may show: one session on one
// machine, reached through its observer socket with a credential made for
// the web observer (`hive web runtime add`).
type Runtime struct {
	Name string `json:"name"`
	// Host is the ssh host for a daemon on another machine; empty is this
	// machine, at Socket.
	Host    string `json:"host,omitempty"`
	Session string `json:"session,omitempty"`
	Socket  string `json:"socket,omitempty"`
	// SSH and HivePath reach Host as the saved machine does (empty: the
	// defaults).
	SSH      []string `json:"ssh,omitempty"`
	HivePath string   `json:"hive_path,omitempty"`
	// Credential is the observer credential's ID on that daemon; its
	// token is in TokenFile.
	Credential string         `json:"credential"`
	TokenFile  string         `json:"token_file"`
	Scope      observer.Scope `json:"scope"`
	Screens    bool           `json:"screens"`
	ExpiresAt  time.Time      `json:"expires_at"`
	AddedAt    time.Time      `json:"added_at"`
}

// Where names where the runtime is: "this machine" or its host, and its
// session.
func (r Runtime) Where() string {
	where := "this machine"
	if r.Host != "" {
		where = r.Host
	}
	if r.Session != "" && r.Session != "default" {
		where += " · session " + r.Session
	}
	return where
}

var runtimeName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// ValidRuntimeName reports whether name can name a runtime (it appears in
// URLs).
func ValidRuntimeName(name string) bool { return runtimeName.MatchString(name) }

// Grant is browser access: what a share link lets its holders see, and
// until when. Its link holds a one-time code; redeeming it gives the
// browser a session cookie, so no long-lived secret is ever in a URL.
type Grant struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Runtimes it may see, by name.
	Runtimes []string `json:"runtimes"`
	// Scope narrows what it sees in each runtime (within the runtime's own
	// observer scope).
	Scope   observer.Scope `json:"scope"`
	Screens bool           `json:"screens"`
	// History allows reading terminal output history (scrollback, recent
	// output), not only the current screen. It needs Screens.
	History   bool       `json:"history"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// The link works until LinkExpiresAt, for up to MaxUses browsers.
	LinkExpiresAt time.Time `json:"link_expires_at"`
	MaxUses       int       `json:"max_uses"`
	Uses          int       `json:"uses"`
	// State is filled on reads: active, expired or revoked.
	State string `json:"state,omitempty"`

	CodeHash string `json:"code_hash,omitempty"`
}

func (g *Grant) stateAt(now time.Time) string {
	switch {
	case g.RevokedAt != nil:
		return observer.StateRevoked
	case !now.Before(g.ExpiresAt):
		return observer.StateExpired
	}
	return observer.StateActive
}

// Session is one browser signed in through a grant.
type Session struct {
	ID         string    `json:"id"`
	Grant      string    `json:"grant"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Client     string    `json:"client,omitempty"` // its user agent, shortened
	// RevokedAt is set when it was signed out by `hive web revoke`; it is
	// kept until it would have expired, so the browser learns why.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Hash      string     `json:"hash,omitempty"`
}

// Limits on grants and their links.
const (
	MinGrantTTL     = time.Minute
	MaxGrantTTL     = 30 * 24 * time.Hour
	DefaultGrantTTL = 8 * time.Hour
	DefaultLinkTTL  = 15 * time.Minute
	MaxLinkUses     = 20
	codePrefix      = "hwc_"
	maxGrants       = 1000
	keepEnded       = 7 * 24 * time.Hour
	touchEvery      = time.Minute
)

// Errors.
var (
	ErrInvalid      = errors.New("web: invalid")
	ErrNotFound     = errors.New("web: not found")
	ErrLinkRefused  = errors.New("web: this link is invalid, used up or expired")
	ErrNoSession    = errors.New("web: not signed in")
	ErrGrantRevoked = errors.New("web: access was revoked")
	ErrGrantExpired = errors.New("web: access expired")
)

// ShareRequest describes a new grant.
type ShareRequest struct {
	Label    string         `json:"label"`
	Runtimes []string       `json:"runtimes"`
	Scope    observer.Scope `json:"scope"`
	Screens  bool           `json:"screens,omitempty"`
	History  bool           `json:"history,omitempty"`
	// TTLSeconds is how long access lasts (DefaultGrantTTL when zero);
	// LinkTTLSeconds how long the link can be redeemed (DefaultLinkTTL);
	// MaxUses how many browsers may redeem it (1 when zero).
	TTLSeconds     int64 `json:"ttl_seconds,omitempty"`
	LinkTTLSeconds int64 `json:"link_ttl_seconds,omitempty"`
	MaxUses        int   `json:"max_uses,omitempty"`
}

// Store keeps the web observer's state: runtimes (runtimes.json, written
// by `hive web runtime`), grants and browser sessions (access.json, written
// by the server). Secrets are kept only as hashes.
type Store struct {
	dir string
	now func() time.Time

	mu       sync.Mutex
	loaded   bool
	grants   map[string]*Grant
	sessions map[string]*Session
}

type accessFile struct {
	Schema   int        `json:"schema"`
	Grants   []*Grant   `json:"grants"`
	Sessions []*Session `json:"sessions"`
}

type runtimesFile struct {
	Schema   int       `json:"schema"`
	Runtimes []Runtime `json:"runtimes"`
}

// NewStore returns the store kept in dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir, now: time.Now}
}

// Dir is where the store lives.
func (s *Store) Dir() string { return s.dir }

func (s *Store) accessPath() string   { return filepath.Join(s.dir, "access.json") }
func (s *Store) runtimesPath() string { return filepath.Join(s.dir, "runtimes.json") }

// Runtimes lists the runtimes, sorted by name.
func (s *Store) Runtimes() ([]Runtime, error) {
	var f runtimesFile
	if err := jsonfile.Read(s.runtimesPath(), &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("web runtimes: %w", err)
	}
	slices.SortFunc(f.Runtimes, func(a, b Runtime) int { return strings.Compare(a.Name, b.Name) })
	return f.Runtimes, nil
}

// SaveRuntime adds or replaces r.
func (s *Store) SaveRuntime(r Runtime) error {
	if !ValidRuntimeName(r.Name) {
		return fmt.Errorf("%w: a runtime name is lowercase letters, digits, '.', '_' and '-' (up to 32)", ErrInvalid)
	}
	list, err := s.Runtimes()
	if err != nil {
		return err
	}
	list = slices.DeleteFunc(list, func(x Runtime) bool { return x.Name == r.Name })
	return s.writeRuntimes(append(list, r))
}

// RemoveRuntime deletes runtime name and its token file.
func (s *Store) RemoveRuntime(name string) (Runtime, error) {
	list, err := s.Runtimes()
	if err != nil {
		return Runtime{}, err
	}
	i := slices.IndexFunc(list, func(x Runtime) bool { return x.Name == name })
	if i < 0 {
		return Runtime{}, fmt.Errorf("%w: runtime %q", ErrNotFound, name)
	}
	r := list[i]
	if err := s.writeRuntimes(slices.Delete(list, i, i+1)); err != nil {
		return Runtime{}, err
	}
	_ = os.Remove(r.TokenFile)
	return r, nil
}

func (s *Store) writeRuntimes(list []Runtime) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return jsonfile.Write(s.runtimesPath(), runtimesFile{Schema: 1, Runtimes: list})
}

// TokenPath is where runtime name's observer token is kept.
func (s *Store) TokenPath(name string) string {
	return filepath.Join(s.dir, "tokens", name+".token")
}

// WriteToken stores a runtime's observer token (0600).
func (s *Store) WriteToken(name, token string) (string, error) {
	p := s.TokenPath(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, []byte(token+"\n"), 0o600)
}

func (s *Store) load() error {
	if s.loaded {
		return nil
	}
	var f accessFile
	if err := jsonfile.Read(s.accessPath(), &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("web access: %w", err)
	}
	s.grants, s.sessions = map[string]*Grant{}, map[string]*Session{}
	for _, g := range f.Grants {
		if g != nil && g.ID != "" {
			s.grants[g.ID] = g
		}
	}
	for _, x := range f.Sessions {
		if x != nil && x.ID != "" {
			s.sessions[x.ID] = x
		}
	}
	s.loaded = true
	return nil
}

func (s *Store) save() error {
	now := s.now()
	f := accessFile{Schema: 1}
	for id, g := range s.grants {
		if g.stateAt(now) != observer.StateActive && now.Sub(endOf(g)) > keepEnded {
			delete(s.grants, id)
			continue
		}
		f.Grants = append(f.Grants, g)
	}
	for id, x := range s.sessions {
		if s.grants[x.Grant] == nil || !now.Before(x.ExpiresAt) {
			delete(s.sessions, id)
			continue
		}
		f.Sessions = append(f.Sessions, x)
	}
	slices.SortFunc(f.Grants, func(a, b *Grant) int { return a.CreatedAt.Compare(b.CreatedAt) })
	slices.SortFunc(f.Sessions, func(a, b *Session) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return jsonfile.Write(s.accessPath(), f)
}

func endOf(g *Grant) time.Time {
	if g.RevokedAt != nil {
		return *g.RevokedAt
	}
	return g.ExpiresAt
}

func public(g *Grant, now time.Time) Grant {
	out := *g
	out.CodeHash = ""
	out.State = g.stateAt(now)
	return out
}

// Share makes a grant and returns it with its link code, shown only now.
func (s *Store) Share(req ShareRequest, runtimes []Runtime) (Grant, string, error) {
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		req.Label = "shared link"
	}
	if len(req.Label) > 64 {
		return Grant{}, "", fmt.Errorf("%w: a label is at most 64 characters", ErrInvalid)
	}
	if len(req.Runtimes) == 0 {
		return Grant{}, "", fmt.Errorf("%w: name the runtimes it may see", ErrInvalid)
	}
	for _, name := range req.Runtimes {
		if !slices.ContainsFunc(runtimes, func(r Runtime) bool { return r.Name == name }) {
			return Grant{}, "", fmt.Errorf("%w: runtime %q (hive web runtime list)", ErrNotFound, name)
		}
	}
	if err := req.Scope.Validate(); err != nil {
		return Grant{}, "", err
	}
	if req.History && !req.Screens {
		return Grant{}, "", fmt.Errorf("%w: output history needs screen access too", ErrInvalid)
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = DefaultGrantTTL
	}
	if ttl < MinGrantTTL || ttl > MaxGrantTTL {
		return Grant{}, "", fmt.Errorf("%w: access lasts between %s and %s", ErrInvalid, MinGrantTTL, MaxGrantTTL)
	}
	linkTTL := time.Duration(req.LinkTTLSeconds) * time.Second
	if linkTTL == 0 {
		linkTTL = DefaultLinkTTL
	}
	linkTTL = min(max(linkTTL, time.Minute), ttl)
	uses := req.MaxUses
	if uses == 0 {
		uses = 1
	}
	if uses < 1 || uses > MaxLinkUses {
		return Grant{}, "", fmt.Errorf("%w: a link works for 1 to %d browsers", ErrInvalid, MaxLinkUses)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Grant{}, "", err
	}
	if len(s.grants) >= maxGrants {
		return Grant{}, "", fmt.Errorf("%w: too many grants; revoke some", ErrInvalid)
	}
	now := s.now().UTC().Truncate(time.Second)
	id, secret := randomHex(6), randomSecret()
	g := &Grant{
		ID: id, Label: req.Label, Runtimes: sortedUnique(req.Runtimes), Scope: req.Scope, Screens: req.Screens, History: req.History,
		CreatedAt: now, ExpiresAt: now.Add(ttl), LinkExpiresAt: now.Add(linkTTL), MaxUses: uses, CodeHash: hash(secret),
	}
	s.grants[id] = g
	if err := s.save(); err != nil {
		delete(s.grants, id)
		return Grant{}, "", err
	}
	return public(g, now), codePrefix + id + "_" + secret, nil
}

// Redeem exchanges a link code for a new browser session, returning the
// session and its cookie value.
func (s *Store) Redeem(code, client string) (Session, Grant, string, error) {
	id, secret, ok := parseSecret(code, codePrefix)
	if !ok {
		return Session{}, Grant{}, "", ErrLinkRefused
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Session{}, Grant{}, "", err
	}
	g, ok := s.grants[id]
	if !ok || !sameHash(g.CodeHash, secret) {
		return Session{}, Grant{}, "", ErrLinkRefused
	}
	now := s.now()
	if g.stateAt(now) != observer.StateActive || !now.Before(g.LinkExpiresAt) || g.Uses >= g.MaxUses {
		return Session{}, public(g, now), "", ErrLinkRefused
	}
	g.Uses++
	sid, ssecret := randomHex(8), randomSecret()
	if len(client) > 120 {
		client = client[:120]
	}
	x := &Session{ID: sid, Grant: g.ID, CreatedAt: now.UTC(), ExpiresAt: g.ExpiresAt, LastSeenAt: now.UTC(), Client: client, Hash: hash(ssecret)}
	s.sessions[sid] = x
	if err := s.save(); err != nil {
		g.Uses--
		delete(s.sessions, sid)
		return Session{}, Grant{}, "", err
	}
	out := *x
	out.Hash = ""
	return out, public(g, now), sid + "." + ssecret, nil
}

// Authenticate returns the session a cookie value names and its grant.
func (s *Store) Authenticate(cookie string) (Session, Grant, error) {
	sid, secret, ok := strings.Cut(cookie, ".")
	if !ok || len(sid) != 16 || len(secret) < 32 || len(secret) > 128 {
		return Session{}, Grant{}, ErrNoSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Session{}, Grant{}, err
	}
	x, ok := s.sessions[sid]
	if !ok || !sameHash(x.Hash, secret) {
		return Session{}, Grant{}, ErrNoSession
	}
	now := s.now()
	g := s.grants[x.Grant]
	switch {
	case g == nil:
		return Session{}, Grant{}, ErrNoSession
	case x.RevokedAt != nil, g.stateAt(now) == observer.StateRevoked:
		return Session{}, public(g, now), ErrGrantRevoked
	case g.stateAt(now) == observer.StateExpired || !now.Before(x.ExpiresAt):
		return Session{}, public(g, now), ErrGrantExpired
	}
	if now.Sub(x.LastSeenAt) >= touchEvery {
		x.LastSeenAt = now.UTC()
		_ = s.save()
	}
	out := *x
	out.Hash = ""
	return out, public(g, now), nil
}

// SignOut ends one browser session.
func (s *Store) SignOut(sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	if _, ok := s.sessions[sid]; !ok {
		return fmt.Errorf("%w: session %s", ErrNotFound, sid)
	}
	delete(s.sessions, sid)
	return s.save()
}

// Revoke ends a grant (and every browser signed in through it), or one
// browser session, by ID. It returns the sessions it ended.
func (s *Store) Revoke(id string) (Grant, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Grant{}, nil, err
	}
	now := s.now()
	if x, ok := s.sessions[id]; ok {
		if x.RevokedAt == nil {
			t := now.UTC()
			x.RevokedAt = &t
		}
		g := s.grants[x.Grant]
		if err := s.save(); err != nil {
			return Grant{}, nil, err
		}
		if g == nil {
			return Grant{}, []string{id}, nil
		}
		return public(g, now), []string{id}, nil
	}
	g, ok := s.grants[id]
	if !ok {
		return Grant{}, nil, fmt.Errorf("%w: no grant or session %s", ErrNotFound, id)
	}
	var ended []string
	t := now.UTC()
	for sid, x := range s.sessions {
		if x.Grant == id && x.RevokedAt == nil {
			ended = append(ended, sid)
			x.RevokedAt = &t
		}
	}
	if g.RevokedAt == nil {
		g.RevokedAt = &t
	}
	if err := s.save(); err != nil {
		return Grant{}, nil, err
	}
	return public(g, now), ended, nil
}

// Grants lists grants (oldest first) and the sessions open through them.
func (s *Store) Grants() ([]Grant, []Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, nil, err
	}
	now := s.now()
	gs := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		gs = append(gs, public(g, now))
	}
	slices.SortFunc(gs, func(a, b Grant) int { return a.CreatedAt.Compare(b.CreatedAt) })
	xs := make([]Session, 0, len(s.sessions))
	for _, x := range s.sessions {
		if g := s.grants[x.Grant]; x.RevokedAt != nil || g == nil || g.stateAt(now) != observer.StateActive {
			continue
		}
		y := *x
		y.Hash = ""
		xs = append(xs, y)
	}
	slices.SortFunc(xs, func(a, b Session) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return gs, xs, nil
}

func hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func sameHash(stored, secret string) bool {
	want, err := hex.DecodeString(stored)
	if err != nil || len(want) != sha256.Size {
		want = make([]byte, sha256.Size)
	}
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(sum[:], want) == 1 && err == nil
}

func parseSecret(v, prefix string) (id, secret string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(v), prefix)
	if !ok {
		return "", "", false
	}
	id, secret, ok = strings.Cut(rest, "_")
	if !ok || len(id) != 12 || len(secret) < 32 || len(secret) > 128 {
		return "", "", false
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", "", false
	}
	return id, secret, true
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sortedUnique(xs []string) []string {
	xs = slices.Clone(xs)
	slices.Sort(xs)
	return slices.Compact(xs)
}
