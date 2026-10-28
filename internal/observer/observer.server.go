package observer

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/terminal"
)

// Limits that keep observers from crowding out interactive clients.
const (
	// DefaultMaxConnections bounds open observer connections, all
	// credentials together.
	DefaultMaxConnections = 32
	// maxPerCredential bounds one credential's connections.
	maxPerCredential = 8
	// maxStreams bounds one connection's open event and screen streams.
	maxStreams = 8
	// maxInFlight bounds one connection's concurrent requests.
	maxInFlight = 8
	// requestRate and requestBurst bound one connection's request rate.
	requestRate  = 20.0
	requestBurst = 40.0
	// maxAuthFailures ends a connection that keeps presenting bad tokens.
	maxAuthFailures = 3
	// writeTimeout ends a connection that stops reading: a stalled
	// observer holds nothing for longer than this.
	writeTimeout = 30 * time.Second
	// authTimeout ends a connection that has not authenticated by then.
	authTimeout = 10 * time.Second
)

// Sources is the daemon state observers read. Everything they read goes
// through the views in this package.
type Sources struct {
	Environments interface {
		List(ctx context.Context) ([]environment.Environment, error)
	}
	Panes interface {
		Tabs(ctx context.Context, envID string) ([]pane.Tab, error)
		Panes(ctx context.Context, envID, tabID string) ([]pane.Pane, error)
		Pane(ctx context.Context, id string) (pane.Pane, error)
	}
	Agents interface {
		List(ctx context.Context, all bool) ([]agent.Agent, error)
	}
	Terminals interface {
		Get(processID string) (terminal.Session, error)
	}
	// Metadata lists reported metadata; nil means none.
	Metadata func() []metadata.Entry
	// Plans lists the plans, newest first; nil means none.
	Plans interface {
		All(ctx context.Context) []task.Plan
	}
	// Plugins lists the installed plugins; nil means none.
	Plugins func(ctx context.Context) []Plugin
	// Insights reads time and usage; nil means none are kept.
	Insights interface {
		Summary(ctx context.Context, q insights.Query) (insights.Summary, error)
		Asks(ctx context.Context, q insights.Query, limit int) ([]insights.Ask, error)
		Budgets(ctx context.Context) []insights.BudgetStatus
	}
	// Info describes the daemon.
	Info func() Info
}

// Info describes the daemon for observe.status.
type Info struct {
	Version              string    `json:"version"`
	Session              string    `json:"session"`
	StartedAt            time.Time `json:"started_at"`
	AgentsSurviveRestart bool      `json:"agents_survive_restart"`
}

// Config assembles a Server.
type Config struct {
	Store   *Store
	History *History
	Audit   *Audit
	Sources Sources
	Logger  *slog.Logger
	// MaxConnections bounds open connections; zero means
	// DefaultMaxConnections.
	MaxConnections int
	// OnActivity, when set, is told whenever observers connect or leave,
	// or start or stop watching a screen: what the daemon shows its users
	// so they know they are being observed.
	OnActivity func(Activity)
}

// Activity is who observes the daemon now.
type Activity struct {
	Observers []ActiveObserver `json:"observers"`
	// Watched counts the screen watches open on each pane, by pane ID.
	Watched map[string]int `json:"watched"`
}

// ActiveObserver is a credential with open connections.
type ActiveObserver struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Connections int    `json:"connections"`
}

// Server answers observers on the observer socket.
type Server struct {
	cfg    Config
	log    *slog.Logger
	router *protocol.Router

	mu       sync.Mutex
	sessions map[*session]struct{}
	watched  map[string]int // pane → open watches
}

// NewServer returns a server; Serve each accepted connection.
func NewServer(cfg Config) *Server {
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = DefaultMaxConnections
	}
	s := &Server{cfg: cfg, log: logging.OrDiscard(cfg.Logger), router: protocol.NewRouter(), sessions: map[*session]struct{}{}, watched: map[string]int{}}
	s.register(s.router)
	return s
}

// SetAudit sets where access is audited. Call it before serving.
func (s *Server) SetAudit(a *Audit) { s.cfg.Audit = a }

// Router is the observer API, for its schema.
func (s *Server) Router() *protocol.Router { return s.router }

