package main

import (
	"context"
	"fmt"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui"
)

func cmdTUI(ctx context.Context, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	return tui.Run(c)
}

func cmdDaemon(ctx context.Context, args []string) error {
	daemon()
	return nil
}

func cmdDemo(ctx context.Context, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	if err := c.Ping(ctx); err != nil {
		return fmt.Errorf("runtime daemon is not running. Start it with: hive daemon &")
	}

	envID := "acme-api"
	_, _ = c.EnvironmentCreate(ctx, envID)

	// Clean up any stale running demo processes in this environment
	existing, _ := c.ProcessList(ctx, envID)
	for _, p := range existing {
		if p.Status == process.StatusRunning {
			_ = c.ProcessStop(ctx, p.ID)
		}
	}

	claudeScript := `echo "› read  src/auth · 214 files"
echo "› plan  rotate session tokens on refresh"
echo "› edit  src/auth/middleware.ts  +9 -3"
while true; do
  sleep 1.5
  echo "› test  auth/session.spec.ts"
  sleep 1.5
  echo "› edit  src/auth/session.ts  +42 -17"
  sleep 1.5
  echo "› edit  src/auth/refresh.ts  +18 -6"
  sleep 1.5
  echo "› commit feat(auth): session refresh logic"
  sleep 3
  echo "› read  src/auth · 214 files"
  echo "› plan  rotate session tokens on refresh"
  echo "› edit  src/auth/middleware.ts  +9 -3"
done`

	codexScript := `echo "› scan  test/auth · 42 suites"
echo "› detect flaky test: session_race"
while true; do
  sleep 1.5
  echo "› patch test/auth/session_test.go  +14 -2"
  sleep 1.5
  echo "› run   test suite (iteration 1/5)"
  sleep 1.5
  echo "› pass  all 42 test suites passed"
  sleep 3
  echo "› scan  test/auth · 42 suites"
  echo "› detect flaky test: session_race"
done`

	docsScript := `echo "› sync  docs/api · fetching OpenAPI spec"
echo "› awaiting review from docs-bot"
while true; do
  sleep 5
done`

	perfScript := `echo "› bench  auth/token_verify · 100k ops/sec"
echo "› profile memory allocations"
while true; do
  sleep 2
  echo "› optimize jwt verification buffer +12% throughput"
  sleep 3
done`

	agents := []struct {
		name   string
		script string
	}{
		{"auth-refactor", claudeScript},
		{"flaky-tests", codexScript},
		{"docs-sync", docsScript},
		{"perf-budget", perfScript},
	}

	fmt.Printf("Launching parallel agents in %s:\n", envID)
	for _, a := range agents {
		p, err := c.ProcessStartRequest(ctx, process.StartRequest{
			EnvironmentID: envID,
			Command:       "sh",
			Args:          []string{"-c", a.script, a.name},
			Terminal:      true,
		})
		if err != nil {
			return fmt.Errorf("start %s: %w", a.name, err)
		}
		fmt.Printf("  ◈ %-16s [PID %d / ID %s]\n", a.name, p.PID, p.ID)
	}

	fmt.Println("\nAll parallel agents running! Open the dashboard:")
	fmt.Println("  hive")
	return nil
}
