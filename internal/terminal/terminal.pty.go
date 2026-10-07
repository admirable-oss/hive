package terminal

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

type ptySession struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	logFile  *os.File
	waitOnce sync.Once
	waitErr  error

	mu          sync.Mutex
	subscribers map[chan []byte]struct{}
	history     []byte
	closed      bool
}

func (s *ptySession) Read(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.history) == 0 {
		return 0, io.EOF
	}
	n := copy(b, s.history)
	return n, nil
}

func (s *ptySession) Write(b []byte) (int, error) {
	return s.ptmx.Write(b)
}

func (s *ptySession) Resize(size Size) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{
		Cols: size.Width,
		Rows: size.Height,
	})
}

func (s *ptySession) Wait() error {
	s.waitOnce.Do(func() {
		s.waitErr = s.cmd.Wait()
	})
	return s.waitErr
}

func (s *ptySession) Pid() int {
	if s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

func (s *ptySession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.ptmx.Close()
}

func (s *ptySession) Subscribe() (<-chan []byte, []byte, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan []byte, 128)
	s.subscribers[ch] = struct{}{}
	hist := make([]byte, len(s.history))
	copy(hist, s.history)

	detach := func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
	}

	return ch, hist, detach
}

// PTYFactory opens real OS PTYs.
type PTYFactory struct{}

func NewPTYFactory() Factory {
	return &PTYFactory{}
}

func (f *PTYFactory) Open(ctx context.Context, cmd Command) (Session, error) {
	c := exec.CommandContext(ctx, cmd.Path, cmd.Args...)
	c.Dir = cmd.WorkingDir
	if len(cmd.Env) > 0 {
		c.Env = cmd.Env
	}

	size := cmd.Size
	if size.Width == 0 || size.Height == 0 {
		size = DefaultSize
	}

	ptmx, err := pty.StartWithSize(c, &pty.Winsize{
		Cols: size.Width,
		Rows: size.Height,
	})
	if err != nil {
		return nil, err
	}

	var logFile *os.File
	if cmd.StdoutPath != "" {
		logFile, _ = os.OpenFile(cmd.StdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	}

	sess := &ptySession{
		cmd:         c,
		ptmx:        ptmx,
		logFile:     logFile,
		subscribers: make(map[chan []byte]struct{}),
	}

	go sess.readLoop()

	return sess, nil
}

func (s *ptySession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			s.mu.Lock()
			if s.logFile != nil {
				_, _ = s.logFile.Write(chunk)
			}
			s.history = append(s.history, chunk...)
			if len(s.history) > 65536 {
				s.history = s.history[len(s.history)-65536:]
			}
			for ch := range s.subscribers {
				select {
				case ch <- chunk:
				default:
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			s.mu.Lock()
			for ch := range s.subscribers {
				close(ch)
			}
			s.subscribers = make(map[chan []byte]struct{})
			if s.logFile != nil {
				_ = s.logFile.Close()
			}
			s.mu.Unlock()
			break
		}
	}
}
