package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/protocol"
)

// Binding is the first line `hive proxy --member` writes on the member
// socket, before anything from the client: which member the connection is,
// as the forced command in authorized_keys says, and where ssh says it
// comes from.
type Binding struct {
	Member string `json:"member"`
	From   string `json:"from,omitempty"`
}

// Member socket limits.
const (
	// DefaultMaxConnections bounds open member connections, all members
	// together.
	DefaultMaxConnections = 32
	bindTimeout           = 10 * time.Second
	maxBindingBytes       = 1024
	writeTimeout          = 30 * time.Second
	// refusedLinger keeps a refused connection open long enough for its
	// client to read why.
	refusedLinger = 10 * time.Second
	// endDelay lets a final answer reach the client before its connection
	// is closed.
	endDelay = 100 * time.Millisecond
)

// BaseMethods are what every member may call, whatever its role: being
// connected, the daemon's version and API level (which clients check
// before anything else), and finding out who it is.
var BaseMethods = []string{"runtime.ping", "runtime.status", "client.report", "access.whoami"}

// Config assembles a Server.
type Config struct {
	Store *Store
	Audit *Audit
	// Handler serves what a member may call: the daemon's router.
	Handler protocol.Handler
	// Methods lists Handler's methods, for the welcome.
	Methods func() []string
	// Allowed says whether m may call method at all; nil allows
	// BaseMethods only.
	Allowed func(m Member, method string) bool
	Logger  *slog.Logger
	// MaxConnections bounds open connections; zero means
	// DefaultMaxConnections.
	MaxConnections int
	// OnChange, when set, is told whenever members connect or leave.
	OnChange func()
}

// Server answers members on the member socket.
type Server struct {
	cfg Config
	log *slog.Logger

	mu    sync.Mutex
	conns map[*conn]struct{}
}

// NewServer returns a server; Serve each accepted connection.
func NewServer(cfg Config) *Server {
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = DefaultMaxConnections
	}
	if cfg.Allowed == nil {
		cfg.Allowed = func(_ Member, method string) bool { return slices.Contains(BaseMethods, method) }
	}
	if cfg.Methods == nil {
		cfg.Methods = func() []string { return nil }
	}
	return &Server{cfg: cfg, log: logging.OrDiscard(cfg.Logger), conns: map[*conn]struct{}{}}
}

// SetAudit sets where access is audited. Call it before serving.
func (s *Server) SetAudit(a *Audit) { s.cfg.Audit = a }

// Serve answers one connection until it ends, it is ended (its member
// revoked or expired) or ctx is done. It closes raw.
func (s *Server) Serve(ctx context.Context, raw net.Conn, info protocol.ServerInfo) {
	defer raw.Close()
	s.mu.Lock()
	full := len(s.conns) >= s.cfg.MaxConnections
	s.mu.Unlock()
	if full {
		s.log.Warn("refused a member connection: too many open", "max", s.cfg.MaxConnections)
		return
	}
	b, err := readBinding(raw)
	if err != nil {
		s.log.Warn("refused a member connection", "err", err)
		s.cfg.Audit.Record(AuditEntry{Action: AuditRefused, Detail: err.Error()})
		return
	}
	c := &conn{s: s, raw: raw, id: b.Member, from: b.From}
	m, err := s.cfg.Store.Check(b.Member)
	if err != nil {
		c.refused = refusal(m, err)
		s.cfg.Audit.Record(AuditEntry{Action: AuditRefused, Member: b.Member, Name: m.Name, From: b.From, Detail: c.refused})
		c.timer = time.AfterFunc(refusedLinger, func() { c.end("refused") })
	} else {
		c.member = m
		s.cfg.Store.Seen(m.ID)
		s.cfg.Audit.Record(AuditEntry{Action: AuditConnected, Member: m.ID, Name: m.Name, From: b.From})
		c.arm(m.ExpiresAt)
	}
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	if c.refused == "" {
		s.changed()
	}
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.end("closed")
		if c.refused == "" {
			s.changed()
		}
	}()
	_ = protocol.ServeConn(ctx, deadlineConn{Conn: raw}, c, info)
}

// readBinding reads the proxy's first line, byte by byte so nothing of the
// client's protocol is read with it.
func readBinding(raw net.Conn) (Binding, error) {
	_ = raw.SetReadDeadline(time.Now().Add(bindTimeout))
	defer func() { _ = raw.SetReadDeadline(time.Time{}) }()
	var line []byte
	one := make([]byte, 1)
	for {
		if _, err := raw.Read(one); err != nil {
			return Binding{}, fmt.Errorf("no member binding: %w", err)
		}
		if one[0] == '\n' {
			break
		}
		line = append(line, one[0])
		if len(line) > maxBindingBytes {
			return Binding{}, errors.New("member binding too long")
		}
	}
	var b Binding
	if err := json.Unmarshal(line, &b); err != nil {
		return Binding{}, fmt.Errorf("bad member binding: %w", err)
	}
	if !ValidID(b.Member) {
		return Binding{}, fmt.Errorf("bad member binding: member %q", b.Member)
	}
	if len(b.From) > 128 {
		b.From = b.From[:128]
	}
	return b, nil
}

