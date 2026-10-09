package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/session"
	"github.com/admirable-oss/hive/internal/vt"
)

// stopTimeout bounds a remote runtime.shutdown: how long agents get to exit
// before the reply is sent anyway.
const stopTimeout = 10 * time.Second

// probeTimeout bounds the dial that checks whether a socket is still served.
const probeTimeout = 500 * time.Millisecond

type Status string

const (
	StatusStopped  Status = "stopped"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
)

// Snapshot is the daemon state reported by runtime.status.
type Snapshot struct {
	Status    Status    `json:"status"`
	Socket    string    `json:"socket"`
	StartedAt time.Time `json:"started_at"`
	// PID and Version identify the daemon process, so a client can notice
	// it is talking to a different build than itself.
	PID             int    `json:"pid"`
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocol_version"`
	// AgentsSurviveRestart is true when agents run under shims and keep
	// running when the daemon stops or restarts.
	AgentsSurviveRestart bool `json:"agents_survive_restart"`
	// Session is the session this daemon serves.
	Session string `json:"session"`
}

// Supervisor is what the server needs from the process domain: closing out
// records left by a crashed daemon, and stopping everything on shutdown.
type Supervisor interface {
	Recover(ctx context.Context) error
	StopAll(ctx context.Context) error
	// Detach lets go of running agents without stopping them; it reports
	// whether they keep running (false: they were stopped instead).
	Detach(ctx context.Context) (bool, error)
}

// Server owns the daemon socket. It serves handler on every accepted
// connection and, on shutdown, stops the agents and removes the socket.
type Server struct {
	cfg     Config
	handler protocol.Handler
	procs   Supervisor
	log     *slog.Logger

	mu        sync.Mutex
	lock      *instanceLock
	status    Status
	startedAt time.Time
	listener  net.Listener
	conns     map[net.Conn]struct{}
	cancel    context.CancelFunc
	workers   []func(ctx context.Context)

	wg       sync.WaitGroup
	done     chan struct{}
	doneOnce sync.Once
}

func NewServer(cfg Config, handler protocol.Handler, procs Supervisor) *Server {
	return &Server{
		cfg:     cfg,
		handler: handler,
		procs:   procs,
		log:     logging.OrDiscard(cfg.Logger),
		status:  StatusStopped,
		conns:   make(map[net.Conn]struct{}),
		done:    make(chan struct{}),
	}
}

// Start claims the socket and begins serving. It refuses to start when
// another daemon already answers on the socket.
func (s *Server) Start(ctx context.Context) error {
	if err := s.cfg.Validate(); err != nil {
		return err
	}
	// The socket's directory doubles as the lock's, so it must exist first.
	if err := platform.PrepareSocketDir(s.cfg.SocketPath); err != nil {
		return err
	}
	if err := os.MkdirAll(s.cfg.root(), 0o700); err != nil {
		return err
	}
	lock, err := acquireLock(filepath.Join(s.cfg.root(), pidFileName))
	if err != nil {
		return err
	}
	// Until Start succeeds, any failure must give the lock back.
	started := false
	defer func() {
		if !started {
			_ = lock.release()
		}
	}()
	if err := s.claimSocket(ctx); err != nil {
		return err
	}
	if err := s.procs.Recover(ctx); err != nil {
		return fmt.Errorf("recover processes: %w", err)
	}

	ln, err := s.cfg.listener().Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	// Anyone who can connect can run commands as this user, so the socket is
	// private regardless of the umask. (The directory is 0700 as well.)
	if err := os.Chmod(s.cfg.SocketPath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = ln.Close()
		return fmt.Errorf("secure socket: %w", err)
	}

	// Connections outlive Start's ctx (often a request or signal context), so
	// they get their own, cancelled by Stop.
	serveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	started = true
	s.mu.Lock()
	s.lock = lock
	s.listener = ln
	s.cancel = cancel
	s.status = StatusRunning
	s.startedAt = time.Now()
	s.mu.Unlock()

	s.wg.Add(1)
	go s.accept(serveCtx, ln)
	for _, w := range s.workers {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			w(serveCtx)
		}()
	}
	s.log.Info("daemon listening", "path", s.cfg.SocketPath, "version", buildinfo.Get().Version, "pid", os.Getpid())
	return nil
}

