package process_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStore_TailEdgeCases(t *testing.T) {
	root := t.TempDir()
	newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	p := process.Process{ID: "0123456789abcdef", EnvironmentID: "env"}
	if err := store.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	stdout, _ := store.LogPaths(p)

	tests := []struct {
		name, log string
		n         int
		want      string
	}{
		{"missing file", "", 3, ""},
		{"no trailing newline", "a\nb\nc", 2, "b\nc"},
		{"trailing newline", "a\nb\nc\n", 2, "b\nc"},
		{"blank lines count", "a\n\n\nb\n", 3, "\n\nb"},
		{"more than exists", "a\nb\n", 10, "a\nb"},
		{"whole log", "a\nb\n", 0, "a\nb"},
		{"single line", "only\n", 1, "only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Remove(stdout)
			if tt.log != "" {
				writeFile(t, stdout, tt.log)
			}
			got, err := store.Tail(context.Background(), p, process.StreamStdout, tt.n)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// logFixture starts a fake process (running until stop is called) whose log
// files the test writes directly.
type logFixture struct {
	svc            process.Service
	proc           process.Process
	stdout, stderr string
	stop           func()
}

func newLogFixture(t *testing.T, terminal bool) logFixture {
	t.Helper()
	root := t.TempDir()
	envs := newEnv(t, root, "env")
	store := process.NewFilesystemStore(root)
	exit := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(exit) }) }
	t.Cleanup(stop)
	runner := &fakeRunner{start: func(context.Context, process.Command) (process.Handle, error) {
		return &fakeHandle{pid: 9, wait: func() error { <-exit; return nil }, kill: func() error { stop(); return nil }}, nil
	}}
	var terms process.Terminals
	if terminal {
		terms = fakeTerminals{exit: exit, stop: stop}
	}
	svc := process.NewService(store, envs, runner, terms)
	p, err := svc.Start(context.Background(), process.StartRequest{EnvironmentID: "env", Command: "agent", Terminal: terminal})
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := store.LogPaths(p)
	t.Cleanup(func() { _ = svc.StopAll(context.Background()) })
	return logFixture{svc: svc, proc: p, stdout: stdout, stderr: stderr, stop: stop}
}

func TestService_LogsSelectsStream(t *testing.T) {
	f := newLogFixture(t, false)
	writeFile(t, f.stdout, "out1\nout2\n")
	writeFile(t, f.stderr, "err1\n")
	ctx := context.Background()

	if got, err := f.svc.Logs(ctx, process.LogsRequest{ID: f.proc.ID}); err != nil || got != "out1\nout2" {
		t.Fatalf("default stream: %q, %v", got, err)
	}
	if got, err := f.svc.Logs(ctx, process.LogsRequest{ID: f.proc.ID, Stream: process.StreamStderr}); err != nil || got != "err1" {
		t.Fatalf("stderr: %q, %v", got, err)
	}
	if _, err := f.svc.Logs(ctx, process.LogsRequest{ID: f.proc.ID, Stream: "stdin"}); !errors.Is(err, process.ErrInvalidStream) {
		t.Fatalf("unknown stream: got %v", err)
	}
	if _, err := f.svc.Logs(ctx, process.LogsRequest{ID: "ffffffffffffffff"}); !errors.Is(err, process.ErrNotFound) {
		t.Fatalf("unknown process: got %v", err)
	}
}

func TestService_TerminalProcessesHaveNoStderr(t *testing.T) {
	f := newLogFixture(t, true)
	if _, err := f.svc.Logs(context.Background(), process.LogsRequest{ID: f.proc.ID, Stream: process.StreamStderr}); !errors.Is(err, process.ErrNoStderr) {
		t.Fatalf("got %v, want ErrNoStderr", err)
	}
}

func TestLogStream_TailWithoutFollow(t *testing.T) {
	f := newLogFixture(t, false)
	writeFile(t, f.stdout, "1\n2\n3\n4\n")
	ls, err := f.svc.OpenLogs(context.Background(), process.LogsRequest{ID: f.proc.ID, Tail: 2})
	if err != nil {
		t.Fatal(err)
	}
	if ls.Following() {
		t.Fatal("a stream without Follow must not follow")
	}
	var out bytes.Buffer
	if err := ls.WriteTo(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "3\n4\n" {
		t.Fatalf("got %q, want the last two lines with their newlines", out.String())
	}
}

// syncBuffer is a bytes.Buffer safe for one writer and one reader.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestLogStream_FollowUntilProcessExits(t *testing.T) {
	f := newLogFixture(t, false)
	ls, err := f.svc.OpenLogs(context.Background(), process.LogsRequest{ID: f.proc.ID, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ls.Following() {
		t.Fatal("a live process should be followed")
	}

	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- ls.WriteTo(context.Background(), out) }()

	// The log does not exist yet; the stream must wait for it.
	time.Sleep(50 * time.Millisecond)
	logf, err := os.OpenFile(f.stdout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	for i := range 3 {
		_, _ = logf.WriteString(strings.Repeat("x", i+1) + "\n")
		time.Sleep(30 * time.Millisecond)
	}
	waitFor(t, func() bool { return strings.Count(out.String(), "\n") == 3 })

	_, _ = logf.WriteString("last words\n")
	f.stop() // the process exits; the stream drains and returns
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("following stream did not end with the process")
	}
	if want := "x\nxx\nxxx\nlast words\n"; out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestLogStream_FollowStopsWithContext(t *testing.T) {
	f := newLogFixture(t, false)
	writeFile(t, f.stdout, "hello\n")
	ls, err := f.svc.OpenLogs(context.Background(), process.LogsRequest{ID: f.proc.ID, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	out := &syncBuffer{}
	go func() { done <- ls.WriteTo(ctx, out) }()
	waitFor(t, func() bool { return out.String() == "hello\n" })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the context must end a follow")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeTerminals opens sessions that run until exit is closed; closing a
// session (stopping it) calls stop.
type fakeTerminals struct {
	exit chan struct{}
	stop func()
}

func (f fakeTerminals) Open(context.Context, string, terminal.Command) (terminal.Session, error) {
	return fakeSession(f), nil
}

type fakeSession fakeTerminals

func (fakeSession) Write(b []byte) (int, error) { return len(b), nil }
func (fakeSession) Resize(terminal.Size) error  { return nil }
func (s fakeSession) Wait() error               { <-s.exit; return nil }
func (fakeSession) Pid() int                    { return 10 }
func (s fakeSession) Close() error              { s.stop(); return nil }
func (fakeSession) Subscribe() *terminal.Subscription {
	return terminal.NewSubscription(nil, nil, nil, nil)
}
