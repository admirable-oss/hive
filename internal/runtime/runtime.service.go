package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
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
}

// Supervisor is what the server needs from the process domain: closing out
// records left by a crashed daemon, and stopping everything on shutdown.
type Supervisor interface {
	Recover(ctx context.Context) error
	StopAll(ctx context.Context) error
}

// Server owns the daemon socket. It serves handler on every accepted
// connection and, on shutdown, stops the agents and removes the socket.
type Server struct {
	cfg     Config
	handler protocol.Handler
	procs   Supervisor

	mu        sync.Mutex
	status    Status
	startedAt time.Time
	listener  net.Listener
	conns     map[net.Conn]struct{}
	cancel    context.CancelFunc

	wg       sync.WaitGroup
	done     chan struct{}
	doneOnce sync.Once
}

func NewServer(cfg Config, handler protocol.Handler, procs Supervisor) *Server {
	return &Server{
		cfg:     cfg,
		handler: handler,
		procs:   procs,
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
	if err := s.claimSocket(); err != nil {
		return err
	}
	if err := s.procs.Recover(ctx); err != nil {
		return fmt.Errorf("recover processes: %w", err)
	}

	ln, err := s.cfg.listener().Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// Connections outlive Start's ctx (often a request or signal context), so
	// they get their own, cancelled by Stop.
	serveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	s.mu.Lock()
	s.listener = ln
	s.cancel = cancel
	s.status = StatusRunning
	s.startedAt = time.Now()
	s.mu.Unlock()

	s.wg.Add(1)
	go s.accept(serveCtx, ln)
	return nil
}

// Stop shuts the daemon down, closes every open connection and waits for
// their handlers to return. It is safe to call more than once.
func (s *Server) Stop(ctx context.Context) error {
	err := s.shutdown(ctx)

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
	return Snapshot{Status: s.status, Socket: s.cfg.SocketPath, StartedAt: s.startedAt}
}

// shutdown stops accepting connections, removes the socket and stops every
// agent. Open connections are left alone so a runtime.shutdown caller still
// gets its reply; Stop closes them afterwards.
func (s *Server) shutdown(ctx context.Context) error {
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
	errs = append(errs, s.procs.StopAll(ctx))

	s.mu.Lock()
	s.status = StatusStopped
	s.mu.Unlock()
	return errors.Join(errs...)
}

func (s *Server) markDone() { s.doneOnce.Do(func() { close(s.done) }) }

func (s *Server) accept(ctx context.Context, ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
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
			_ = protocol.Serve(ctx, conn, s.handler, protocol.DefaultMaxMessageSize)
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

// claimSocket prepares the socket path. A socket that still answers belongs
// to a live daemon; one that does not is left over from a crash and removed.
func (s *Server) claimSocket() error {
	path := s.cfg.SocketPath
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if conn, err := net.DialTimeout("unix", path, probeTimeout); err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
