package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/task"
)

// taskTimeout bounds task commands that do not wait.
const taskTimeout = 15 * time.Second

func newTaskCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"tasks", "plan"},
		Short:   "Run plans: many agents working through tasks with dependencies",
		Long: `A plan is tasks for coding agents. Each task is a prompt for an agent kind,
run in an environment or a fresh worktree, with a completion condition and a
time budget; needs order them. Hive runs a plan's tasks in parallel within its
limits (max_parallel, per-kind caps, each machine's capacity), places them on
this machine and saved machines by label, stops for approvals, and keeps every
task's result: its last screen, the files it changed, its exit status and the
summary its agent wrote. Plans survive a daemon restart.

Write a plan file (see ` + "`hive task run --help`" + `), or run one task from flags.
Commands take a plan ID or a unique prefix of one.`,
		Example: `  hive task run plan.toml --wait
  hive task run -p "Fix the flaky test in api/" --kind claude --env api --isolate
  hive task list
  hive task approve 3f2a deploy
  hive task report 3f2a`,
	}
	cmd.AddCommand(newTaskRunCmd(a), newTaskListCmd(a), newTaskGetCmd(a), newTaskWaitCmd(a),
		newTaskCancelCmd(a), newTaskRetryCmd(a), newTaskApproveCmd(a), newTaskReportCmd(a))
	return cmd
}

// planFileHelp documents the plan file for `hive task run --help`.
const planFileHelp = `A plan file is TOML:

  name = "session refactor"
  max_parallel = 4          # tasks of this plan running at once
  timeout = "2h"            # the plan's budget; what is left is cancelled

  [kinds]                   # per agent kind, tasks running at once
  claude = 2

  [defaults]                # anything a [[task]] may say
  kind = "claude"
  environment = "api"
  isolate = true            # each task in a new worktree on hive/<plan>/<task>
  timeout = "30m"

  [[task]]
  id = "schema"
  prompt = "Design the new session schema in docs/session.md."

  [[task]]
  id = "api"
  needs = ["schema"]        # starts once schema succeeded
  approve = true            # and a person said yes
  prompt = "Implement docs/session.md."
  until = "command"         # done (default) | idle | match | command
  command = ["go", "test", "./..."]
  labels = ["linux"]        # on a machine with these labels (or machine = "…")
  retries = 1

until = "done" ends a task when its agent finishes the turn the prompt began,
"idle" when it is ready for another prompt, "match" when its screen matches
match = "<regular expression>", and "command" when command exits 0 after the
turn (any other status fails it). A blocked agent pauses only its own task.
Agents are asked to write a summary to $HIVE_TASK_SUMMARY (no_summary = true
skips that); it is part of the task's result.`

func newTaskRunCmd(a *app) *cobra.Command {
	var (
		s       task.Spec
		name    string
		timeout time.Duration
		wait    bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "run [plan.toml | -]",
		Short: "Run a plan file, or one task given by flags",
		Long: `Run a plan, from a file (- reads standard input) or as one task given by
--prompt. It prints the plan's ID at once; --wait waits for it to finish and
prints its report, exiting 1 when a task did not succeed.

` + planFileHelp,
		Args: rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tasks := client.NewTasks(a.client)
			var (
				plan task.Plan
				err  error
			)
			switch {
			case len(args) == 1 && cmd.Flags().Changed("prompt"):
				return &usageError{errors.New("give a plan file or --prompt, not both")}
			case len(args) == 1:
				text, source, rerr := readPlan(args[0])
				if rerr != nil {
					return rerr
				}
				plan, err = tasks.RunFile(ctx, text, source)
			case s.Prompt == "":
				return &usageError{errors.New("give a plan file, or a task with --prompt")}
			default:
				s.TimeoutMS = timeout.Milliseconds()
				if s.Until == task.UntilCommand && len(s.Command) == 0 {
					return &usageError{errors.New("--until command needs --check")}
				}
				if len(s.Command) > 0 && s.Until == "" {
					s.Until = task.UntilCommand
				}
				plan, err = tasks.Run(ctx, task.PlanSpec{Name: name, Tasks: []task.Spec{s}})
			}
			if err != nil {
				return err
			}
			if !wait {
				return a.emit(plan, func() error {
					fmt.Fprintf(a.out, "plan %s started: %d tasks (hive task get %s)\n", plan.ID, len(plan.Tasks), plan.ID)
					return nil
				})
			}
			return waitAndReport(ctx, a, plan.ID)
		},
	})
	f := cmd.Flags()
	f.StringVarP(&s.Prompt, "prompt", "p", "", "one task's prompt (instead of a plan file)")
	f.StringVar(&s.ID, "id", "task", "the task's ID")
	f.StringVar(&s.Kind, "kind", "claude", "the agent kind (see hive agent manifests)")
	f.StringVarP(&s.EnvironmentID, "env", "e", "", "the environment to run in")
	f.StringVarP(&s.Worktree, "worktree", "w", "", "a branch: run in a new worktree on it")
	f.BoolVar(&s.Isolate, "isolate", false, "run in a new worktree on hive/<plan>/<task>")
	f.StringVar(&s.Base, "base", "", "where a new worktree branch starts (default HEAD)")
	f.StringVar((*string)(&s.Until), "until", "", "done (default), idle, match or command")
	f.StringVar(&s.Match, "match", "", "with --until match: the regular expression to wait for")
	f.StringArrayVar(&s.Command, "check", nil, "a command whose exit status decides (repeat for its arguments: --check go --check test)")
	f.DurationVar(&timeout, "timeout", 0, "the task's time budget (default none)")
	f.BoolVar(&s.Approve, "approve", false, "wait for approval before starting")
	f.StringVar(&s.Machine, "machine", "", "run on this saved machine (local: this one)")
	f.StringArrayVar(&s.Labels, "label", nil, "run on a machine with this label (repeat for more)")
	f.IntVar(&s.Retries, "retries", 0, "retry a failed attempt this many times")
	f.StringVar(&name, "name", "", "the plan's name")
	f.BoolVar(&wait, "wait", false, "wait for the plan to finish and print its report")
	return cmd
}