// Serve answers one connection until it ends, it is ended (its credential
// revoked or expired) or ctx is done. It closes conn.
func (s *Server) Serve(ctx context.Context, conn net.Conn, info protocol.ServerInfo) {
	c := &session{s: s, raw: conn, inFlight: make(chan struct{}, maxInFlight), tokens: requestBurst, refilled: time.Now()}
	s.mu.Lock()
	if len(s.sessions) >= s.cfg.MaxConnections {
		s.mu.Unlock()
		s.log.Warn("refused an observer connection: too many open", "max", s.cfg.MaxConnections)
		_ = conn.Close()
		return
	}
	s.sessions[c] = struct{}{}
	s.mu.Unlock()
	unauthenticated := time.AfterFunc(authTimeout, func() {
		if c.current() == nil {
			c.end("did not authenticate")
		}
	})
	defer unauthenticated.Stop()
	defer func() {
		s.mu.Lock()
		delete(s.sessions, c)
		s.mu.Unlock()
		authenticated := c.current() != nil
		c.end("closed")
		if authenticated {
			s.changed()
		}
	}()
	_ = protocol.ServeConn(ctx, deadlineConn{Conn: conn}, c, info)
}

// Close ends every connection: the daemon is stopping.
func (s *Server) Close() {
	s.mu.Lock()
	all := make([]*session, 0, len(s.sessions))
	for c := range s.sessions {
		all = append(all, c)
	}
	s.mu.Unlock()
	for _, c := range all {
		c.end("the daemon stopped")
	}
}

// Revoked ends every connection of credential id.
func (s *Server) Revoked(id string) {
	for _, c := range s.sessionsOf(id) {
		c.end("revoked")
	}
}

func (s *Server) sessionsOf(id string) []*session {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*session
	for c := range s.sessions {
		if cred := c.current(); cred != nil && cred.ID == id {
			out = append(out, c)
		}
	}
	return out
}

// Activity reports who observes now.
func (s *Server) Activity() Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := Activity{Observers: []ActiveObserver{}, Watched: map[string]int{}}
	byID := map[string]int{}
	for c := range s.sessions {
		cred := c.current()
		if cred == nil {
			continue
		}
		i, ok := byID[cred.ID]
		if !ok {
			i = len(a.Observers)
			byID[cred.ID] = i
			a.Observers = append(a.Observers, ActiveObserver{ID: cred.ID, Name: cred.Name})
		}
		a.Observers[i].Connections++
	}
	slices.SortFunc(a.Observers, func(x, y ActiveObserver) int { return strings.Compare(x.Name+x.ID, y.Name+y.ID) })
	maps.Copy(a.Watched, s.watched)
	return a
}

// changed tells OnActivity what observes now.
func (s *Server) changed() {
	if s.cfg.OnActivity != nil {
		s.cfg.OnActivity(s.Activity())
	}
}

// watching counts a screen watch on pane (delta +1 or -1).
func (s *Server) watching(pane string, delta int) {
	s.mu.Lock()
	s.watched[pane] += delta
	if s.watched[pane] <= 0 {
		delete(s.watched, pane)
	}
	s.mu.Unlock()
	s.changed()
}

// Connections counts open authenticated connections by credential.
func (s *Server) Connections() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for c := range s.sessions {
		if cred := c.current(); cred != nil {
			out[cred.ID]++
		}
	}
	return out
}

// deadlineConn bounds every write, so an observer that stops reading is
// disconnected instead of holding its connection's writer forever.
type deadlineConn struct{ net.Conn }

func (c deadlineConn) Write(b []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.Conn.Write(b)
}

// session is one observer connection.
type session struct {
	s        *Server
	raw      net.Conn
	inFlight chan struct{}

	mu       sync.Mutex
	cred     *Credential
	client   string
	failures int
	streams  int
	tokens   float64
	refilled time.Time
	expiry   *time.Timer
	ended    bool
}

type sessionKey struct{}

func sessionOf(ctx context.Context) *session {
	c, _ := ctx.Value(sessionKey{}).(*session)
	return c
}

// Methods lists the observer API for the welcome.
func (c *session) Methods() []string { return c.s.router.Methods() }

// Handle authenticates, re-checks the credential and bounds the rate of
// every request before the observer router answers it.
func (c *session) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	if req.Method != methodAuth && req.Method != methodSchema {
		if _, err := c.credential(); err != nil {
			return protocol.Fail(req, err)
		}
		if !c.allow() {
			return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeUnavailable, errors.New("observer: too many requests; slow down")))
		}
	}
	select {
	case c.inFlight <- struct{}{}:
		defer func() { <-c.inFlight }()
	default:
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeUnavailable, errors.New("observer: too many requests in flight")))
	}
	return c.s.router.Handle(context.WithValue(ctx, sessionKey{}, c), req)
}

