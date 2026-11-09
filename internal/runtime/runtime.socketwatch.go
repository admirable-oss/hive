package runtime

import (
	"context"
	"errors"
	"net"
	"os"
	"time"
)

// socketCheckInterval is how often the daemon checks that its control socket
// is still on disk.
var socketCheckInterval = 2 * time.Second

// watchSocket puts the control socket back when its file is removed under a
// running daemon (a tmp cleaner, a stray rm). Without it the daemon keeps
// its lock and its agents but no client can reach it: every command fails
// with "already running" and `hive stop` cannot stop it. A path taken over
// by another file is left alone. It only runs for a socket that exists on
// disk once listening, so in-memory test listeners are unaffected.
func (s *Server) watchSocket(ctx context.Context, served os.FileInfo) {
	defer s.wg.Done()
	t := time.NewTicker(socketCheckInterval)
	defer t.Stop()
	warned := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Lstat(s.cfg.SocketPath)
		switch {
		case err == nil && os.SameFile(fi, served):
			warned = false
			continue
		case err == nil:
			if !warned {
				s.log.Warn("the control socket's path now holds another file; leaving it alone", "path", s.cfg.SocketPath)
				warned = true
			}
			continue
		case !errors.Is(err, os.ErrNotExist):
			continue // cannot tell; try again later
		}
		if fi, ok := s.relisten(ctx); ok {
			served = fi
		}
	}
}

// relisten binds a new listener at the socket path, serves it and closes
// the old one without removing the path (which is now the new socket's).
func (s *Server) relisten(ctx context.Context) (os.FileInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != StatusRunning || s.listener == nil || ctx.Err() != nil {
		return nil, false
	}
	ln, err := s.cfg.listener().Listen("unix", s.cfg.SocketPath)
	if err != nil {
		s.log.Error("control socket removed and cannot be recreated", "path", s.cfg.SocketPath, "err", err)
		return nil, false
	}
	if err := os.Chmod(s.cfg.SocketPath, 0o600); err != nil {
		_ = ln.Close()
		s.log.Error("secure recreated control socket", "err", err)
		return nil, false
	}
	fi, err := os.Lstat(s.cfg.SocketPath)
	if err != nil {
		_ = ln.Close()
		return nil, false
	}
	old := s.listener
	if ul, ok := old.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = old.Close()
	s.listener = ln
	s.wg.Add(1)
	go s.accept(ctx, ln)
	s.log.Warn("control socket was removed; recreated it", "path", s.cfg.SocketPath)
	return fi, true
}
