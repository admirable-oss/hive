// Command plan-review reviews directories as a plan: one agent per
// directory in parallel, each in its own worktree, then one more that reads
// every review and writes the summary. Hive keeps the limits, the order and
// the results; the program only submits the plan and prints its report.
//
//	go run ./examples/plan-review -env api -kind claude internal/auth internal/billing
//
// It needs a running Hive daemon and an environment for the repository
// (`hive env create api` in it). The plan survives this program: follow it
// with `hive task list`, and reprint the report with `hive task report`.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func main() {
	env := flag.String("env", os.Getenv("HIVE_ENV_ID"), "the repository's environment")
	kind := flag.String("kind", "claude", "the agent to review with")
	parallel := flag.Int("parallel", 3, "reviews running at once")
	timeout := flag.Duration("timeout", 30*time.Minute, "how long a review may take")
	flag.Parse()
	dirs := flag.Args()
	if *env == "" || len(dirs) == 0 {
		log.Fatal("usage: plan-review -env <env> [-kind claude] [-parallel 3] <dir>...")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c, err := hiveapi.ConnectContext(ctx, hiveapi.Options{})
	if err != nil {
		stop()
		log.Fatalf("connect to hive: %v", err) //nolint:gocritic // stop ran
	}
	defer c.Close()

	spec := hiveapi.PlanSpec{
		Name:        "review " + strings.Join(dirs, ", "),
		MaxParallel: *parallel,
		Defaults:    &hiveapi.TaskSpec{Kind: *kind, EnvironmentID: *env, Isolate: true, TimeoutMS: timeout.Milliseconds()},
	}
	var reviews []string
	for _, dir := range dirs {
		id := "review-" + strings.NewReplacer("/", "-", ".", "-").Replace(strings.Trim(dir, "/."))
		reviews = append(reviews, id)
		spec.Tasks = append(spec.Tasks, hiveapi.TaskSpec{
			ID: id, Title: "Review " + dir,
			Prompt: fmt.Sprintf("Review the code in %s for bugs, security problems and unclear code. "+
				"Do not change any code. Write your findings, most severe first, one line each, to REVIEW.md, and commit it.", dir),
		})
	}
	spec.Tasks = append(spec.Tasks, hiveapi.TaskSpec{
		ID: "summary", Title: "Summarise the reviews", Needs: reviews,
		Prompt: "Other agents reviewed parts of this repository on the branches hive/$HIVE_PLAN_ID/review-*. " +
			"Read each branch's REVIEW.md (git show <branch>:REVIEW.md) and write SUMMARY.md: the ten most severe findings overall.",
	})

	plan, err := c.RunPlan(ctx, spec)
	if err != nil {
		log.Fatalf("run the plan: %v", err)
	}
	fmt.Printf("plan %s: %d tasks (hive task get %s)\n", plan.ID, len(plan.Tasks), plan.ID)

	// Wait, saying (once) when a task needs someone: a permission prompt.
	told := map[string]bool{}
	for {
		res, err := c.WaitPlan(ctx, plan.ID, hiveapi.PlanWaitOptions{Until: hiveapi.PlanWaitAttention, Timeout: time.Minute})
		if err != nil {
			log.Fatalf("wait: %v", err)
		}
		if res.Outcome == hiveapi.PlanOutcomeAttention {
			for _, t := range res.Plan.Summary().Attention {
				if key := t.ID + string(t.State); !told[key] {
					told[key] = true
					fmt.Printf("task %s needs you: open its pane (%s) in Hive\n", t.ID, t.PaneID)
				}
			}
			// Wait for the next change before asking again.
			if _, err := c.WaitPlan(ctx, plan.ID, hiveapi.PlanWaitOptions{Until: hiveapi.PlanWaitChange, After: res.Plan.Rev}); err != nil {
				log.Fatalf("wait: %v", err)
			}
			continue
		}
		if res.Outcome == hiveapi.PlanOutcomeFinished {
			if err := hiveapi.NewReport(res.Plan).WriteText(os.Stdout, 0); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
}
