package terminal_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
)

// ─── fakes ───────────────────────────────────────────────────────────────

type fakeSession struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	pid     int
	closed  bool
	waitErr error
	waitCh  chan struct{}
}

func newFakeSession(pid int) *fakeSession {
	return &fakeSession{pid: pid, waitCh: make(chan struct{})}
}

func (s *fakeSession) Read(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buf.Len() == 0 {
		return 0, io.EOF
	}
	return s.buf.Read(b)
}

func (s *fakeSession) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(b)
}

func (s *fakeSession) Resize(_ terminal.Size) error { return nil }
func (s *fakeSession) Pid() int                     { return s.pid }

func (s *fakeSession) Wait() error {
	<-s.waitCh
	return s.waitErr
}

func (s *fakeSession) Subscribe() *terminal.Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return terminal.NewSubscription(make(chan []byte), s.buf.Bytes(), nil, nil)
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	select {
	case <-s.waitCh:
	default:
		close(s.waitCh)
	}
	return nil
}

func (s *fakeSession) exitWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.waitCh:
	default:
		s.waitErr = err
		close(s.waitCh)
	}
}

type fakeFactory struct {
	mu       sync.Mutex
	openFn   func(ctx context.Context, cmd terminal.Command) (terminal.Session, error)
	opened   []terminal.Command
	sessions []*fakeSession
}

func (f *fakeFactory) Open(ctx context.Context, cmd terminal.Command) (terminal.Session, error) {
	f.mu.Lock()
	f.opened = append(f.opened, cmd)
	f.mu.Unlock()
	var (
		sess terminal.Session
		err  error
	)
	if f.openFn != nil {
		sess, err = f.openFn(ctx, cmd)
	} else {
		sess = newFakeSession(1234)
	}
	if fs, ok := sess.(*fakeSession); ok {
		f.mu.Lock()
		f.sessions = append(f.sessions, fs)
		f.mu.Unlock()
	}
	return sess, err
}

// newTestService returns a service over factory whose sessions all exit
// when the test ends, so the service's watcher goroutines finish too.
func newTestService(t *testing.T, factory *fakeFactory) terminal.Service {
	t.Helper()
	t.Cleanup(func() {
		factory.mu.Lock()
		defer factory.mu.Unlock()
		for _, s := range factory.sessions {
			s.exitWith(nil)
		}
	})
	return terminal.NewService(factory)
}

// ─── tests ────────────────────────────────────────────────────────────────

func TestTerminalService_Open(t *testing.T) {
	factory := &fakeFactory{}
	svc := newTestService(t, factory)

	sess, err := svc.Open(context.Background(), "proc-1", terminal.Command{
		Path: "/bin/sh",
		Size: terminal.DefaultSize,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
}

func TestTerminalService_DuplicateOpen(t *testing.T) {
	factory := &fakeFactory{}
	svc := newTestService(t, factory)

	_, err := svc.Open(context.Background(), "proc-1", terminal.Command{Path: "/bin/sh"})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_, err = svc.Open(context.Background(), "proc-1", terminal.Command{Path: "/bin/sh"})
	if !errors.Is(err, terminal.ErrSessionExists) {
		t.Errorf("expected ErrSessionExists, got %v", err)
	}
}

func TestTerminalService_Get(t *testing.T) {
	factory := &fakeFactory{}
	svc := newTestService(t, factory)

	_, err := svc.Get("missing")
	if !errors.Is(err, terminal.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}

	_, _ = svc.Open(context.Background(), "proc-2", terminal.Command{Path: "/bin/sh"})
	sess, err := svc.Get("proc-2")
	if err != nil {
		t.Fatalf("Get after Open: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session")
	}
}

func TestTerminalService_WriteRead(t *testing.T) {
	fake := newFakeSession(100)
	factory := &fakeFactory{
		openFn: func(_ context.Context, _ terminal.Command) (terminal.Session, error) {
			return fake, nil
		},
	}
	svc := newTestService(t, factory)

	_, _ = svc.Open(context.Background(), "proc-3", terminal.Command{Path: "/bin/sh"})
	sess, _ := svc.Get("proc-3")

	if _, err := sess.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 5)
	n, _ := fake.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Errorf("expected 'hello', got %q", buf[:n])
	}
}

func TestTerminalService_Resize(t *testing.T) {
	fake := newFakeSession(101)
	factory := &fakeFactory{
		openFn: func(_ context.Context, _ terminal.Command) (terminal.Session, error) {
			return fake, nil
		},
	}
	svc := newTestService(t, factory)

	_, _ = svc.Open(context.Background(), "proc-4", terminal.Command{Path: "/bin/sh"})
	sess, _ := svc.Get("proc-4")

	if err := sess.Resize(terminal.Size{Width: 160, Height: 50}); err != nil {
		t.Fatalf("Resize: %v", err)
	}
}

func TestTerminalService_CloseRemovesSession(t *testing.T) {
	fake := newFakeSession(102)
	factory := &fakeFactory{
		openFn: func(_ context.Context, _ terminal.Command) (terminal.Session, error) {
			return fake, nil
		},
	}
	svc := newTestService(t, factory)

	_, _ = svc.Open(context.Background(), "proc-5", terminal.Command{Path: "/bin/sh"})
	if err := svc.Close("proc-5"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fake.closed {
		t.Error("expected underlying session to be closed")
	}
	_, err := svc.Get("proc-5")
	if !errors.Is(err, terminal.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound after close, got %v", err)
	}
}

func TestTerminalService_AutoCleanOnExit(t *testing.T) {
	fake := newFakeSession(103)
	factory := &fakeFactory{
		openFn: func(_ context.Context, _ terminal.Command) (terminal.Session, error) {
			return fake, nil
		},
	}
	svc := newTestService(t, factory)

	_, _ = svc.Open(context.Background(), "proc-6", terminal.Command{Path: "/bin/sh"})

	// Simulate process exit
	fake.exitWith(nil)

	// Give the goroutine a moment to clean up (service.Open launched a goroutine
	// that calls session.Wait then deletes from the map).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := svc.Get("proc-6")
		if errors.Is(err, terminal.ErrSessionNotFound) {
			return // pass
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("session was not forgotten after its process exited")
}

func TestTerminalService_FailedOpen(t *testing.T) {
	factory := &fakeFactory{
		openFn: func(_ context.Context, _ terminal.Command) (terminal.Session, error) {
			return nil, errors.New("no pty available")
		},
	}
	svc := newTestService(t, factory)

	_, err := svc.Open(context.Background(), "proc-7", terminal.Command{Path: "/bin/sh"})
	if err == nil {
		t.Fatal("expected error from failed open")
	}
}
