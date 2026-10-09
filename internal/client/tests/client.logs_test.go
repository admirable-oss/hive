package client_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/runtime"
)

// startDaemon runs a daemon on a short socket path for the test's duration.
func startDaemon(t *testing.T) client.Client {
	t.Helper()
	path := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: path})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := mod.Service.Stop(ctx); err != nil {
			t.Errorf("stop daemon: %v", err)
		}
	})
	return client.NewService(client.Config{SocketPath: path})
}

func waitExited(t *testing.T, c client.Client, id string) process.Process {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, err := c.ProcessGet(context.Background(), id)
		if err == nil && !p.Active() {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %s did not exit", id)
	return process.Process{}
}

func TestClient_LogsStreamBeyondTheFrameLimit(t *testing.T) {
	c := startDaemon(t)
	ctx := context.Background()
	if _, err := c.EnvironmentCreate(ctx, "logs"); err != nil {
		t.Fatal(err)
	}
	// 3 MiB of output is three times the protocol's 1 MiB frame limit.
	const size = 3 << 20
	p, err := c.ProcessStart(ctx, process.StartRequest{
		EnvironmentID: "logs", Command: "sh",
		Args: []string{"-c", "head -c 3145728 /dev/zero | tr '\\0' 'x'; echo; echo end >&2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitExited(t, c, p.ID)

	var out bytes.Buffer
	if err := c.ProcessLogsStream(ctx, process.LogsRequest{ID: p.ID}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != size+1 {
		t.Fatalf("streamed %d bytes, want %d", out.Len(), size+1)
	}

	// The single-reply form degrades to a truncated tail instead of failing.
	res, err := c.ProcessLogs(ctx, process.LogsRequest{ID: p.ID, Tail: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || len(res.Logs) > process.MaxLogsBytes {
		t.Fatalf("expected a truncated reply under %d bytes, got %d (truncated=%v)", process.MaxLogsBytes, len(res.Logs), res.Truncated)
	}

	errLog, err := c.ProcessLogs(ctx, process.LogsRequest{ID: p.ID, Stream: process.StreamStderr})
	if err != nil || errLog.Logs != "end" {
		t.Fatalf("stderr = %q, %v", errLog.Logs, err)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestClient_LogsFollowEndsWithTheProcess(t *testing.T) {
	c := startDaemon(t)
	ctx := context.Background()
	if _, err := c.EnvironmentCreate(ctx, "follow"); err != nil {
		t.Fatal(err)
	}
	p, err := c.ProcessStart(ctx, process.StartRequest{
		EnvironmentID: "follow", Command: "sh",
		Args: []string{"-c", "for i in 1 2 3; do echo tick $i; sleep 0.2; done"},
	})
	if err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- c.ProcessLogsStream(ctx, process.LogsRequest{ID: p.ID, Follow: true}, out) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("follow did not end when the process exited")
	}
	if got := out.String(); got != "tick 1\ntick 2\ntick 3\n" {
		t.Fatalf("followed output = %q", got)
	}
}

func TestClient_LogsFollowStopsOnCancel(t *testing.T) {
	c := startDaemon(t)
	ctx := context.Background()
	if _, err := c.EnvironmentCreate(ctx, "cancel"); err != nil {
		t.Fatal(err)
	}
	p, err := c.ProcessStart(ctx, process.StartRequest{EnvironmentID: "cancel", Command: "sh", Args: []string{"-c", "echo up; sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	followCtx, cancel := context.WithCancel(ctx)
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- c.ProcessLogsStream(followCtx, process.LogsRequest{ID: p.ID, Follow: true}, out) }()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "up") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled follow should return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the follow")
	}
}

func TestClient_LogsErrorsAreTyped(t *testing.T) {
	c := startDaemon(t)
	ctx := context.Background()
	if _, err := c.EnvironmentCreate(ctx, "typed"); err != nil {
		t.Fatal(err)
	}
	p, err := c.ProcessStart(ctx, process.StartRequest{EnvironmentID: "typed", Command: "true", Terminal: true})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = c.ProcessLogsStream(ctx, process.LogsRequest{ID: p.ID, Stream: process.StreamStderr}, &out)
	if pe, ok := errors.AsType[*protocol.Error](err); !ok || pe.Code != protocol.ErrorCodeInvalidParams {
		t.Fatalf("stderr of a terminal process: got %v, want invalid_params", err)
	}
	_, err = c.ProcessLogs(ctx, process.LogsRequest{ID: "ffffffffffffffff"})
	if pe, ok := errors.AsType[*protocol.Error](err); !ok || pe.Code != protocol.ErrorCodeNotFound {
		t.Fatalf("unknown process: got %v, want not_found", err)
	}
}

func TestClient_UnavailableDaemonIsTyped(t *testing.T) {
	c := client.NewService(client.Config{SocketPath: shortSock(t)})
	if err := c.Ping(context.Background()); !errors.Is(err, client.ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}

func TestClient_StatusIdentifiesTheDaemon(t *testing.T) {
	c := startDaemon(t)
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.PID == 0 || st.Version == "" || st.ProtocolVersion != protocol.Version2 {
		t.Fatalf("status lacks identity: %+v", st)
	}
}

// shortSock returns a socket path short enough for macOS's 104-byte limit,
// which t.TempDir (it embeds the test name) can exceed.
func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "hive.sock")
}