// allow takes one request from the connection's token bucket.
func (c *session) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.tokens = min(requestBurst, c.tokens+now.Sub(c.refilled).Seconds()*requestRate)
	c.refilled = now
	if c.tokens < 1 {
		return false
	}
	c.tokens--
	return true
}

func (c *session) current() *Credential {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cred
}

// errUnauthenticated answers anything but observe.auth before it.
var errUnauthenticated = protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New("observer: authenticate first (observe.auth)"))

// credential returns the connection's credential, checked again: a
// credential revoked or expired since ends the connection.
func (c *session) credential() (Credential, error) {
	cred := c.current()
	if cred == nil {
		return Credential{}, errUnauthenticated
	}
	fresh, err := c.s.cfg.Store.Check(cred.ID)
	if err != nil {
		reason := "revoked"
		if errors.Is(err, ErrExpired) {
			reason = "expired"
		}
		go c.end(reason)
		return Credential{}, protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New("observer: credential "+reason))
	}
	return fresh, nil
}

// authenticate binds the connection to token's credential.
func (c *session) authenticate(token, client string) (Credential, error) {
	c.mu.Lock()
	if c.cred != nil {
		c.mu.Unlock()
		return Credential{}, protocol.NewError(protocol.ErrorCodeConflict, errors.New("observer: this connection is already authenticated"))
	}
	c.mu.Unlock()
	cred, err := c.s.cfg.Store.Authenticate(token)
	if err != nil {
		c.s.cfg.Audit.Record(AuditEntry{Action: AuditRefused, Credential: TokenID(token), Detail: refusal(err), Client: client})
		c.mu.Lock()
		c.failures++
		tooMany := c.failures >= maxAuthFailures
		c.mu.Unlock()
		if tooMany {
			go c.end("too many failed attempts")
		}
		// One answer for every refusal: a client learns nothing about
		// which credentials exist.
		return Credential{}, protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New("observer: invalid, expired or revoked token"))
	}
	if n := len(c.s.sessionsOf(cred.ID)); n >= maxPerCredential {
		c.s.cfg.Audit.Record(AuditEntry{Action: AuditRefused, Credential: cred.ID, Name: cred.Name, Detail: "too many connections", Client: client})
		return Credential{}, protocol.NewError(protocol.ErrorCodeUnavailable, errors.New("observer: this credential has too many open connections"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended {
		return Credential{}, errUnauthenticated
	}
	c.cred, c.client = &cred, client
	c.expiry = time.AfterFunc(time.Until(cred.ExpiresAt), func() { c.end("expired") })
	c.s.cfg.Audit.Record(AuditEntry{Action: AuditConnected, Credential: cred.ID, Name: cred.Name, Client: client})
	go c.s.changed() // c.mu is held
	return cred, nil
}

func refusal(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrRevoked):
		return "revoked"
	case errors.Is(err, ErrInvalidToken):
		return "invalid token"
	}
	return err.Error()
}

// denied records a request outside the credential's scope.
func (c *session) denied(cred Credential, what string) {
	c.s.cfg.Audit.Record(AuditEntry{Action: AuditDenied, Credential: cred.ID, Name: cred.Name, Detail: what, Client: c.clientName()})
}

func (c *session) clientName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

// openStream counts a stream against the connection's limit.
func (c *session) openStream() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams >= maxStreams {
		return protocol.NewError(protocol.ErrorCodeUnavailable, errors.New("observer: too many open streams on this connection"))
	}
	c.streams++
	return nil
}

func (c *session) closeStream() {
	c.mu.Lock()
	c.streams--
	c.mu.Unlock()
}

// end closes the connection once, recording why.
func (c *session) end(reason string) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	cred, client := c.cred, c.client
	if c.expiry != nil {
		c.expiry.Stop()
	}
	c.mu.Unlock()
	_ = c.raw.Close()
	if cred != nil {
		c.s.cfg.Audit.Record(AuditEntry{Action: AuditDisconnected, Credential: cred.ID, Name: cred.Name, Detail: reason, Client: client})
	}
}
