package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
)

// ObserverSocketName is the observer socket's name in the storage root.
const ObserverSocketName = "observer.sock"

// ObserversConfig is the [observers] policy.
type ObserversConfig struct {
	// Disabled turns the observer socket off; credentials are kept.
	Disabled bool
	// History is how many events observers can catch up with; zero means
	// observer.DefaultHistory.
	History int
	// MaxConnections bounds open observer connections; zero means
	// observer.DefaultMaxConnections.
	MaxConnections int
}

// EventObserverActivity says who observes now: observer.Activity. The
// multiplexer shows it, so people know when they are being watched.
const EventObserverActivity = "observer.activity"

// ObserverSocketPath is where the daemon storing its data in root listens
// for observers.
func ObserverSocketPath(root string) string {
	return platform.SocketPath(root, ObserverSocketName)
}

// observers serves the observer socket: read-only, scoped access for
// programs holding an observer credential (see package observer). It is a
// listener of its own, so observers never share the control socket.
type observers struct {
	cfg     Config
	log     *slog.Logger
	dir     string
	path    string
	store   *observer.Store
	history *observer.History
	index   *observer.Index
	server  *observer.Server
	bus     *event.Bus

	auditOnce sync.Once
	audit     *observer.Audit
}

func newObservers(cfg Config, log *slog.Logger, bus *event.Bus, envs environment.Service, panes *pane.Service, src observer.Sources) *observers {
	dir := filepath.Join(cfg.root(), "observers")
	o := &observers{
		cfg: cfg, log: log, dir: dir, path: ObserverSocketPath(cfg.root()), bus: bus,
		store:   observer.NewStore(dir),
		history: observer.NewHistory(cfg.Observers.History),
		index:   observer.NewIndex(func(ctx context.Context) ([]pane.Pane, error) { return panes.Panes(ctx, "", "") }),
	}
	src.Environments = envs
	src.Panes = panes
	o.server = observer.NewServer(observer.Config{
		Store: o.store, History: o.history, Sources: src, Logger: log, MaxConnections: cfg.Observers.MaxConnections,
		OnActivity: func(a observer.Activity) { bus.Publish(EventObserverActivity, a) },
	})
	return o
}

// auditLog opens the audit log on first use.
func (o *observers) auditLog() *observer.Audit {
	o.auditOnce.Do(func() {
		a, err := observer.OpenAudit(o.dir)
		if err != nil {
			o.log.Warn("observer audit log unavailable", "err", err)
			return
		}
		o.audit = a
	})
	return o.audit
}

// run records events for observers and serves the observer socket until
// ctx ends.
func (o *observers) run(ctx context.Context) {
	if o.cfg.Observers.Disabled {
		return
	}
	o.server.SetAudit(o.auditLog())
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { observer.RecordEvents(ctx, o.bus, o.index, o.history) })

	_ = os.Remove(o.path) // left by a daemon that crashed; the daemon lock is ours
	ln, err := o.cfg.listener().Listen("unix", o.path)
	if err != nil {
		o.log.Warn("observer socket unavailable", "path", o.path, "err", err)
		return
	}
	if err := os.Chmod(o.path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		o.log.Warn("observer socket not secured; closing it", "err", err)
		_ = ln.Close()
		return
	}
	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close()
		o.server.Close() // observers do not hold the daemon up
	})
	defer stop()
	defer os.Remove(o.path)
	info := protocol.ServerInfo{Version: buildinfo.Get().Version}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if err := platform.CheckPeer(conn); err != nil {
			o.log.Warn("refused a connection to the observer socket", "err", err)
			_ = conn.Close()
			continue
		}
		wg.Go(func() { o.server.Serve(ctx, conn, info) })
	}
}

// Observer admin methods, on the control socket only: credentials are
// made, listed and revoked by whoever controls the daemon.