// readPlan reads a plan file, or standard input for "-".
func readPlan(path string) (text, source string, err error) {
	var data []byte
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, task.MaxPlanBytes+1))
		source = "<stdin>"
	} else {
		data, err = os.ReadFile(path)
		source = filepath.Base(path)
	}
	if err != nil {
		return "", "", &usageError{err}
	}
	return string(data), source, nil
}

// waitAndReport waits for a plan, prints its report, and exits by it.
func waitAndReport(ctx context.Context, a *app, id string) error {
	res, err := client.NewTasks(a.client).Wait(ctx, id, client.TaskWaitOptions{})
	if err != nil {
		return err
	}
	report := task.NewReport(res.Plan, time.Now())
	if err := a.emit(report, func() error { return report.WriteText(a.out, 0) }); err != nil {
		return err
	}
	return planOutcome(res)
}

// planOutcome turns how a wait ended into an exit status: 0 for a plan (or
// task) that succeeded, 1 for one that did not, 4 for a timeout and 5 for
// a task waiting for a person.
func planOutcome(res task.WaitResult) error {
	switch res.Outcome {
	case task.OutcomeTimeout:
		return &codedError{code: exitTimeout, msg: "timed out"}
	case task.OutcomeAttention:
		return &codedError{code: exitAgent, msg: "a task waits for you (hive task get " + res.Plan.ID + ")"}
	case task.OutcomeFinished:
		if res.Plan.State == task.PlanSucceeded || res.Plan.State == task.PlanRunning {
			return nil // succeeded, or (waiting for one task) the plan goes on
		}
		s := res.Plan.Summary()
		bad := s.Counts[task.StateFailed] + s.Counts[task.StateCancelled] + s.Counts[task.StateSkipped]
		return &codedError{code: exitError, msg: fmt.Sprintf("plan %s %s: %d of %d tasks did not succeed", res.Plan.ID, res.Plan.State, bad, s.Total)}
	}
	return nil
}

func newTaskListCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List plans, newest first",
		Args:    noArgs,
		RunE: withTimeout(taskTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			plans, err := client.NewTasks(a.client).List(ctx)
			if err != nil {
				return err
			}
			if plans == nil {
				plans = []task.Summary{}
			}
			return a.emit(plans, func() error {
				w := a.table()
				fmt.Fprintln(w, "PLAN\tNAME\tSTATE\tPROGRESS\tWAITING\tSTARTED")
				for _, p := range plans {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, cmpStr(p.Name, "-"), p.State,
						progress(p), cmpStr(attention(p), "-"), ago(p.CreatedAt))
				}
				return w.Flush()
			})
		}),
	})
}

