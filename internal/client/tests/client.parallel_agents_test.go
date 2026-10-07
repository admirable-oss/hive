package client_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/runtime"
)

func TestClientParallelAgentsIntegration(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	path := filepath.Join("/tmp", fmt.Sprintf("h-p-%d.sock", time.Now().UnixNano()%100000))
	defer os.Remove(path)

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

	// 1. Create environment "acme-api"
	env, err := c.EnvironmentCreate(ctx, "acme-api")
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	// 2. Start Claude agent in parallel
	claudeScript := `echo "› read  src/auth · 214 files"; sleep 0.1; echo "› plan  rotate session tokens on refresh"; sleep 0.1; echo "› edit  src/auth/middleware.ts  +9 -3"; sleep 2`
	claudeProc, err := c.ProcessStartRequest(ctx, process.StartRequest{
		EnvironmentID: env.ID,
		Command:       "sh",
		Args:          []string{"-c", claudeScript},
		Terminal:      true,
	})
	if err != nil {
		t.Fatalf("start claude: %v", err)
	}

	// 3. Start Codex agent in parallel in the same environment
	codexScript := `echo "› scan  test/auth · 42 suites"; sleep 0.1; echo "› detect flaky test: session_race"; sleep 0.1; echo "› pass  all 42 test suites passed"; sleep 2`
	codexProc, err := c.ProcessStartRequest(ctx, process.StartRequest{
		EnvironmentID: env.ID,
		Command:       "sh",
		Args:          []string{"-c", codexScript},
		Terminal:      true,
	})
	if err != nil {
		t.Fatalf("start codex: %v", err)
	}

	// 4. Verify both are running concurrently in the environment
	procs, err := c.ProcessList(ctx, env.ID)
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	if len(procs) != 2 {
		t.Fatalf("expected 2 processes, got %d", len(procs))
	}

	// 5. Verify live logs streamed from Claude
	deadline := time.Now().Add(3 * time.Second)
	var claudeLogs string
	for time.Now().Before(deadline) {
		logs, err := c.ProcessLogs(ctx, claudeProc.ID, 10)
		if err == nil && strings.Contains(logs, "middleware.ts") {
			claudeLogs = logs
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(claudeLogs, "middleware.ts") {
		t.Fatalf("expected claude logs to contain middleware.ts, got: %q", claudeLogs)
	}

	// 6. Verify live logs streamed from Codex in parallel
	deadline = time.Now().Add(3 * time.Second)
	var codexLogs string
	for time.Now().Before(deadline) {
		logs, err := c.ProcessLogs(ctx, codexProc.ID, 10)
		if err == nil && strings.Contains(logs, "session_race") {
			codexLogs = logs
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(codexLogs, "session_race") {
		t.Fatalf("expected codex logs to contain session_race, got: %q", codexLogs)
	}

	// 7. Verify logs are separate and isolated
	if strings.Contains(claudeLogs, "session_race") {
		t.Errorf("claude logs should not contain codex logs")
	}
	if strings.Contains(codexLogs, "middleware.ts") {
		t.Errorf("codex logs should not contain claude logs")
	}
}
