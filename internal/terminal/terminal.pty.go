package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"

	"github.com/admirable-oss/hive/internal/pgroup"
)

// historySize bounds the scrollback replayed to a newly attached client.
const historySize = 64 << 10

// PTYFactory opens real OS pseudo-terminals.
type PTYFactory struct{}

func NewPTYFactory() Factory { return PTYFactory{} }

// Open starts cmd in a new PTY. The process outlives ctx: sessions end when
// the process exits or Close is called, never when a request finishes.
func (PTYFactory) Open(_ context.Context, cmd Command) (Session, error) {
	var log *os.File
	if cmd.LogPath != "" {
		f, err := os.OpenFile(cmd.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		log = f
	}

	size := cmd.Size
	if size.Width == 0 || size.Height == 0 {
		size = DefaultSize
	}
	c := exec.Command(cmd.Path, cmd.Args...)
	c.Dir = cmd.WorkingDir
	// pty.Start makes the child a session (and so process-group) leader,
	// which is what lets Close stop the whole group.
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: size.Width, Rows: size.Height})
	if err != nil {
		if log != nil {
			_ = log.Close()
		}
		return nil, err
	}

	s := &ptySession{cmd: c, ptmx: ptmx, log: log, subscribers: make(map[chan []byte]struct{})}
	go s.readLoop()
	return s, nil
}

type ptySession struct {
	cmd  *exec.Cmd
	ptmx *os.File
	log  *os.File

	waitOnce sync.Once
	waitErr  error

	mu          sync.Mutex
	subscribers map[chan []byte]struct{}
	history     []byte
	ended       bool // output stream finished; subscriber channels are closed
	closing     bool
}

func (s *ptySession) Write(b []byte) (int, error) { return s.ptmx.Write(b) }

func (s *ptySession) Resize(size Size) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: size.Width, Rows: size.Height})
}

// Wait is safe to call from several goroutines (the terminal service and the
// process monitor both wait on the same session).
func (s *ptySession) Wait() error {
	s.waitOnce.Do(func() { s.waitErr = s.cmd.Wait() })
	return s.waitErr
}

func (s *ptySession) Pid() int {
	if s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Close asks the process group to stop (SIGTERM, then SIGKILL after a grace
// period). The PTY itself is released by readLoop once output ends.
func (s *ptySession) Close() error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	s.mu.Unlock()
	return pgroup.Terminate(s.Pid())
}

func (s *ptySession) Subscribe() (<-chan []byte, []byte, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan []byte, 128)
	hist := append([]byte(nil), s.history...)
	if s.ended {
		close(ch)
		return ch, hist, func() {}
	}
	s.subscribers[ch] = struct{}{}
	return ch, hist, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subscribers[ch]; ok { // readLoop may have closed it already
			delete(s.subscribers, ch)
			close(ch)
		}
	}
}

// readLoop fans PTY output out to the log file, the history buffer and every
// subscriber. It is the single reader of ptmx and the owner of its lifetime.
func (s *ptySession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.publish(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			break // EIO once every holder of the terminal has exited
		}
	}

	s.mu.Lock()
	s.ended = true
	for ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = nil
	s.mu.Unlock()

	_ = s.ptmx.Close()
	if s.log != nil {
		_ = s.log.Close()
	}
}

func (s *ptySession) publish(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log != nil {
		_, _ = s.log.Write(chunk)
	}
	s.history = append(s.history, chunk...)
	if over := len(s.history) - historySize; over > 0 {
		s.history = s.history[over:]
	}
	// A subscriber that can't keep up loses chunks instead of stalling the
	// agent: blocking here would freeze the process once the PTY fills.
	for ch := range s.subscribers {
		select {
		case ch <- chunk:
		default:
		}
	}
}
