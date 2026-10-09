package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
)

const (
	demoEnv = "acme-api"
	demoTab = "demo"
)

// demoAgents are shell loops that print agent-like activity forever. The
// trailing name argument is what the UI shows as the agent's name.
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

func newDemoCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "demo",
		Short: "Open a tab of four demo agents to explore the multiplexer",
		Args:  noArgs,
		RunE:  withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error { return runDemo(ctx, a) }),
	})
}

func runDemo(ctx context.Context, a *app) error {
	_, _ = a.client.EnvironmentCreate(ctx, environment.CreateRequest{ID: demoEnv}) // fine if it already exists
	ws := client.NewWorkspace(a.client)

	// Replace whatever a previous run left: its tab, and any stray agent.
	tabs, err := ws.TabList(ctx, demoEnv)
	if err != nil {
		return err
	}
	for _, t := range tabs {
		if t.Name == demoTab {
			_ = ws.TabClose(ctx, t.ID)
		}
	}
	existing, err := a.client.ProcessList(ctx, demoEnv)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Active() {
			_ = a.client.ProcessStop(ctx, p.ID)
		}
	}

	// A 2×2 grid: the first agent opens the tab, the others split it.
	spec := func(i int) pane.Spec {
		ag := demoAgents[i]
		return pane.Spec{Name: ag.name, Command: []string{"sh", "-c", ag.script, ag.name}}
	}
	fmt.Fprintf(a.out, "Launching demo agents in %s:\n", demoEnv)
	created, err := ws.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: demoEnv, Name: demoTab, Pane: spec(0)})
	if err != nil {
		return fmt.Errorf("start %s: %w", demoAgents[0].name, err)
	}
	panes := []pane.Pane{created.Pane}
	no := false
	for i, at := range []struct {
		from int
		d    layout.Direction
	}{{0, layout.Right}, {0, layout.Down}, {1, layout.Down}} {
		p, err := ws.PaneSplit(ctx, pane.SplitRequest{Pane: panes[at.from].ID, Direction: at.d, Spec: spec(i + 1), Focus: &no})
		if err != nil {
			return fmt.Errorf("start %s: %w", demoAgents[i+1].name, err)
		}
		panes = append(panes, p)
	}
	for _, p := range panes {
		fmt.Fprintf(a.out, "  ◈ %-16s [pane %s / process %s]\n", p.Name, p.ID, p.ProcessID)
	}
	fmt.Fprintln(a.out, "\nAll agents running. Open them with: hive  (then C-b O for the Overview, C-b d to leave)")
	return nil
}
