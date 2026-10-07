package client_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/runtime"
)

func TestClientProcessIntegration(t *testing.T) {
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

	// 1. Create environment
	env, err := c.EnvironmentCreate(ctx, "flyrank")
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	// 2. Run real process: echo hello
	echoProc, err := c.ProcessStart(ctx, process.StartRequest{EnvironmentID: env.ID, Command: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("ProcessStart echo: %v", err)
	}
	if echoProc.ID == "" {
		t.Fatalf("expected non-empty process ID")
	}

	// Wait for echo to exit
	var finishedProc process.Process
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p, err := c.ProcessGet(ctx, echoProc.ID)
		if err == nil && (p.Status == process.StatusExited || p.Status == process.StatusFailed) {
			finishedProc = p
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finishedProc.Status != process.StatusExited {
		t.Fatalf("expected echo to exit with status exited, got %s", finishedProc.Status)
	}
	if finishedProc.ExitCode == nil || *finishedProc.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %v", finishedProc.ExitCode)
	}

	// Check stdout.log content
	stdoutLog := filepath.Join(baseDir, "environments", "flyrank", "processes", echoProc.ID, "stdout.log")
	stdoutData, err := os.ReadFile(stdoutLog)
	if err != nil {
		t.Fatalf("read stdout.log: %v", err)
	}
	if string(stdoutData) != "hello\n" {
		t.Fatalf("expected stdout 'hello\\n', got %q", string(stdoutData))
	}

	// 3. List processes in environment
	procs, err := c.ProcessList(ctx, "flyrank")
	if err != nil {
		t.Fatalf("ProcessList: %v", err)
	}
	if len(procs) == 0 {
		t.Fatalf("expected at least 1 process in list")
	}

	// 4. Run real long-running process: sleep 30, then stop it
	sleepProc, err := c.ProcessStart(ctx, process.StartRequest{EnvironmentID: env.ID, Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("ProcessStart sleep: %v", err)
	}
	if sleepProc.Status != process.StatusRunning {
		t.Fatalf("expected sleep to be running, got %s", sleepProc.Status)
	}

	// Stop it
	if err := c.ProcessStop(ctx, sleepProc.ID); err != nil {
		t.Fatalf("ProcessStop: %v", err)
	}

	// Wait for it to transition to killed
	var killedProc process.Process
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p, err := c.ProcessGet(ctx, sleepProc.ID)
		if err == nil && p.Status == process.StatusKilled {
			killedProc = p
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if killedProc.Status != process.StatusKilled {
		t.Fatalf("expected sleep to be killed, got %s", killedProc.Status)
	}
}