// progress is "3/10 done, 2 running" for a plan.
func progress(p task.Summary) string {
	parts := []string{fmt.Sprintf("%d/%d done", p.Counts[task.StateSucceeded], p.Total)}
	for _, s := range []task.State{task.StateRunning, task.StateBlocked, task.StateQueued, task.StateFailed, task.StateSkipped, task.StateCancelled} {
		if n := p.Counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	return strings.Join(parts, ", ")
}

// attention lists the tasks of a plan that wait for a person.
func attention(p task.Summary) string {
	var out []string
	for _, t := range p.Attention {
		what := "approval"
		if t.State == task.StateBlocked {
			what = "blocked"
		}
		out = append(out, t.ID+" ("+what+")")
	}
	return strings.Join(out, ", ")
}

// planCmd is a command on a plan (and maybe one task): <plan> [task].
func planCmd(a *app, c *cobra.Command, run func(ctx context.Context, t client.Tasks, id, taskID string) error) *cobra.Command {
	c.Args = rangeArgs(1, 2)
	c.ValidArgsFunction = completePlans(a)
	c.RunE = withTimeout(taskTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
		taskID := ""
		if len(args) == 2 {
			taskID = args[1]
		}
		return run(ctx, client.NewTasks(a.client), args[0], taskID)
	})
	return needsDaemon(c)
}

func newTaskGetCmd(a *app) *cobra.Command {
	return planCmd(a, &cobra.Command{
		Use:     "get <plan> [task]",
		Aliases: []string{"show"},
		Short:   "Show a plan and its tasks, or one task in full",
	}, func(ctx context.Context, t client.Tasks, id, taskID string) error {
		p, err := t.Get(ctx, id)
		if err != nil {
			return err
		}
		if taskID != "" {
			x := p.Task(taskID)
			if x == nil {
				return fmt.Errorf("plan %s has no task %q", p.ID, taskID)
			}
			return a.emit(x, func() error { return printTask(a, p, x) })
		}
		return a.emit(p, func() error { return printPlan(a, p) })
	})
}

func printPlan(a *app, p task.Plan) error {
	s := p.Summary()
	fmt.Fprintf(a.out, "plan %s", p.ID)
	if p.Name != "" {
		fmt.Fprintf(a.out, " · %s", p.Name)
	}
	fmt.Fprintf(a.out, ": %s, %s (started %s)\n", p.State, progress(s), ago(p.CreatedAt))
	if p.Reason != "" {
		fmt.Fprintf(a.out, "  %s\n", p.Reason)
	}
	fmt.Fprintln(a.out)
	w := a.table()
	fmt.Fprintln(w, "TASK\tSTATE\tKIND\tMACHINE\tPANE\tTRY\tNEEDS\tWHY")
	for i := range p.Tasks {
		t := &p.Tasks[i]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", t.Spec.ID, t.State, t.Spec.Kind, cmpStr(t.Machine, "-"),
			cmpStr(t.PaneID, "-"), t.Attempt, cmpStr(strings.Join(t.Spec.Needs, ","), "-"), cmpStr(t.Reason, "-"))
	}
	return w.Flush()
}

func printTask(a *app, p task.Plan, t *task.Task) error {
	w := a.table()
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%s\t%s\n", k, v)
		}
	}
	row("task", p.ID+"/"+t.Spec.ID)
	row("title", t.Spec.Title)
	row("state", string(t.State))
	row("why", t.Reason)
	row("kind", t.Spec.Kind)
	row("machine", t.Machine)
	row("environment", t.EnvironmentID)
	row("path", t.Path)
	row("branch", t.Spec.Branch(p.ID))
	row("pane", t.PaneID)
	if t.Attempt > 0 {
		row("attempt", fmt.Sprint(t.Attempt))
	}
	row("needs", strings.Join(t.Spec.Needs, ", "))
	if err := w.Flush(); err != nil {
		return err
	}
	if len(t.Log) > 0 {
		fmt.Fprintln(a.out, "\nlog:")
		for _, e := range t.Log {
			fmt.Fprintf(a.out, "  %s  %s\n", e.Time.Local().Format(time.TimeOnly), e.Message)
		}
	}
	if t.Result != nil {
		r := task.NewReport(task.Plan{ID: p.ID, Tasks: []task.Task{*t}}, time.Now())
		fmt.Fprintln(a.out)
		return r.WriteText(a.out, task.MaxScreenLines)
	}
	return nil
}