// Go adds a background task that runs from Start until Stop. It must be
// called before Start.
func (s *Server) Go(task func(ctx context.Context)) {
	s.workers = append(s.workers, task)
}

// Stop shuts the daemon down, closes every open connection and waits for
// their handlers to return. Agents keep running when they can (under
// shims); a later daemon adopts them. It is safe to call more than once.
func (s *Server) Stop(ctx context.Context) error {
	return s.stop(ctx, false)
}

// StopWithAgents is Stop, but it stops every agent first.
func (s *Server) StopWithAgents(ctx context.Context) error {
	return s.stop(ctx, true)
}

func (s *Server) stop(ctx context.Context, stopAgents bool) error {
	err := s.shutdown(ctx, stopAgents)

	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
	s.markDone()
	return err
}

// Done is closed once the daemon has been stopped, locally or remotely.
func (s *Server) Done() <-chan struct{} { return s.done }

func (s *Server) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Status:               s.status,
		Socket:               s.cfg.SocketPath,
		StartedAt:            s.startedAt,
		PID:                  os.Getpid(),
		Version:              buildinfo.Get().Version,
		ProtocolVersion:      protocol.Version2,
		AgentsSurviveRestart: s.cfg.Shim != nil,
		Session:              session.Normalize(s.cfg.Session),
	}
}

// shutdown stops accepting connections, removes the socket and either stops
// every agent or detaches from them. Open connections are left alone so a
// runtime.shutdown caller still gets its reply; Stop closes them afterwards.
func (s *Server) shutdown(ctx context.Context, stopAgents bool) error {
	s.mu.Lock()
	ln := s.listener
	s.listener = nil
	if ln != nil {
		s.status = StatusStopping
	}
	s.mu.Unlock()
	if ln == nil {
		return nil
	}

	errs := []error{ln.Close()}
	if err := os.Remove(s.cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	if stopAgents {
		s.log.Info("daemon stopping; stopping all agents")
		errs = append(errs, s.procs.StopAll(ctx))
	} else {
		kept, err := s.procs.Detach(ctx)
		errs = append(errs, err)
		if kept {
			s.log.Info("daemon stopping; agents keep running and will be re-attached by the next daemon")
		} else {
			s.log.Info("daemon stopping; agents could not outlive it and were stopped")
		}
	}

	s.mu.Lock()
	s.status = StatusStopped
	lock := s.lock
	s.lock = nil
	s.mu.Unlock()
	errs = append(errs, lock.release())
	err := errors.Join(errs...)
	if err != nil {
		s.log.Error("daemon stopped with errors", "err", err)
	} else {
		s.log.Info("daemon stopped")
	}
	return err
}

func (s *Server) markDone() { s.doneOnce.Do(func() { close(s.done) }) }

func (s *Server) accept(ctx context.Context, ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.log.Error("accept failed; no longer accepting connections", "err", err)
			}
			return // listener closed
		}
		if !s.track(conn) {
			_ = conn.Close()
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.untrack(conn)
			info := protocol.ServerInfo{Version: buildinfo.Get().Version, Capabilities: []string{vt.FrameCapability, vt.FrameLinksCapability}}
			if err := protocol.ServeConn(ctx, conn, s.handler, info); err != nil && !errors.Is(err, net.ErrClosed) {
				s.log.Debug("connection ended with an error", "err", err)
			}
		}()
	}
}

// track registers conn so Stop can close it; it reports false once the
// server is shutting down.
func (s *Server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != StatusRunning {
		return false
	}
	s.conns[conn] = struct{}{}
	return true
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
}

// claimSocket prepares the socket path. Callers hold the instance lock, so a
// socket that still answers belongs to a daemon on another storage root
// sharing this socket path; one that does not is left over from a crash.
func (s *Server) claimSocket(ctx context.Context) error {
	path := s.cfg.SocketPath
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var d net.Dialer
	if conn, err := d.DialContext(ctx, "unix", path); err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err == nil {
		s.log.Info("removed stale socket left by a previous daemon", "path", path)
	}
	return nil
}
