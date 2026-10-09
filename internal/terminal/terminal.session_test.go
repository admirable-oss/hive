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
	"github.com/admirable-oss/hive/internal/vt"
)

// ─── fakes ───────────────────────────────────────────────────────────────

type fakeSession struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	pid     int
	closed  bool
	waitErr error
	waitCh  chan struct{}
	size    terminal.Size
	resizes int
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

func (s *fakeSession) Resize(size terminal.Size) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size = size
	s.resizes++
	return nil
}

func (s *fakeSession) Size() terminal.Size {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

func (s *fakeSession) Pid() int { return s.pid }

func (s *fakeSession) Snapshot(context.Context) (*vt.Screen, error) { return vt.NewScreen(1, 1), nil }

func (s *fakeSession) Scrollback(context.Context, int, bool) ([]string, error) { return nil, nil }

func (s *fakeSession) Frames(ctx context.Context, _ func(*vt.Frame) error) error {
	<-ctx.Done()
	return nil
}

func (s *fakeSession) Wait() error {
	<-s.waitCh
	return s.waitErr
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

func TestTerminalService_LastViewToInteractSetsTheSize(t *testing.T) {
	fake := newFakeSession(104)
	factory := &fakeFactory{openFn: func(context.Context, terminal.Command) (terminal.Session, error) { return fake, nil }}
	svc := newTestService(t, factory)
	if _, err := svc.Open(context.Background(), "p", terminal.Command{Path: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	small, big := terminal.Size{Width: 80, Height: 20}, terminal.Size{Width: 200, Height: 60}

	// A passive viewer (the dashboard) does not resize the agent's terminal.
	dash, err := svc.Join("p", small, false)
	if err != nil {
		t.Fatal(err)
	}
	if fake.Size() != (terminal.Size{}) {
		t.Fatalf("joining passively resized the terminal to %+v", fake.Size())
	}
	// An attach takes over at once.
	attach, err := svc.Join("p", big, true)
	if err != nil {
		t.Fatal(err)
	}
	if fake.Size() != big {
		t.Fatalf("attach: size %+v, want %+v", fake.Size(), big)
	}
	// Typing in the dashboard hands the size back to it.
	if err := svc.Interact("p", dash); err != nil {
		t.Fatal(err)
	}
	if fake.Size() != small {
		t.Fatalf("after dashboard input: size %+v, want %+v", fake.Size(), small)
	}
	// Repeated input from the view in control does not resize again.
	before := fake.resizes
	_ = svc.Interact("p", dash)
	if fake.resizes != before {
		t.Fatal("input from the view in control must not resize")
	}
	// A view in control follows its own window size; others only record it.
	if err := svc.ResizeView("p", dash, terminal.Size{Width: 90, Height: 25}); err != nil || fake.Size().Width != 90 {
		t.Fatalf("resize of the active view: %+v, %v", fake.Size(), err)
	}
	if err := svc.ResizeView("p", attach, terminal.Size{Width: 300, Height: 70}); err != nil || fake.Size().Width != 90 {
		t.Fatalf("resize of an inactive view must not apply: %+v, %v", fake.Size(), err)
	}
	svc.Leave("p", dash)
	if err := svc.Interact("p", dash); !errors.Is(err, terminal.ErrViewNotFound) {
		t.Fatalf("interact after leave: %v", err)
	}
	if _, err := svc.Join("missing", small, false); !errors.Is(err, terminal.ErrSessionNotFound) {
		t.Fatalf("join on a missing session: %v", err)
	}
}

func TestTerminalService_AdoptNeedsAnAdopter(t *testing.T) {
	svc := newTestService(t, &fakeFactory{})
	if _, err := svc.Adopt(context.Background(), "p"); !errors.Is(err, terminal.ErrNotAdoptable) {
		t.Fatalf("got %v, want ErrNotAdoptable", err)
	}
}