func newTaskWaitCmd(a *app) *cobra.Command {
	var (
		o     client.TaskWaitOptions
		until string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "wait <plan> [task]",
		Short: "Wait for a plan (or one task) to finish, or to need you",
		Long: `Wait for a plan, or one of its tasks, to finish. --until attention also ends
when a task waits for approval or its agent is blocked on a decision; change
ends at the plan's next change.
Exit status: 0 succeeded (or changed), 1 did not succeed, 4 timed out, 5 a
task waits for you.`,
		Args:              rangeArgs(1, 2),
		ValidArgsFunction: completePlans(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				o.Task = args[1]
			}
			o.Until = task.WaitUntil(until)
			if o.Until == task.WaitChange {
				p, err := client.NewTasks(a.client).Get(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				o.After = p.Rev
			}
			res, err := client.NewTasks(a.client).Wait(cmd.Context(), args[0], o)
			if err != nil {
				return err
			}
			if err := a.emit(res, func() error {
				fmt.Fprintf(a.out, "%s: ", res.Outcome)
				return printPlan(a, res.Plan)
			}); err != nil {
				return err
			}
			if o.Task != "" && res.Outcome == task.OutcomeFinished {
				if t := res.Plan.Task(o.Task); t != nil && t.State != task.StateSucceeded {
					return &codedError{code: exitError, msg: fmt.Sprintf("task %s %s", t.Spec.ID, t.State)}
				}
				return nil
			}
			return planOutcome(res)
		},
	})
	cmd.Flags().StringVar(&until, "until", string(task.WaitFinished), "finished, attention or change")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", 0, "give up after this long (default never)")
	return cmd
}

func newTaskCancelCmd(a *app) *cobra.Command {
	return planCmd(a, &cobra.Command{
		Use:   "cancel <plan> [task]",
		Short: "Cancel a plan, or one task (its agent is stopped; what needs it is skipped)",
	}, func(ctx context.Context, t client.Tasks, id, taskID string) error {
		p, err := t.Cancel(ctx, id, taskID)
		if err != nil {
			return err
		}
		return a.done(map[string]any{"plan": p}, "cancelling %s", target(p.ID, taskID))
	})
}

func newTaskRetryCmd(a *app) *cobra.Command {
	return planCmd(a, &cobra.Command{
		Use:   "retry <plan> [task]",
		Short: "Run failed, cancelled and skipped tasks again (in the worktrees they had)",
	}, func(ctx context.Context, t client.Tasks, id, taskID string) error {
		p, err := t.Retry(ctx, id, taskID)
		if err != nil {
			return err
		}
		return a.done(map[string]any{"plan": p}, "retrying %s", target(p.ID, taskID))
	})
}

func newTaskApproveCmd(a *app) *cobra.Command {
	var o client.ApproveOptions
	cmd := planCmd(a, &cobra.Command{
		Use:   "approve <plan> [task]",
		Short: "Let a task waiting for approval start (--deny cancels it)",
		Long: `Let a task that waits for approval start. The task may be left out when only
one waits. --deny cancels it instead, and skips what needs it; ` + "`hive task retry`" + `
asks again.`,
	}, func(ctx context.Context, t client.Tasks, id, taskID string) error {
		if o.By == "" {
			o.By = cmpStr(a.getenv("USER"), a.getenv("USERNAME"))
		}
		p, err := t.Approve(ctx, id, taskID, o)
		if err != nil {
			return err
		}
		verb := "approved"
		if o.Deny {
			verb = "refused"
		}
		return a.done(map[string]any{"plan": p}, "%s %s", verb, target(p.ID, taskID))
	})
	cmd.Flags().BoolVar(&o.Deny, "deny", false, "refuse: cancel the task")
	cmd.Flags().StringVar(&o.Note, "note", "", "why, for the task's log")
	return cmd
}

func newTaskReportCmd(a *app) *cobra.Command {
	var screen int
	cmd := planCmd(a, &cobra.Command{
		Use:   "report <plan>",
		Short: "Print what a plan did: each task's outcome, changed files and summary",
	}, func(ctx context.Context, t client.Tasks, id, taskID string) error {
		if taskID != "" {
			return &usageError{errors.New("report takes a plan only (hive task get <plan> <task> shows one task)")}
		}
		p, err := t.Get(ctx, id)
		if err != nil {
			return err
		}
		r := task.NewReport(p, time.Now())
		return a.emit(r, func() error { return r.WriteText(a.out, screen) })
	})
	cmd.Flags().IntVar(&screen, "screen", 0, "also show each task's last n screen lines")
	return cmd
}

func target(planID, taskID string) string {
	if taskID == "" {
		return "plan " + planID
	}
	return "task " + taskID + " of plan " + planID
}

// completePlans offers plan IDs, then the plan's task IDs.
func completePlans(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completeTimeout)
		defer cancel()
		tasks := client.NewTasks(a.client)
		var out []cobra.Completion
		if len(args) == 1 {
			p, err := tasks.Get(ctx, args[0])
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			for _, t := range p.Tasks {
				out = append(out, cobra.CompletionWithDesc(t.Spec.ID, string(t.State)))
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}
		plans, err := tasks.List(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		for _, p := range plans {
			out = append(out, cobra.CompletionWithDesc(p.ID, cmpStr(p.Name, string(p.State))))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
