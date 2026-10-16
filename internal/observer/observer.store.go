package observer

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
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// Credential lifetimes. Every credential expires; DefaultTTL applies when a
// request does not say.
const (
	MinTTL     = time.Minute
	MaxTTL     = 365 * 24 * time.Hour
	DefaultTTL = 30 * 24 * time.Hour
)

// keepEnded is how long a revoked or expired credential stays listed.
const keepEnded = 30 * 24 * time.Hour

// maxCredentials bounds one session's credentials.
const maxCredentials = 1000

// touchEvery bounds how often a credential's last use is written to disk.
const touchEvery = time.Minute

// Errors a credential check returns. The observer socket answers each with
// the same message, so a client cannot probe which credentials exist.
var (
	ErrInvalidToken = errors.New("observer: invalid token")
	ErrExpired      = errors.New("observer: credential expired")
	ErrRevoked      = errors.New("observer: credential revoked")
	ErrNotFound     = errors.New("observer: no such credential")
)

// tokenPrefix starts every observer token, so they are recognisable (and
// scanners can find leaked ones).
const tokenPrefix = "hvo_"

// CreateRequest describes a new credential.
type CreateRequest struct {
	Name    string        `json:"name"`
	Scope   Scope         `json:"scope"`
	Screens bool          `json:"screens,omitempty"`
	TTL     time.Duration `json:"ttl,omitempty"` // zero means DefaultTTL
}

// Store keeps a session's observer credentials in one file. Only the daemon
// writes it.
type Store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	loaded  bool
	creds   map[string]*stored
	touched map[string]time.Time
}

type stored struct {
	Credential
	Hash string `json:"hash"`
}

type storeFile struct {
	Schema      int       `json:"schema"`
	Credentials []*stored `json:"credentials"`
}

// NewStore returns the store kept in dir.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "credentials.json"), now: time.Now, touched: map[string]time.Time{}}
}

func (s *Store) load() error {
	if s.loaded {
		return nil
	}
	var f storeFile
	if err := jsonfile.Read(s.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("observer credentials: %w", err)
	}
	s.creds = map[string]*stored{}
	for _, c := range f.Credentials {
		if c != nil && c.ID != "" {
			c.hash = c.Hash
			s.creds[c.ID] = c
		}
	}
	s.loaded = true
	return nil
}