// ObserverCreateParams describes a new observer credential.
type ObserverCreateParams struct {
	Name    string         `json:"name"`
	Scope   observer.Scope `json:"scope"`
	Screens bool           `json:"screens,omitempty"`
	// TTLSeconds is how long it lasts; zero means observer.DefaultTTL.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

// ObserverCreated is a new credential with its token, shown only now, and
// where observers connect.
type ObserverCreated struct {
	Credential observer.Credential `json:"credential"`
	Token      string              `json:"token"`
	Socket     string              `json:"socket"`
}

// ObserverInfo is a credential and its open connections.
type ObserverInfo struct {
	observer.Credential
	Connections int `json:"connections"`
}

// ObserverList is observer.list.
type ObserverList struct {
	Socket      string         `json:"socket"`
	Enabled     bool           `json:"enabled"`
	Credentials []ObserverInfo `json:"credentials"`
}

type observerRef struct {
	ID string `json:"id"`
}

type observerValidateParams struct {
	Token string `json:"token"`
}

// ObserverValidation says whether a token is usable, and as what.
type ObserverValidation struct {
	Valid      bool                 `json:"valid"`
	State      string               `json:"state"` // active, expired, revoked or invalid
	Credential *observer.Credential `json:"credential,omitempty"`
}

type observerAuditParams struct {
	Credential string `json:"credential,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

func registerObservers(r *protocol.Router, o *observers, envs environment.Service, panes *pane.Service) {
	r.MustRegister("observer.create", protocol.Method(func(ctx context.Context, p ObserverCreateParams) (ObserverCreated, error) {
		for _, id := range p.Scope.Environments {
			if _, err := envs.Get(ctx, id); err != nil {
				return ObserverCreated{}, protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf("environment %q: %w", id, err))
			}
		}
		for _, id := range p.Scope.Panes {
			if _, err := panes.Pane(ctx, id); err != nil {
				return ObserverCreated{}, protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf("pane %q: %w", id, err))
			}
		}
		cred, token, err := o.store.Create(observer.CreateRequest{
			Name: p.Name, Scope: p.Scope, Screens: p.Screens, TTL: time.Duration(p.TTLSeconds) * time.Second,
		})
		if err != nil {
			return ObserverCreated{}, observerError(err)
		}
		o.auditLog().Record(observer.AuditEntry{Action: observer.AuditCreated, Credential: cred.ID, Name: cred.Name, Detail: auditScope(cred)})
		return ObserverCreated{Credential: cred, Token: token, Socket: o.path}, nil
	}))
	r.MustRegister("observer.list", protocol.Method(func(context.Context, protocol.Empty) (ObserverList, error) {
		creds, err := o.store.List()
		if err != nil {
			return ObserverList{}, err
		}
		conns := o.server.Connections()
		out := ObserverList{Socket: o.path, Enabled: !o.cfg.Observers.Disabled, Credentials: []ObserverInfo{}}
		for _, c := range creds {
			out.Credentials = append(out.Credentials, ObserverInfo{Credential: c, Connections: conns[c.ID]})
		}
		return out, nil
	}))
	r.MustRegister("observer.get", protocol.Method(func(_ context.Context, p observerRef) (ObserverInfo, error) {
		c, err := o.store.Get(p.ID)
		if err != nil {
			return ObserverInfo{}, observerError(err)
		}
		return ObserverInfo{Credential: c, Connections: o.server.Connections()[c.ID]}, nil
	}))
	r.MustRegister("observer.revoke", protocol.Method(func(_ context.Context, p observerRef) (observer.Credential, error) {
		c, err := o.store.Revoke(p.ID)
		if err != nil {
			return observer.Credential{}, observerError(err)
		}
		o.server.Revoked(c.ID)
		o.auditLog().Record(observer.AuditEntry{Action: observer.AuditRevoked, Credential: c.ID, Name: c.Name})
		return c, nil
	}))
	r.MustRegister("observer.validate", protocol.Method(func(_ context.Context, p observerValidateParams) (ObserverValidation, error) {
		c, err := o.store.Inspect(p.Token)
		switch {
		case err == nil:
			return ObserverValidation{Valid: true, State: observer.StateActive, Credential: &c}, nil
		case errors.Is(err, observer.ErrExpired), errors.Is(err, observer.ErrRevoked):
			return ObserverValidation{State: c.State, Credential: &c}, nil
		case errors.Is(err, observer.ErrInvalidToken):
			return ObserverValidation{State: "invalid"}, nil
		}
		return ObserverValidation{}, err
	}))
	r.MustRegister("observer.activity", protocol.Method(func(context.Context, protocol.Empty) (observer.Activity, error) {
		return o.server.Activity(), nil
	}))
	r.MustRegister("observer.audit", protocol.Method(func(_ context.Context, p observerAuditParams) ([]observer.AuditEntry, error) {
		entries, err := o.auditLog().Read(p.Limit, p.Credential)
		if entries == nil {
			entries = []observer.AuditEntry{}
		}
		return entries, err
	}))
}

func auditScope(c observer.Credential) string {
	s := "scope " + c.Scope.String()
	if c.Screens {
		s += ", screens"
	}
	return s
}

func observerError(err error) error {
	switch {
	case errors.Is(err, observer.ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, observer.ErrInvalid):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}
