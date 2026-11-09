package runtime

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"
)

// socketCheckInterval is how often a kept socket is checked on disk.
var socketCheckInterval = 2 * time.Second

// keepListening wraps ln, listening on the unix socket at path, so that the
// socket is put back when its file is removed under a running daemon (a tmp
// cleaner, a stray rm). Without it the daemon keeps its lock and agents but
// nobody can reach that socket until it restarts: on the control socket
// every command fails with "already running" and `hive stop` cannot stop it.
// Accept carries on across the swap. A path taken over by another file is
// left alone. A listener with no file on disk (tests) is returned as is.
func keepListening(ln net.Listener, path string, f ListenerFactory, log *slog.Logger) net.Listener {
	fi, err := os.Lstat(path)
	if err != nil {
		return ln
	}
	k := &keptListener{path: path, factory: f, log: log, cur: ln, served: fi, done: make(chan struct{})}
	go k.watch()
	return k
}

type keptListener struct {
	path    string
	factory ListenerFactory
	log     *slog.Logger

	mu     sync.Mutex
	cur    net.Listener
	served os.FileInfo
	closed bool
	done   chan struct{}
}

func (k *keptListener) Accept() (net.Conn, error) {
	for {
		k.mu.Lock()
		ln := k.cur
		k.mu.Unlock()
		conn, err := ln.Accept()
		if err == nil {
			return conn, nil
		}
		k.mu.Lock()
		swapped, closed := k.cur != ln, k.closed
		k.mu.Unlock()
		if swapped && !closed {
			continue // the old socket was replaced; serve the new one
		}
		return nil, err
	}
}

func (k *keptListener) Close() error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return nil
	}
	k.closed = true
	close(k.done)
	ln := k.cur
	k.mu.Unlock()
	return ln.Close()
}

func (k *keptListener) Addr() net.Addr {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cur.Addr()
}

func (k *keptListener) watch() {
	t := time.NewTicker(socketCheckInterval)
	defer t.Stop()
	warned := false
	for {
		select {
		case <-k.done:
			return
		case <-t.C:
		}
		fi, err := os.Lstat(k.path)
		k.mu.Lock()
		served := k.served
		k.mu.Unlock()
		switch {
		case err == nil && os.SameFile(fi, served):
			warned = false
		case err == nil:
			if !warned {
				k.log.Warn("socket path now holds another file; leaving it alone", "path", k.path)
				warned = true
			}
		case errors.Is(err, os.ErrNotExist):
			k.rebind()
		}
	}
}

// rebind listens at the path again and retires the old listener without
// removing the path, which is now the new socket's.
func (k *keptListener) rebind() {
	ln, err := k.factory.Listen("unix", k.path)
	if err != nil {
		k.log.Error("socket removed and cannot be recreated", "path", k.path, "err", err)
		return
	}
	fi, err := os.Lstat(k.path)
	if err == nil {
		err = os.Chmod(k.path, 0o600)
	}
	if err != nil {
		_ = ln.Close()
		k.log.Error("secure recreated socket", "path", k.path, "err", err)
		return
	}
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		_ = ln.Close()
		return
	}
	old := k.cur
	k.cur, k.served = ln, fi
	k.mu.Unlock()
	if ul, ok := old.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = old.Close()
	k.log.Warn("socket was removed; recreated it", "path", k.path)
}
