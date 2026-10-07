package client_test

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/runtime"
)

func TestClientTerminalIntegration(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "hive.sock")

	mod := runtime.NewModule(runtime.Config{
		SocketPath: path,
		BaseDir:    baseDir,
		Listener:   runtime.NewNetListenerFactory(),
	})
	if err := mod.Service.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = mod.Service.Stop(stopCtx)
	}()

	if err := waitForSocket(path, 2*time.Second); err != nil {
		t.Fatalf("socket did not appear: %v", err)
	}

	c := client.NewService(client.Config{SocketPath: path})

	// 1. Create environment "dev"
	env, err := c.EnvironmentCreate(ctx, "dev")
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	// 2. Start /bin/sh inside a PTY
	proc, err := c.ProcessStart(ctx, process.StartRequest{
		EnvironmentID: env.ID,
		Command:       "/bin/sh",
		Terminal:      true,
	})
	if err != nil {
		t.Fatalf("ProcessStart: %v", err)
	}
	if proc.ID == "" {
		t.Fatalf("expected non-empty process ID")
	}
	if !proc.Terminal {
		t.Errorf("expected Terminal to be true")
	}
	if proc.Status != process.StatusRunning {
		t.Errorf("expected status running, got %s", proc.Status)
	}

	// 3. Connect and interact via TerminalAttach
	inReader, inWriter := io.Pipe()
	syncWriter := &syncBuffer{}

	attachDone := make(chan error, 1)
	go func() {
		attachDone <- c.TerminalAttach(ctx, proc.ID, inReader, syncWriter)
	}()

	// Helper to send command and wait for output
	waitForOutput := func(send string, expected string) {
		_, err := inWriter.Write([]byte(send))
		if err != nil {
			t.Fatalf("write %q: %v", send, err)
		}

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(syncWriter.String(), expected) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for output %q, current output:\n%s", expected, syncWriter.String())
	}

	// Test: echo hello -> expect "hello"
	waitForOutput("echo hello\n", "hello")

	// Test: pwd -> expect environment path
	waitForOutput("pwd\n", env.Path)

	// Test: export TEST=hive && echo $TEST -> expect "hive"
	waitForOutput("export TEST=hive\necho $TEST\n", "hive")

	// Test: exit -> shell terminates
	_, _ = inWriter.Write([]byte("exit\n"))
	_ = inWriter.Close()

	select {
	case err := <-attachDone:
		if err != nil {
			t.Fatalf("TerminalAttach finished with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for attach to finish after exit")
	}

	// 4. Verify process transitioned to EXITED with exit code 0
	deadline := time.Now().Add(3 * time.Second)
	var finalProc process.Process
	for time.Now().Before(deadline) {
		p, err := c.ProcessGet(ctx, proc.ID)
		if err == nil && p.Status == process.StatusExited {
			finalProc = p
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalProc.Status != process.StatusExited {
		t.Fatalf("expected status %s, got %s", process.StatusExited, finalProc.Status)
	}
	if finalProc.ExitCode == nil || *finalProc.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %v", finalProc.ExitCode)
	}
}

type syncBuffer struct {
	mu  sync.RWMutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buf.String()
}
