package terminal

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/pgroup"
)

const (
	// DefaultHistoryBytes bounds the output replayed to a newly attached client.
	DefaultHistoryBytes = 64 << 10
	// subscriberBuffer is how many chunks (up to 4 KiB each) a subscriber may
	// fall behind before it is cut off.
	subscriberBuffer = 256
	readChunk        = 4096
)

// PTYFactory opens real OS pseudo-terminals. Its zero value uses the
// defaults, so PTYFactory{} is ready to use.
type PTYFactory struct {
	// Size applies when a Command has no size. Zero means DefaultSize.
	Size Size
	// HistoryBytes is the replay buffer per session. Zero means the default.
	HistoryBytes int
	// StopGrace is the SIGTERM → SIGKILL delay of Close. Zero means pgroup.Grace.
	StopGrace time.Duration
	Logger    *slog.Logger
}

func NewPTYFactory() Factory { return PTYFactory{} }

// Open starts cmd in a new PTY. The process outlives ctx: sessions end when
// the process exits or Close is called, never when a request finishes.
func (f PTYFactory) Open(_ context.Context, cmd Command) (Session, error) {
	var logFile *os.File
	if cmd.LogPath != "" {
		// Agent output can contain secrets, so the log is private.
		lf, err := os.OpenFile(cmd.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		logFile = lf
	}

	size := cmd.Size
	if size.Width == 0 || size.Height == 0 {
		size = f.Size
	}
	if size.Width == 0 || size.Height == 0 {
		size = DefaultSize
	}
	c := exec.Command(cmd.Path, cmd.Args...) //nolint:noctx // agents outlive any request context
	c.Dir = cmd.WorkingDir
	// pty.Start makes the child a session (and so process-group) leader,
	// which is what lets Close stop the whole group.
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: size.Width, Rows: size.Height})
	if err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, err
	}

	histBytes := f.HistoryBytes
	if histBytes <= 0 {
		histBytes = DefaultHistoryBytes
	}
	grace := f.StopGrace
	if grace <= 0 {
		grace = pgroup.Grace
	}
	s := &ptySession{
		cmd:         c,
		ptmx:        ptmx,
		logFile:     logFile,
		grace:       grace,
		log:         logging.OrDiscard(f.Logger).With("pid", c.Process.Pid),
		history:     newHistory(histBytes),
		subscribers: make(map[*subscriber]struct{}),
	}
	go s.readLoop()
	return s, nil
}

type ptySession struct {
	cmd     *exec.Cmd
	ptmx    *os.File
	logFile *os.File // written only by readLoop, so it needs no lock
	grace   time.Duration
	log     *slog.Logger

	waitOnce sync.Once
	waitErr  error

	mu          sync.Mutex
	subscribers map[*subscriber]struct{}
	history     *history
	ended       bool // output stream finished; subscriber channels are closed
	closing     bool
}

type subscriber struct {
	ch     chan []byte
	lagged atomic.Bool
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

// Close asks the process group to stop (SIGTERM, then SIGKILL after the
// grace period). The PTY itself is released by readLoop once output ends.
func (s *ptySession) Close() error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	s.mu.Unlock()
	return pgroup.TerminateAfter(s.Pid(), s.grace)
}

func (s *ptySession) Subscribe() *Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	hist := s.history.Bytes()
	if s.ended {
		close(sub.ch)
		return NewSubscription(sub.ch, hist, nil, nil)
	}
	s.subscribers[sub] = struct{}{}
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.drop(sub)
	}
	return NewSubscription(sub.ch, hist, cancel, sub.lagged.Load)
}

// drop removes and closes sub if it is still registered. Callers hold mu.
func (s *ptySession) drop(sub *subscriber) {
	if _, ok := s.subscribers[sub]; ok {
		delete(s.subscribers, sub)
		close(sub.ch)
	}
}

// readLoop fans PTY output out to the log file, the history buffer and every
// subscriber. It is the single reader of ptmx and the owner of its lifetime.
func (s *ptySession) readLoop() {
	buf := make([]byte, readChunk)
	logOK := s.logFile != nil
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			// Disk I/O happens before taking the lock, so a slow disk can
			// delay this agent's output but never Subscribe or other calls.
			if logOK {
				if _, werr := s.logFile.Write(chunk); werr != nil {
					s.log.Warn("terminal log write failed; further output is not logged", "err", werr)
					logOK = false
				}
			}
			s.publish(chunk)
		}
		if err != nil {
			break // EIO once every holder of the terminal has exited
		}
	}

	s.mu.Lock()
	s.ended = true
	for sub := range s.subscribers {
		s.drop(sub)
	}
	s.mu.Unlock()

	_ = s.ptmx.Close()
	if s.logFile != nil {
		_ = s.logFile.Close()
	}
}

func (s *ptySession) publish(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history.Write(chunk)
	// Blocking here would freeze the agent once the PTY buffer fills, and
	// skipping a chunk would corrupt the subscriber's screen. A subscriber
	// that cannot keep up is cut off instead and told why (Lagged).
	for sub := range s.subscribers {
		select {
		case sub.ch <- chunk:
		default:
			sub.lagged.Store(true)
			s.drop(sub)
			s.log.Warn("terminal subscriber fell behind and was detached")
		}
	}
}
