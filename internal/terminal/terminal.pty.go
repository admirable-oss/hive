package terminal

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/vt"
)

// ptyDevice is the OS pseudo-terminal under a session and the process in
// it: a Unix PTY, or a Windows pseudo console (ConPTY).
type ptyDevice interface {
	// Read returns the program's output; io.EOF (or an error) once every
	// holder of the terminal has gone.
	io.Reader
	// Write types into the program's input.
	io.Writer
	Resize(Size) error
	Pid() int
	// Wait waits for the process; an error with ExitCode() reports a
	// failure.
	Wait() error
	// Foreground returns the PID of the program in the foreground: the
	// terminal's foreground group on Unix; on Windows the newest live
	// descendant of the process.
	Foreground() (int, error)
	// Close releases the terminal once output has ended.
	Close() error
}

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
	var logFile io.WriteCloser
	if cmd.LogPath != "" {
		// Agent output can contain secrets, so the log is private.
		var err error
		if cmd.LogMaxBytes > 0 {
			logFile, err = logging.OpenRotating(cmd.LogPath, cmd.LogMaxBytes, 1)
		} else {
			logFile, err = platform.OpenLog(cmd.LogPath)
		}
		if err != nil {
			return nil, err
		}
		if !cmd.LogGraphics {
			logFile = vt.NewGraphicsStripper(logFile)
		}
	}

	size := cmd.Size.Or(f.Size, DefaultSize)
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	// Programs inside a Hive terminal see a capable xterm-compatible one.
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=hive")
	// The child leads its own group (a session on Unix, a job on
	// Windows), which is what lets Close stop the whole tree.
	ptmx, err := openPTY(cmd, size, env)
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
		ptmx:     ptmx,
		logFile:  logFile,
		grace:    grace,
		log:      logging.OrDiscard(f.Logger).With("pid", ptmx.Pid()),
		size:     size,
		watchers: make(map[chan struct{}]struct{}),
		waiters:  make(map[*waiter]struct{}),
		graphics: newGraphicsJournal(),
	}
	// Answers to the program's terminal queries go back to its input.
	s.term = vt.New(int(size.Width), int(size.Height), vt.Options{
		Reply: func(b []byte) { _, _ = ptmx.Write(b) },
		KittyGraphics: func(command vt.KittyGraphicsCommand) {
			if command.ValidateKittyGraphicsRelay() == nil {
				s.graphics.publish(vt.GraphicsEvent{Command: command.Raw, Cursor: command.Cursor})
			}
		},
		ScrollbackBytes: f.ScrollbackBytes,
	})
	if len(cmd.Replay) > 0 {
		_, _ = s.term.Write(cmd.Replay)
	}
	go s.readLoop()
	return s, nil
}

type ptySession struct {
	ptmx    ptyDevice
	logFile io.WriteCloser // written only by readLoop, so it needs no lock
	grace   time.Duration
	log     *slog.Logger

	waitOnce sync.Once
	waitErr  error

	mu       sync.Mutex
	term     *vt.Terminal
	size     Size
	watchers map[chan struct{}]struct{} // Frames loops waiting for changes
	ended    bool                       // output finished; the screen is final

	waiters     map[*waiter]struct{} // WaitOutput calls
	graphics    *graphicsJournal
	waitVersion uint64 // output already checked for waiters
	waitPushed  uint64
	closing     bool
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
	if err := s.ptmx.Resize(size); err != nil {
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
	s.waitOnce.Do(func() { s.waitErr = s.ptmx.Wait() })
	return s.waitErr
}

func (s *ptySession) Pid() int { return s.ptmx.Pid() }

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

var _ ForegroundReader = (*ptySession)(nil)

// Foreground asks the terminal which program is in its foreground.
func (s *ptySession) Foreground(context.Context) (Foreground, error) {
	s.mu.Lock()
	ended := s.ended
	s.mu.Unlock()
	if ended {
		return Foreground{}, ErrEnded
	}
	pgid, err := s.ptmx.Foreground()
	if err != nil {
		return Foreground{}, err
	}
	args, err := platform.ProcessArgs(pgid)
	if err != nil {
		return Foreground{}, err
	}
	return Foreground{PID: pgid, Args: args}, nil
}

func (s *ptySession) Snapshot(context.Context) (*vt.Screen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.Snapshot(), nil
}

func (s *ptySession) Graphics(ctx context.Context, emit func(vt.GraphicsEvent) error) error {
	return s.graphics.stream(ctx, emit)
}

func (s *ptySession) Read(_ context.Context, req ReadRequest) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readTerminal(s.term, req)
}

func (s *ptySession) WaitOutput(ctx context.Context, req WaitRequest) (string, error) {
	re, err := compilePattern(req.Pattern)
	if err != nil {
		return "", err
	}
	w := &waiter{re: re, found: make(chan string, 1)}
	s.mu.Lock()
	if req.Anywhere {
		lines, _ := readTerminal(s.term, ReadRequest{Source: SourceRecent, Lines: MaxReadLines})
		if line, ok := matchLines(re, lines); ok {
			s.mu.Unlock()
			return line, nil
		}
	}
	if s.ended {
		s.mu.Unlock()
		return "", ErrEnded
	}
	if len(s.waiters) == 0 {
		// Only output from now on counts.
		s.waitVersion, s.waitPushed = s.term.Version(), s.term.Scrollback().Pushed()
	}
	s.waiters[w] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiters, w)
		s.mu.Unlock()
	}()

	select {
	case line, ok := <-w.found:
		if !ok {
			return "", ErrEnded
		}
		return line, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// checkWaitersLocked matches the output written since the last check (lines
// that scrolled off, then lines that changed on screen) against every
// waiter. Callers hold mu.
func (s *ptySession) checkWaitersLocked() {
	if len(s.waiters) == 0 {
		return
	}
	var lines []string
	scrolled, pushed := s.term.Scrollback().Since(s.waitPushed)
	for _, l := range scrolled {
		lines = append(lines, vt.LineText(l))
	}
	lines = append(lines, s.term.ChangedLines(s.waitVersion)...)
	s.waitVersion, s.waitPushed = s.term.Version(), pushed
	for w := range s.waiters {
		if line, ok := matchLines(w.re, lines); ok {
			w.found <- line
			delete(s.waiters, w)
		}
	}
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
			s.checkWaitersLocked()
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
	for w := range s.waiters {
		close(w.found) // no match is coming
		delete(s.waiters, w)
	}
	_ = s.term.Close()
	s.graphics.close()
	s.mu.Unlock()

	_ = s.ptmx.Close()
	if s.logFile != nil {
		_ = s.logFile.Close()
	}
}
