// Package web is the web observer: a browser companion that shows Hive's
// agents, live, without being able to drive them.
//
// It is its own process (`hive web serve`), apart from the daemons it shows.
// It reaches each daemon (a runtime: one session on one machine) only
// through that daemon's observer socket, with an observer credential made
// for it (`hive web runtime add`); it holds no control socket and no way to
// type, change layouts or start anything. Browsers sign in with share links
// (`hive web share`): a link carries a one-time code (in the URL fragment,
// so it reaches no server log) that is exchanged for a session cookie. What
// a browser sees is the intersection of the runtime's observer scope and
// its grant's scope, and access ends when the grant expires or is revoked.
package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
)

// Config sets up the web observer.
type Config struct {
	// Dir holds its state: runtimes, grants, sessions, the audit log and
	// the admin socket.
	Dir string
	// Listen is the HTTP address (host:port).
	Listen string
	// PublicURL is where browsers reach it (for share links); empty
	// derives it from Listen.
	PublicURL string
	// TLSCert and TLSKey serve HTTPS.
	TLSCert, TLSKey string
	// BehindProxy says a reverse proxy in front terminates TLS: cookies
	// are Secure and X-Forwarded-For names the client in the audit log.
	BehindProxy bool
	Logger      *slog.Logger
	Version     string
}

// DefaultListen is where the web observer listens unless told otherwise.
const DefaultListen = "127.0.0.1:8484"

// ErrInsecure refuses to serve browsers on the network over plain HTTP.
var ErrInsecure = errors.New("web: serving beyond this machine needs TLS (--tls-cert/--tls-key) or a TLS-terminating proxy (--behind-proxy)")

// Validate checks the configuration is safe to serve.
func (c Config) Validate() error {
	if c.Dir == "" {
		return fmt.Errorf("%w: a state directory is required", ErrInvalid)
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("%w: listen address %q: %w", ErrInvalid, c.Listen, err)
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return fmt.Errorf("%w: give both --tls-cert and --tls-key", ErrInvalid)
	}
	if !loopback(host) && c.TLSCert == "" && !c.BehindProxy {
		return ErrInsecure
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("%w: public URL %q", ErrInvalid, c.PublicURL)
		}
	}
	return nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// secure reports whether browsers reach the web observer over HTTPS.
func (c Config) secure() bool {
	return c.TLSCert != "" || c.BehindProxy || strings.HasPrefix(c.PublicURL, "https://")
}

// publicURL is the base of share links.
func (c Config) publicURL() string {
	if c.PublicURL != "" {
		return strings.TrimSuffix(c.PublicURL, "/")
	}
	scheme := "http"
	if c.TLSCert != "" {
		scheme = "https"
	}
	host, port, _ := net.SplitHostPort(c.Listen)
	if host == "" || host == "0.0.0.0" || host == "::" || loopback(host) {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// AdminSocketPath is where `hive web` commands reach a running server.
func AdminSocketPath(dir string) string { return platform.SocketPath(dir, "web.sock") }

// Server is the web observer.
type Server struct {
	cfg     Config
	store   *Store
	audit   *observer.Audit
	log     *slog.Logger
	now     func() time.Time
	started time.Time
	limit   *bucket

	mu       sync.Mutex
	runtimes []Runtime
	conns    map[string]*conn
	streams  map[*stream]struct{}
}

// New opens the web observer's state.
func New(cfg Config) (*Server, error) {
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, err
	}
	audit, err := observer.OpenAudit(cfg.Dir)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, store: NewStore(cfg.Dir), audit: audit, log: logging.OrDiscard(cfg.Logger), now: time.Now,
		limit: newBucket(10, 20), conns: map[string]*conn{}, streams: map[*stream]struct{}{},
	}
	if err := s.Reload(); err != nil {
		_ = audit.Close()
		return nil, err
	}
	return s, nil
}

// Store is the server's state.
func (s *Server) Store() *Store { return s.store }

// Reload reads the runtimes again: removed or changed runtimes are
// disconnected (their viewers see them go).
func (s *Server) Reload() error {
	list, err := s.store.Runtimes()
	if err != nil {
		return err
	}
	s.mu.Lock()
	var stale []*conn
	for name, c := range s.conns {
		i := slices.IndexFunc(list, func(r Runtime) bool { return r.Name == name })
		if i < 0 || !sameRuntime(list[i], c.rt) {
			stale = append(stale, c)
			delete(s.conns, name)
		}
	}
	for _, r := range list {
		if _, ok := s.conns[r.Name]; !ok {
			s.conns[r.Name] = newConn(r, s.log)
		}
	}
	s.runtimes = list
	streams := make([]*stream, 0, len(s.streams))
	for st := range s.streams {
		streams = append(streams, st)
	}
	s.mu.Unlock()
	for _, c := range stale {
		c.Close()
	}
	for _, st := range streams {
		st.end("reload") // their runtimes may have changed: reconnect
	}
	return nil
}

func (s *Server) conn(name string) *conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[name]
}

// allowed lists the runtimes a grant may see that exist.
func (s *Server) allowed(g Grant) []Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Runtime
	for _, r := range s.runtimes {
		if slices.Contains(g.Runtimes, r.Name) {
			out = append(out, r)
		}
	}
	return out
}

