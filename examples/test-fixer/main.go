// Command test-fixer runs a Go module's tests and sends one agent at each
// failing package, in its own worktree, to make it pass. It reports which
// agents finished and where their work is; merging is up to you.
//
//	go run ./examples/test-fixer -env api -kind codex
//
// Run it in the repository's directory, with a Hive environment for it
// (`hive env create api`). Agents that block on a permission prompt are
// reported, not answered: look at them in Hive.
package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func main() {
	env := flag.String("env", os.Getenv("HIVE_ENV_ID"), "the repository's environment")
	kind := flag.String("kind", "codex", "the agent to fix with")
	maxAgents := flag.Int("max", 4, "at most this many agents")
	timeout := flag.Duration("timeout", time.Hour, "how long a fix may take")
	flag.Parse()
	if *env == "" {
		log.Fatal("usage: test-fixer -env <env> [-kind codex] [-max 4]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	failing := failingPackages(ctx)
	if len(failing) == 0 {
		fmt.Println("every package passes")
		return
	}
	if len(failing) > *maxAgents {
		failing = failing[:*maxAgents]
	}
	c, err := hiveapi.ConnectContext(ctx, hiveapi.Options{})
	if err != nil {
		stop()
		log.Fatalf("connect to hive: %v", err) //nolint:gocritic // stop ran
	}
	defer c.Close()

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, pkg := range failing {
		wg.Go(func() {
			report := func(format string, args ...any) {
				mu.Lock()
				defer mu.Unlock()
				fmt.Printf("%-40s "+format+"\n", append([]any{pkg}, args...)...)
			}
			slug := strings.NewReplacer("/", "-", ".", "-").Replace(strings.TrimPrefix(pkg, "./"))
			started, err := c.StartAgent(ctx, hiveapi.StartAgent{
				Kind: *kind, EnvironmentID: *env, Worktree: "fix/" + slug, Name: "fix-" + slug,
			})
			if err != nil {
				report("could not start an agent: %v", err)
				return
			}
			report("agent %s working in %s", started.Pane.ID, started.Environment.Path)
			prompt := fmt.Sprintf("The tests of the Go package %s fail. Run `go test %s`, find the cause and fix it "+
				"(fix the code, not the test, unless the test is wrong). Run the tests again until they pass, then commit.", pkg, pkg)
			res, err := c.Prompt(ctx, started.Pane.ID, prompt, hiveapi.PromptOptions{Wait: true, Timeout: *timeout})
			switch {
			case err != nil:
				report("failed: %v", err)
			case res.Outcome == hiveapi.OutcomeReached:
				report("done: review branch fix/%s", slug)
			default:
				report("%s: see agent %s in Hive", res.Outcome, started.Pane.ID)
			}
		})
	}
	wg.Wait()
}

// failingPackages runs `go test ./...` and returns the packages that fail.
func failingPackages(ctx context.Context) []string {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run() // failing tests are what we are after
	var pkgs []string
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "FAIL" && f[1] != "" && !strings.HasPrefix(f[1], "[") {
			pkgs = append(pkgs, f[1])
		}
	}
	return pkgs
}
