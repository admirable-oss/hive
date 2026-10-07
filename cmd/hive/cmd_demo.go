package main

import (
	"context"
	"fmt"

	"github.com/admirable-oss/hive/internal/process"
)

const demoEnv = "acme-api"

// demoAgents are shell loops that print agent-like activity forever. The
// trailing name argument is what the dashboard shows as the agent's name.
var demoAgents = []struct{ name, script string }{
	{"auth-refactor", `echo "› read  src/auth · 214 files"
echo "› plan  rotate session tokens on refresh"
echo "› edit  src/auth/middleware.ts  +9 -3"
while true; do
  sleep 1.5; echo "› test  auth/session.spec.ts"
  sleep 1.5; echo "› edit  src/auth/session.ts  +42 -17"
  sleep 1.5; echo "› edit  src/auth/refresh.ts  +18 -6"
  sleep 1.5; echo "› commit feat(auth): session refresh logic"
  sleep 3
done`},
	{"flaky-tests", `echo "› scan  test/auth · 42 suites"
echo "› detect flaky test: session_race"
while true; do
  sleep 1.5; echo "› patch test/auth/session_test.go  +14 -2"
  sleep 1.5; echo "› run   test suite (iteration 1/5)"
  sleep 1.5; echo "› pass  all 42 test suites passed"
  sleep 3
done`},
	{"docs-sync", `echo "› sync  docs/api · fetching OpenAPI spec"
echo "› awaiting review from docs-bot"
while true; do sleep 5; done`},
	{"perf-budget", `echo "› bench  auth/token_verify · 100k ops/sec"
echo "› profile memory allocations"
while true; do
  sleep 2; echo "› optimize jwt verification buffer +12% throughput"
  sleep 3
done`},
}

func cmdDemo(ctx context.Context, a *app, _ []string) error {
	if err := a.client.Ping(ctx); err != nil {
		return fmt.Errorf("the runtime is not running; start it with: hive daemon")
	}
	_, _ = a.client.EnvironmentCreate(ctx, demoEnv) // fine if it already exists

	// Replace any demo agents left over from a previous run.
	existing, err := a.client.ProcessList(ctx, demoEnv)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Active() {
			_ = a.client.ProcessStop(ctx, p.ID)
		}
	}

	fmt.Fprintf(a.out, "Launching demo agents in %s:\n", demoEnv)
	for _, agent := range demoAgents {
		p, err := a.client.ProcessStart(ctx, process.StartRequest{
			EnvironmentID: demoEnv,
			Command:       "sh",
			Args:          []string{"-c", agent.script, agent.name},
			Terminal:      true,
		})
		if err != nil {
			return fmt.Errorf("start %s: %w", agent.name, err)
		}
		fmt.Fprintf(a.out, "  ◈ %-16s [PID %d / ID %s]\n", agent.name, p.PID, p.ID)
	}
	fmt.Fprintln(a.out, "\nAll agents running. Open the dashboard with: hive")
	return nil
}
