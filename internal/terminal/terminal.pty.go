package terminal

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/vt"
)

const (
	readChunk = 16 << 10
	// FrameInterval is the minimum time between two frames to one viewer
	// (at most ~120 per second). The first frame after a quiet period is
	// sent at once, so typing feels immediate.
	FrameInterval = 8 * time.Millisecond
)

// PTYFactory opens real OS pseudo-terminals in this process. Its zero value
// uses the defaults, so PTYFactory{} is ready to use.
type PTYFactory struct {
	// Size applies when a Command has no size. Zero means DefaultSize.
	Size Size
	// ScrollbackBytes bounds each session's scrollback. Zero means the
	// emulator's default; negative keeps none.
	ScrollbackBytes int
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

	size := cmd.Size.Or(f.Size, DefaultSize)
	c := exec.Command(cmd.Path, cmd.Args...) //nolint:noctx // agents outlive any request context
	c.Dir = cmd.WorkingDir
	c.Env = cmd.Env
	if c.Env == nil {
		c.Env = os.Environ()
	}
	// Programs inside a Hive terminal see a capable xterm-compatible one.
	c.Env = append(c.Env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=hive")
	// pty.Start makes the child a session (and so process-group) leader,
	// which is what lets Close stop the whole group.
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: size.Width, Rows: size.Height})
	if err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, err
	}

	grace := f.StopGrace
	if grace <= 0 {
		grace = pgroup.Grace
	}
	s := &ptySession{
		cmd:      c,
		ptmx:     ptmx,
		logFile:  logFile,
		grace:    grace,
		log:      logging.OrDiscard(f.Logger).With("pid", c.Process.Pid),
		size:     size,
		watchers: make(map[chan struct{}]struct{}),
	}
	// Answers to the program's terminal queries go back to its input.
	s.term = vt.New(int(size.Width), int(size.Height), vt.Options{
		Reply:           func(b []byte) { _, _ = ptmx.Write(b) },
		ScrollbackBytes: f.ScrollbackBytes,
	})
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

	mu       sync.Mutex
	term     *vt.Terminal
	size     Size
	watchers map[chan struct{}]struct{} // Frames loops waiting for changes
	ended    bool                       // output finished; the screen is final
	closing  bool
}

func (s *ptySession) Write(b []byte) (int, error) { return s.ptmx.Write(b) }

func (s *ptySession) Resize(size Size) error {
	if !size.Valid() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if size == s.size || s.ended {
		return nil
	}
	if err := pty.Setsize(s.ptmx, &pty.Winsize{Cols: size.Width, Rows: size.Height}); err != nil {
		return err
	}
	s.size = size
	s.term.Resize(int(size.Width), int(size.Height))
	s.notifyLocked()
	return nil
}

func (s *ptySession) Size() Size {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
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

func (s *ptySession) Snapshot(context.Context) (*vt.Screen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.Snapshot(), nil
}

func (s *ptySession) Scrollback(_ context.Context, n int, ansi bool) ([]string, error) {
	s.mu.Lock()
	lines := s.term.Scrollback().Tail(n)
	s.mu.Unlock()
	return FormatLines(lines, ansi), nil
}

// FormatLines renders lines of cells as plain text or ANSI-styled text.
func FormatLines(lines [][]vt.Cell, ansi bool) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if ansi {
			out[i] = vt.LineANSI(l)
		} else {
			out[i] = vt.LineText(l)
		}
	}
	return out
}

func (s *ptySession) Frames(ctx context.Context, emit func(*vt.Frame) error) error {
	changed := make(chan struct{}, 1)
	s.mu.Lock()
	s.watchers[changed] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.watchers, changed)
		s.mu.Unlock()
	}()

	var (
		view vt.View
		last time.Time
	)
	for {
		s.mu.Lock()
		f := s.term.Frame(&view)
		ended := s.ended
		s.mu.Unlock()
		if f != nil {
			if err := emit(f); err != nil {
				return err
			}
			last = time.Now()
		}
		if ended {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		}
		// Coalesce bursts: no more than one frame per FrameInterval.
		if wait := FrameInterval - time.Since(last); wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		}
	}
}

// notifyLocked wakes every Frames loop. Callers hold mu.
func (s *ptySession) notifyLocked() {
	for ch := range s.watchers {
		select {
		case ch <- struct{}{}:
		default: // already pending
		}
	}
}

// readLoop feeds PTY output to the log file and the emulator. It is the
// single reader of ptmx and the owner of its lifetime.
func (s *ptySession) readLoop() {
	buf := make([]byte, readChunk)
	logOK := s.logFile != nil
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			// Disk I/O happens before taking the lock, so a slow disk can
			// delay this agent's output but never viewers or other calls.
			if logOK {
				if _, werr := s.logFile.Write(buf[:n]); werr != nil {
					s.log.Warn("terminal log write failed; further output is not logged", "err", werr)
					logOK = false
				}
			}
			s.mu.Lock()
			_, _ = s.term.Write(buf[:n])
			s.notifyLocked()
			s.mu.Unlock()
		}
		if err != nil {
			break // EIO once every holder of the terminal has exited
		}
	}

	s.mu.Lock()
	s.ended = true
	s.notifyLocked()
	_ = s.term.Close()
	s.mu.Unlock()

	_ = s.ptmx.Close()
	if s.logFile != nil {
		_ = s.logFile.Close()
	}
}
