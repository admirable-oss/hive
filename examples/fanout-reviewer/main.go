// Command fanout-reviewer asks one agent per directory to review it, all in
// parallel, each in its own worktree, and prints what each one found.
//
//	go run ./examples/fanout-reviewer -env api -kind claude internal/auth internal/billing
//
// It needs a running Hive daemon and an environment for the repository
// (`hive env create api` in it). The agents stay open in Hive afterwards,
// so you can read their reviews in full or continue the conversation.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func main() {
	env := flag.String("env", os.Getenv("HIVE_ENV_ID"), "the repository's environment")
	kind := flag.String("kind", "claude", "the agent to review with")
	timeout := flag.Duration("timeout", 30*time.Minute, "how long a review may take")
	flag.Parse()
	dirs := flag.Args()
	if *env == "" || len(dirs) == 0 {
		log.Fatal("usage: fanout-reviewer -env <env> [-kind claude] <dir>...")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c, err := hiveapi.ConnectContext(ctx, hiveapi.Options{})
	if err != nil {
		stop()
		log.Fatalf("connect to hive: %v", err) //nolint:gocritic // stop ran
	}
	defer c.Close()

	type review struct {
		dir     string
		outcome hiveapi.Outcome
		output  []string
		err     error
	}
	reviews := make([]review, len(dirs))
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Go(func() {
			r := review{dir: dir}
			defer func() { reviews[i] = r }()
			slug := strings.NewReplacer("/", "-", ".", "-").Replace(strings.Trim(dir, "/."))
			started, err := c.StartAgent(ctx, hiveapi.StartAgent{
				Kind: *kind, EnvironmentID: *env, Worktree: "review/" + slug, Name: "review-" + slug,
			})
			if err != nil {
				r.err = err
				return
			}
			prompt := fmt.Sprintf("Review the code in %s for bugs, security problems and unclear code. "+
				"Do not change any code. End with a list of findings, most severe first, one line each.", dir)
			res, err := c.Prompt(ctx, started.Pane.ID, prompt, hiveapi.PromptOptions{
				Wait: true, Timeout: *timeout, Read: 60,
			})
			r.outcome, r.output, r.err = res.Outcome, res.Output, err
		})
	}
	wg.Wait()

	for _, r := range reviews {
		fmt.Printf("=== %s: ", r.dir)
		switch {
		case r.err != nil:
			fmt.Printf("failed: %v\n", r.err)
		case r.outcome != hiveapi.OutcomeReached:
			fmt.Printf("%s (open Hive to see it)\n", r.outcome)
		default:
			fmt.Println("done")
			fmt.Println(strings.Join(r.output, "\n"))
		}
	}
}