func (s *Store) save() error {
	now := s.now()
	f := storeFile{Schema: 1}
	for id, c := range s.creds {
		if c.StateAt(now) != StateActive && now.Sub(c.endedAt()) > keepEnded {
			delete(s.creds, id)
			continue
		}
		f.Credentials = append(f.Credentials, c)
	}
	slices.SortFunc(f.Credentials, func(a, b *stored) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return jsonfile.Write(s.path, f) // 0600: a temp file renamed into place
}

func (c *stored) endedAt() time.Time {
	if c.RevokedAt != nil {
		return *c.RevokedAt
	}
	return c.ExpiresAt
}

func (s *Store) public(c *stored) Credential {
	out := c.Credential
	out.State = c.StateAt(s.now())
	out.hash = ""
	return out
}

// Create makes a credential and returns it with its token, which is not
// stored and cannot be shown again.
func (s *Store) Create(req CreateRequest) (Credential, string, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 || strings.ContainsFunc(req.Name, unicode.IsControl) {
		return Credential{}, "", fmt.Errorf("%w: a name is 1 to 64 printable characters", ErrInvalid)
	}
	if err := req.Scope.Validate(); err != nil {
		return Credential{}, "", err
	}
	if req.TTL == 0 {
		req.TTL = DefaultTTL
	}
	if req.TTL < MinTTL || req.TTL > MaxTTL {
		return Credential{}, "", fmt.Errorf("%w: expiry must be between %s and %s", ErrInvalid, MinTTL, MaxTTL)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Credential{}, "", err
	}
	if len(s.creds) >= maxCredentials {
		return Credential{}, "", fmt.Errorf("%w: at most %d credentials (revoke unused ones)", ErrInvalid, maxCredentials)
	}
	id, secret := randomHex(6), randomSecret()
	now := s.now().UTC().Truncate(time.Second)
	sum := sha256.Sum256([]byte(secret))
	c := &stored{Credential: Credential{
		ID: id, Name: req.Name, Scope: normalScope(req.Scope), Screens: req.Screens,
		CreatedAt: now, ExpiresAt: now.Add(req.TTL),
	}, Hash: hex.EncodeToString(sum[:])}
	c.hash = c.Hash
	s.creds[id] = c
	if err := s.save(); err != nil {
		delete(s.creds, id)
		return Credential{}, "", err
	}
	return s.public(c), tokenPrefix + id + "_" + secret, nil
}

func normalScope(s Scope) Scope {
	s.Environments = sortedUnique(s.Environments)
	s.Panes = sortedUnique(s.Panes)
	return s
}

func sortedUnique(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	xs = slices.Clone(xs)
	slices.Sort(xs)
	return slices.Compact(xs)
}

// List returns every credential, oldest first, without hashes.
func (s *Store) List() ([]Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	out := make([]Credential, 0, len(s.creds))
	for _, c := range s.creds {
		out = append(out, s.public(c))
	}
	slices.SortFunc(out, func(a, b Credential) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

// Get returns credential id.
func (s *Store) Get(id string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Credential{}, err
	}
	c, ok := s.creds[id]
	if !ok {
		return Credential{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return s.public(c), nil
}

// Revoke ends credential id now. Revoking it again changes nothing.
func (s *Store) Revoke(id string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Credential{}, err
	}
	c, ok := s.creds[id]
	if !ok {
		return Credential{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if c.RevokedAt == nil {
		now := s.now().UTC()
		c.RevokedAt = &now
		if err := s.save(); err != nil {
			c.RevokedAt = nil
			return Credential{}, err
		}
	}
	return s.public(c), nil
}

// Authenticate returns the active credential token belongs to, and records
// its use.
func (s *Store) Authenticate(token string) (Credential, error) {
	return s.match(token, true)
}

// Inspect returns the credential token belongs to, like Authenticate, but
// without recording a use: for checking a token.
func (s *Store) Inspect(token string) (Credential, error) {
	return s.match(token, false)
}

func (s *Store) match(token string, use bool) (Credential, error) {
	id, secret, ok := parseToken(token)
	if !ok {
		return Credential{}, ErrInvalidToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Credential{}, err
	}
	c, ok := s.creds[id]
	sum := sha256.Sum256([]byte(secret))
	want := make([]byte, sha256.Size)
	if ok {
		if h, err := hex.DecodeString(c.hash); err == nil && len(h) == sha256.Size {
			want = h
		}
	}
	if subtle.ConstantTimeCompare(sum[:], want) != 1 || !ok {
		return Credential{}, ErrInvalidToken
	}
	if err := stateErr(c.StateAt(s.now())); err != nil {
		return s.public(c), err
	}
	now := s.now().UTC()
	if use && now.Sub(s.touched[id]) >= touchEvery {
		s.touched[id] = now
		c.LastUsedAt = &now
		_ = s.save() // a missed last-use time is not worth failing a login
	}
	return s.public(c), nil
}

// Check returns credential id when it is still active: what a connection
// that authenticated earlier re-checks before each request.
func (s *Store) Check(id string) (Credential, error) {
	c, err := s.Get(id)
	if err != nil {
		return Credential{}, err
	}
	return c, stateErr(c.State)
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

// TokenID returns the credential ID a token names, or "" for something that
// is not a token. It does not check the token.
func TokenID(token string) string {
	id, _, _ := parseToken(token)
	return id
}

func parseToken(token string) (id, secret string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(token), tokenPrefix)
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