// Share makes a grant and returns its link.
func (s *Server) Share(req ShareRequest) (Grant, string, error) {
	s.mu.Lock()
	runtimes := slices.Clone(s.runtimes)
	s.mu.Unlock()
	g, code, err := s.store.Share(req, runtimes)
	if err != nil {
		return Grant{}, "", err
	}
	s.audit.Record(observer.AuditEntry{
		Action: "shared", Credential: g.ID, Name: g.Label,
		Detail: fmt.Sprintf("runtimes %s, scope %s, screens %v, history %v, until %s", strings.Join(g.Runtimes, ","), scopeText(g.Scope), g.Screens, g.History, g.ExpiresAt.Format(time.RFC3339)),
	})
	return g, s.cfg.publicURL() + "/login#" + code, nil
}

func scopeText(sc observer.Scope) string {
	if sc.All {
		return "all"
	}
	return sc.String()
}

// Revoke ends a grant or one browser session; open pages end at once.
func (s *Server) Revoke(id string) (Grant, []string, error) {
	g, ended, err := s.store.Revoke(id)
	if err != nil {
		return Grant{}, nil, err
	}
	s.audit.Record(observer.AuditEntry{Action: "revoked", Credential: id, Name: g.Label, Detail: fmt.Sprintf("%d browser session(s) ended", len(ended))})
	s.endSessions(ended, "revoked")
	return g, ended, nil
}

func (s *Server) endSessions(ids []string, reason string) {
	s.mu.Lock()
	var hit []*stream
	for st := range s.streams {
		if slices.Contains(ids, st.session) {
			hit = append(hit, st)
		}
	}
	s.mu.Unlock()
	for _, st := range hit {
		st.end(reason)
	}
}

// Status is what `hive web status` shows.
type Status struct {
	Version   string          `json:"version"`
	Listen    string          `json:"listen"`
	PublicURL string          `json:"public_url"`
	StartedAt time.Time       `json:"started_at"`
	Viewers   int             `json:"viewers"`
	Runtimes  []RuntimeStatus `json:"runtimes"`
}

// RuntimeStatus is one runtime's connection.
type RuntimeStatus struct {
	Name     string    `json:"name"`
	Where    string    `json:"where"`
	State    string    `json:"state"`
	Error    string    `json:"error,omitempty"`
	SyncedAt time.Time `json:"synced_at,omitzero"`
}

// Status reports the server and its runtimes.
func (s *Server) Status() Status {
	s.mu.Lock()
	st := Status{Version: s.cfg.Version, Listen: s.cfg.Listen, PublicURL: s.cfg.publicURL(), StartedAt: s.started, Viewers: len(s.streams), Runtimes: []RuntimeStatus{}}
	conns := make([]*conn, 0, len(s.runtimes))
	for _, r := range s.runtimes {
		conns = append(conns, s.conns[r.Name])
	}
	s.mu.Unlock()
	for _, c := range conns {
		v := c.model.View()
		st.Runtimes = append(st.Runtimes, RuntimeStatus{Name: c.rt.Name, Where: c.rt.Where(), State: v.State, Error: v.Err, SyncedAt: v.SyncedAt})
	}
	return st
}

// Run serves browsers and the admin socket until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	s.started = s.now()
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	admin, err := s.listenAdmin(ctx)
	if err != nil {
		_ = ln.Close()
		return err
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}
	if s.cfg.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey)
		if err != nil {
			_ = ln.Close()
			_ = admin.Close()
			return fmt.Errorf("TLS: %w", err)
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
		ln = tls.NewListener(ln, srv.TLSConfig)
	}
	s.log.Info("web observer listening", "listen", s.cfg.Listen, "url", s.cfg.publicURL())
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
	case err := <-errc:
		_ = admin.Close()
		s.closeAll()
		return err
	}
	// Open streams are ended first, so Shutdown does not wait on them.
	s.mu.Lock()
	streams := make([]*stream, 0, len(s.streams))
	for st := range s.streams {
		streams = append(streams, st)
	}
	s.mu.Unlock()
	for _, st := range streams {
		st.end("shutdown")
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(sctx)
	_ = admin.Close()
	s.closeAll()
	return err
}

func (s *Server) closeAll() {
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	_ = s.audit.Close()
}

// listenAdmin serves `hive web` commands on a private unix socket.
func (s *Server) listenAdmin(ctx context.Context) (net.Listener, error) {
	path := AdminSocketPath(s.cfg.Dir)
	if err := platform.PrepareSocketDir(path); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("admin socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = ln.Close()
		return nil, err
	}
	router := protocol.NewRouter()
	s.registerAdmin(router)
	info := protocol.ServerInfo{Version: s.cfg.Version}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if err := platform.CheckPeer(c); err != nil {
				s.log.Warn("refused a connection to the web admin socket", "err", err)
				_ = c.Close()
				continue
			}
			go func() { _ = protocol.ServeConn(ctx, c, router, info) }()
		}
	}()
	return ln, nil
}

// bucket is a token bucket.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	at     time.Time
}

func newBucket(rate, burst float64) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, at: time.Now()}
}

func (b *bucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.at).Seconds()*b.rate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sameRuntime reports whether a and b connect the same way.
func sameRuntime(a, b Runtime) bool {
	return a.Name == b.Name && a.Host == b.Host && a.Session == b.Session && a.Socket == b.Socket &&
		a.Credential == b.Credential && a.TokenFile == b.TokenFile && a.Screens == b.Screens &&
		a.HivePath == b.HivePath && slices.Equal(a.SSH, b.SSH)
}