func refusal(m Member, err error) string {
	who := m.Name
	if who == "" {
		who = "this member"
	}
	switch {
	case errors.Is(err, ErrExpired):
		return who + "'s access expired"
	case errors.Is(err, ErrRevoked):
		return who + "'s access was revoked"
	case errors.Is(err, ErrNotFound):
		return "no such member"
	}
	return err.Error()
}

// Close ends every connection: the daemon is stopping.
func (s *Server) Close() {
	for _, c := range s.all("") {
		c.end("the daemon stopped")
	}
}

// Revoked ends every connection of member id at once.
func (s *Server) Revoked(id string) {
	for _, c := range s.all(id) {
		c.end("revoked")
	}
}

// Changed re-reads member id after its access changed: connections of a
// member no longer active end, and the others follow its new expiry.
func (s *Server) Changed(id string) {
	m, err := s.cfg.Store.Check(id)
	for _, c := range s.all(id) {
		if err != nil {
			c.end(refusal(m, err))
			continue
		}
		c.mu.Lock()
		c.member = m
		c.mu.Unlock()
		c.arm(m.ExpiresAt)
	}
}

// all lists the accepted connections of member id, or of every member.
func (s *Server) all(id string) []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*conn
	for c := range s.conns {
		if c.refused == "" && (id == "" || c.id == id) {
			out = append(out, c)
		}
	}
	return out
}

// Connections counts open accepted connections by member.
func (s *Server) Connections() map[string]int {
	out := map[string]int{}
	for _, c := range s.all("") {
		out[c.id]++
	}
	return out
}

func (s *Server) changed() {
	if s.cfg.OnChange != nil {
		s.cfg.OnChange()
	}
}

// deadlineConn bounds every write, so a member that stops reading is
// disconnected instead of holding its connection's writer forever.
type deadlineConn struct{ net.Conn }

func (c deadlineConn) Write(b []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.Conn.Write(b)
}

// conn is one member connection.
type conn struct {
	s    *Server
	raw  net.Conn
	id   string
	from string
	// refused, when set, is why the connection is not accepted: every
	// request answers it until the connection closes.
	refused string

	mu     sync.Mutex
	member Member
	client string
	timer  *time.Timer
	ended  bool
}

// arm ends the connection when its member expires.
func (c *conn) arm(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(time.Until(at), func() { c.end("expired") })
}

// end closes the connection once, recording why.
func (c *conn) end(reason string) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	m, client := c.member, c.client
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
	_ = c.raw.Close()
	if c.refused == "" {
		c.s.cfg.Audit.Record(AuditEntry{Action: AuditDisconnected, Member: m.ID, Name: m.Name, From: c.from, Client: client, Detail: reason})
	}
}

// Handle checks the member, then what it asks for, before serving it.
func (c *conn) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	if p := protocol.PeerOf(ctx); p != nil {
		c.mu.Lock()
		c.client = p.Client
		c.mu.Unlock()
	}
	if c.refused != "" {
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New(c.refused)))
	}
	m, err := c.s.cfg.Store.Check(c.id)
	if err != nil {
		why := refusal(m, err)
		time.AfterFunc(endDelay, func() { c.end(why) })
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New(why)))
	}
	if !c.s.cfg.Allowed(m, req.Method) {
		c.s.cfg.Audit.Record(AuditEntry{
			Action: AuditDenied, Member: m.ID, Name: m.Name, From: c.from, Client: c.clientName(),
			Method: req.Method, Outcome: protocol.ErrorCodePermissionDenied,
		})
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodePermissionDenied,
			fmt.Errorf("member %s may not call %s", m.Name, req.Method)))
	}
	return c.s.cfg.Handler.Handle(WithCaller(ctx, Caller{Member: m, From: c.from}), req)
}

// Methods lists what the member may call, for the welcome.
func (c *conn) Methods() []string {
	if c.refused != "" {
		return nil
	}
	c.mu.Lock()
	m := c.member
	c.mu.Unlock()
	var out []string
	for _, method := range c.s.cfg.Methods() {
		if c.s.cfg.Allowed(m, method) {
			out = append(out, method)
		}
	}
	return out
}

func (c *conn) clientName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}
