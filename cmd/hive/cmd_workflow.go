package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/daemonctl"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/workflow"
)

// workflowTimeout bounds workflow commands that do not wait.
const workflowTimeout = 30 * time.Second

func newWorkflowCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workflow",
		Aliases: []string{"workflows", "wf"},
		Short:   "Run workflows: reusable steps started by events, schedules, pushes or by hand",
		Long: `A workflow is a TOML file of steps: agents' tasks, commands and plugin
actions, with inputs, outputs, conditions and loops. It runs when one of its
triggers fires (a Hive event such as an agent done or blocked, a schedule, a
git push or new branch, a webhook a plugin received) or when you run it, the
same way every time. Each run is recorded with the workflow's text, its inputs
and its trigger; steps retry with backoff, have timeouts, can be cancelled,
and runs resume after a daemon restart.

Your own workflows are the *.toml files in ~/.hive/workflows. Shared ones
(a repository's .hive/workflows, listed in [workflows] paths) fire only once
you trust them, and again after each change: ` + "`hive workflow trust <name>`" + `.
Running one by hand needs no trust.

` + "`hive workflow run --headless`" + ` runs a workflow in CI: with a daemon of its own,
a JSON report (--json) and an exit status.`,
		Example: `  hive workflow list
  hive workflow run review -i branch=main --wait
  hive workflow run .hive/workflows/ci.toml --headless --json
  hive workflow runs
  hive workflow report 3f2a
  hive workflow trust review`,
	}
	cmd.AddCommand(newWorkflowListCmd(a), newWorkflowShowCmd(a), newWorkflowValidateCmd(a), newWorkflowRunCmd(a),
		newWorkflowRunsCmd(a), newWorkflowStatusCmd(a), newWorkflowWaitCmd(a), newWorkflowReportCmd(a),
		newWorkflowCancelCmd(a), newWorkflowRerunCmd(a), newWorkflowFireCmd(a), newWorkflowTrustCmd(a),
		newWorkflowReloadCmd(a))
	return cmd
}

// workflowFileHelp documents the workflow file for `hive workflow run --help`.
const workflowFileHelp = `A workflow file is TOML:

  version = 1
  name = "review-push"
  timeout = "2h"             # the run's budget
  concurrency = "queue"      # queue | parallel | skip, when a trigger fires during a run
  environment = "api"        # where steps run unless they say

  [inputs.branch]
  default = "main"           # or required = true

  [[on]]                     # event, schedule, every, git, webhook or plugin
  git = "push"
  branches = ["main"]
  [on.inputs]
  branch = "${{ trigger.branch }}"

  [[step]]
  id = "review"
  agent = ["claude", "codex"] # the first kind installed here ("any": any kind)
  prompt = "Review ${{ inputs.branch }}."
  isolate = true

  [[step]]
  id = "test"
  run = ["go", "test", "./..."]   # or run = "a shell line"
  retries = 1
  backoff = "10s"
  on_failure = "continue"

  [[step]]
  id = "fix"
  if = "steps.test.outcome == 'failure'"
  agent = "any"
  prompt = "The tests fail: ${{ steps.test.output }}"

A step runs after the one before it unless needs says otherwise (needs = []
starts it at once). action = "plugin/action" with with = {…} runs a plugin's
action, and uses = "plugin/step" a plugin's step with typed inputs and
outputs; foreach and repeat = { max, until } loop. Commands may write
name=value lines to $HIVE_OUTPUT: steps.<id>.outputs.<name>. A trigger
plugin = "plugin/trigger" fires when that plugin says, with match = {…} over
its payload.`

func newWorkflowListCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the workflow files, their triggers and their latest runs",
		Args:    noArgs,
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			list, err := client.NewWorkflows(a.client).List(ctx)
			if err != nil {
				return workflowErr(err)
			}
			if list == nil {
				list = []workflow.Workflow{}
			}
			return a.emit(list, func() error {
				w := a.table()
				if len(list) == 0 {
					fmt.Fprintln(w, "no workflows: write one to ~/.hive/workflows/<name>.toml (hive workflow run --help)")
					return w.Flush()
				}
				fmt.Fprintln(w, "NAME\tTRIGGERS\tSTEPS\tFIRES\tLAST RUN\tNEXT")
				for _, wf := range list {
					last := "-"
					if wf.Last != nil {
						last = fmt.Sprintf("%s %s %s", wf.Last.ID, wf.Last.State, ago(wf.Last.CreatedAt))
					}
					next := "-"
					if !wf.Next.IsZero() {
						next = wf.Next.Local().Format("Jan 2 15:04")
					}
					fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", wf.Name, triggersText(wf), wf.Steps, fires(wf), last, next)
				}
				return w.Flush()
			})
		}),
	})
}

func triggersText(w workflow.Workflow) string {
	if w.Error != "" {
		return "(does not parse)"
	}
	var parts []string
	for _, t := range w.Triggers {
		parts = append(parts, t.Description)
	}
	return cmpStr(strings.Join(parts, "; "), "by hand")
}

// fires says whether a workflow's triggers may fire, and why not.
func fires(w workflow.Workflow) string {
	switch {
	case w.Error != "":
		return "no: " + firstLine(w.Error)
	case w.Own:
		return "yes"
	case w.Trusted:
		return "yes (trusted)"
	}
	return "no: not trusted"
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func newWorkflowShowCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:               "show <name>",
		Short:             "Show a workflow: its file, inputs, triggers, steps and trust",
		Args:              exactArgs(1),
		ValidArgsFunction: completeWorkflows(a),
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			w, err := client.NewWorkflows(a.client).Get(ctx, args[0])
			if err != nil {
				return workflowErr(err)
			}
			return a.emit(w, func() error {
				fmt.Fprintf(a.out, "%s (%s)\n", w.Name, w.Path)
				if w.Description != "" {
					fmt.Fprintf(a.out, "  %s\n", w.Description)
				}
				fmt.Fprintf(a.out, "  fires: %s\n", fires(w))
				if w.Error != "" {
					fmt.Fprintf(a.out, "\n%s\n", w.Error)
					return nil
				}
				for _, p := range w.Problems {
					fmt.Fprintf(a.out, "  problem: %s\n", p)
				}
				for _, in := range w.Inputs {
					req := ""
					if in.Required {
						req = " (required)"
					}
					fmt.Fprintf(a.out, "  input %s = %q%s  %s\n", in.Name, in.Default, req, in.Description)
				}
				for _, t := range w.Triggers {
					fmt.Fprintf(a.out, "  on %s\n", t.Description)
				}
				if w.Spec != nil {
					for _, st := range w.Spec.Steps {
						fmt.Fprintf(a.out, "  step %s: %s", st.ID, stepWhat(st))
						if len(st.Needs) > 0 {
							fmt.Fprintf(a.out, " (after %s)", strings.Join(st.Needs, ", "))
						}
						if st.If != "" {
							fmt.Fprintf(a.out, " if %s", st.If)
						}
						fmt.Fprintln(a.out)
					}
				}
				return nil
			})
		}),
	})
}

// stepWhat says what a step runs, in a few words.
func stepWhat(st workflow.StepSpec) string {
	switch st.Kind() {
	case workflow.KindAgent:
		return strings.Join(st.Agent, " or ") + " agent"
	case workflow.KindAction:
		return "action " + st.Action
	case workflow.KindStep:
		return "step " + st.Uses
	}
	if st.Shell != "" {
		return st.Shell
	}
	return strings.Join(st.Run, " ")
}

func newWorkflowValidateCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file | ->",
		Short: "Check a workflow file without running it (no daemon needed)",
		Args:  exactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			text, source, err := readWorkflowFile(args[0])
			if err != nil {
				return err
			}
			spec, err := workflow.Parse([]byte(text), source)
			if err != nil {
				return &codedError{code: exitError, msg: err.Error()}
			}
			return a.emit(spec, func() error {
				fmt.Fprintf(a.out, "%s: workflow %s, %d steps, %d triggers\n", source, spec.Name, len(spec.Steps), len(spec.Triggers))
				return nil
			})
		},
	}
}

// readWorkflowFile reads a workflow file, or standard input for "-".
func readWorkflowFile(path string) (text, source string, err error) {
	var data []byte
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, workflow.MaxFileBytes+1))
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

// isWorkflowFile reports whether arg names a file rather than a workflow.
func isWorkflowFile(arg string) bool {
	if arg == "-" || strings.HasSuffix(arg, ".toml") || strings.ContainsRune(arg, filepath.Separator) || strings.Contains(arg, "/") {
		return true
	}
	fi, err := os.Stat(arg)
	return err == nil && !fi.IsDir()
}

// parseInputs reads -i name=value flags.
func parseInputs(flags []string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range flags {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, &usageError{fmt.Errorf("-i %q: write name=value", f)}
		}
		out[k] = v
	}
	return out, nil
}

func newWorkflowRunCmd(a *app) *cobra.Command {
	var (
		inputs   []string
		dir      string
		wait     bool
		timeout  time.Duration
		headless bool
	)
	cmd := &cobra.Command{
		Use:   "run <name | file.toml | ->",
		Short: "Run a workflow by name, or a workflow file",
		Long: `Run a workflow: one the daemon knows by name, or a file (- reads standard
input). It prints the run's ID at once; --wait waits for it and prints its
report, exiting 1 when it did not succeed and 4 on --timeout.

Steps run in the current directory (or --dir) unless they name an
environment; agent steps get an environment for it.

--headless runs it with a daemon of its own, in a temporary HIVE_HOME, as
CI needs: it waits, prints the report (--json for the data), stops the daemon
and its agents, and exits by the run's outcome. Triggers do not fire there.

` + workflowFileHelp,
		Args:              exactArgs(1),
		ValidArgsFunction: completeWorkflows(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := parseInputs(inputs)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("dir") {
				dir = "."
			}
			if dir, err = a.dirArg(dir); err != nil {
				return err
			}
			o := client.RunOptions{Inputs: in, Dir: dir, By: "cli"}
			if headless {
				return runHeadless(cmd.Context(), a, args[0], o, timeout)
			}
			if err := a.ensureDaemon(cmd.Context()); err != nil {
				return err
			}
			run, err := startWorkflowRun(cmd.Context(), a.client, args[0], o)
			if err != nil {
				return err
			}
			if !wait {
				return a.emit(run, func() error {
					fmt.Fprintf(a.out, "run %s of %s started: %d steps (hive workflow report %s)\n", run.ID, run.Workflow, len(run.Steps), run.ID)
					return nil
				})
			}
			return waitAndReportRun(cmd.Context(), a, a.client, run.ID, timeout)
		},
	}
	f := cmd.Flags()
	f.StringArrayVarP(&inputs, "input", "i", nil, "an input, as name=value (repeat for more)")
	f.StringVar(&dir, "dir", "", "where steps run unless they name an environment (default: here)")
	f.BoolVar(&wait, "wait", false, "wait for the run to finish and print its report")
	f.DurationVar(&timeout, "timeout", 0, "with --wait or --headless: give up waiting after this long")
	f.BoolVar(&headless, "headless", false, "run it with a daemon of its own, as CI does, and exit by its outcome")
	return cmd
}

// startWorkflowRun starts a run of a workflow by name, or of a file.
func startWorkflowRun(ctx context.Context, c client.Client, arg string, o client.RunOptions) (workflow.Run, error) {
	wf := client.NewWorkflows(c)
	if isWorkflowFile(arg) {
		text, source, err := readWorkflowFile(arg)
		if err != nil {
			return workflow.Run{}, err
		}
		run, err := wf.RunFile(ctx, text, source, o)
		return run, workflowErr(err)
	}
	run, err := wf.Run(ctx, arg, o)
	return run, workflowErr(err)
}

// waitAndReportRun waits for a run, prints its report, and exits by it.
func waitAndReportRun(ctx context.Context, a *app, c client.Client, id string, timeout time.Duration) error {
	res, err := client.NewWorkflows(c).Wait(ctx, id, timeout)
	if err != nil {
		return workflowErr(err)
	}
	report := workflow.NewReport(res.Run, time.Now())
	if err := a.emit(report, func() error { return report.WriteText(a.out, 0) }); err != nil {
		return err
	}
	return runOutcome(res)
}

// runOutcome turns how a wait ended into an exit status: 0 for a run that
// succeeded, 1 for one that did not, 4 for a timeout.
func runOutcome(res workflow.WaitResult) error {
	switch {
	case res.Outcome == workflow.WaitTimeout:
		return &codedError{code: exitTimeout, msg: "timed out; the run goes on (hive workflow wait " + res.Run.ID + ")"}
	case res.Run.State == workflow.StateSucceeded:
		return nil
	}
	msg := fmt.Sprintf("run %s of %s %s", res.Run.ID, res.Run.Workflow, res.Run.State)
	if res.Run.Reason != "" {
		msg += ": " + res.Run.Reason
	}
	return &codedError{code: exitError, msg: msg}
}

// runHeadless runs a workflow with a daemon of its own, in a temporary
// HIVE_HOME: what CI does, with no daemon around and nothing left behind.
func runHeadless(ctx context.Context, a *app, arg string, o client.RunOptions, timeout time.Duration) error {
	if a.remote != nil {
		return &usageError{errors.New("--headless runs on this machine; leave out --remote and --machine")}
	}
	home, err := os.MkdirTemp("", "hvw") // short: the socket's path must fit
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	b, err := newApp(func(k string) string {
		if k == "HIVE_HOME" {
			return home
		}
		if k == "HIVE_SESSION" {
			return ""
		}
		return a.getenv(k)
	}, a.out, a.errOut)
	if err != nil {
		return err
	}
	b.json, b.template = a.json, a.template
	defer b.client.Close()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the hive binary: %w", err)
	}
	env := append(os.Environ(), "HIVE_HOME="+home)
	if err := daemonctl.Spawn(ctx, daemonctl.SpawnOptions{
		Executable: exe, Args: []string{"daemon"}, Env: env,
		StderrPath: b.daemonStderr(), LogPath: b.daemonLog(), Ready: b.client.Ping, Timeout: 30 * time.Second,
	}); err != nil {
		return fmt.Errorf("starting a headless daemon: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := b.shutdownDaemon(sctx, true); err != nil {
			fmt.Fprintf(a.errOut, "hive: stopping the headless daemon: %v\n", err)
		}
	}()
	if a.json {
		fmt.Fprintf(a.errOut, "hive: headless daemon in %s\n", home)
	}
	run, err := startWorkflowRun(ctx, b.client, arg, o)
	if err != nil {
		return err
	}
	if !a.json {
		fmt.Fprintf(a.errOut, "hive: running %s (run %s)\n", run.Workflow, run.ID)
	}
	return waitAndReportRun(ctx, b, b.client, run.ID, timeout)
}

func newWorkflowRunsCmd(a *app) *cobra.Command {
	var (
		name  string
		limit int
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "runs",
		Short: "List workflow runs, newest first",
		Args:  noArgs,
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			runs, err := client.NewWorkflows(a.client).Runs(ctx, name, limit)
			if err != nil {
				return workflowErr(err)
			}
			if runs == nil {
				runs = []workflow.RunSummary{}
			}
			return a.emit(runs, func() error {
				w := a.table()
				if len(runs) == 0 {
					fmt.Fprintln(w, "no runs")
					return w.Flush()
				}
				fmt.Fprintln(w, "RUN\tWORKFLOW\tSTATE\tTRIGGER\tSTEPS\tNOW\tSTARTED")
				for _, r := range runs {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Workflow, r.State, r.Trigger,
						stepProgress(r), cmpStr(strings.Join(r.Current, ", "), "-"), ago(r.CreatedAt))
				}
				return w.Flush()
			})
		}),
	})
	cmd.Flags().StringVarP(&name, "workflow", "w", "", "only this workflow's runs")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "at most this many")
	return cmd
}

// stepProgress is how many of a run's steps finished.
func stepProgress(r workflow.RunSummary) string {
	done := r.Counts[workflow.StepSucceeded] + r.Counts[workflow.StepFailed] + r.Counts[workflow.StepSkipped] + r.Counts[workflow.StepCancelled]
	s := fmt.Sprintf("%d/%d", done, r.Total)
	if n := r.Counts[workflow.StepFailed]; n > 0 {
		s += fmt.Sprintf(" (%d failed)", n)
	}
	return s
}

func newWorkflowStatusCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:               "status <run>",
		Aliases:           []string{"get"},
		Short:             "Show a run: its trigger, inputs and each step's state",
		Args:              exactArgs(1),
		ValidArgsFunction: completeRuns(a),
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			run, err := client.NewWorkflows(a.client).Status(ctx, args[0])
			if err != nil {
				return workflowErr(err)
			}
			return a.emit(run, func() error {
				fmt.Fprintf(a.out, "run %s of %s: %s (trigger %s, %s)\n", run.ID, run.Workflow, run.State, run.Trigger.Type, ago(run.CreatedAt))
				if run.Reason != "" {
					fmt.Fprintf(a.out, "  %s\n", run.Reason)
				}
				w := a.table()
				fmt.Fprintln(w, "STEP\tKIND\tSTATE\tATTEMPTS\tREASON")
				for _, st := range run.Steps {
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", st.ID, st.Kind, st.State, st.Attempts, cmpStr(firstLine(st.Reason), "-"))
				}
				return w.Flush()
			})
		}),
	})
}

func newWorkflowWaitCmd(a *app) *cobra.Command {
	var timeout time.Duration
	cmd := needsDaemon(&cobra.Command{
		Use:   "wait <run>",
		Short: "Wait for a run to finish and print its report",
		Long: `Wait for a run to finish and print its report.
Exit status: 0 succeeded, 1 did not succeed, 4 timed out.`,
		Args:              exactArgs(1),
		ValidArgsFunction: completeRuns(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			return waitAndReportRun(cmd.Context(), a, a.client, args[0], timeout)
		},
	})
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (default never)")
	return cmd
}

func newWorkflowReportCmd(a *app) *cobra.Command {
	var lines int
	cmd := needsDaemon(&cobra.Command{
		Use:               "report <run>",
		Short:             "Print a run's report: its trigger, inputs, and each step's outcome and output",
		Args:              exactArgs(1),
		ValidArgsFunction: completeRuns(a),
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			run, err := client.NewWorkflows(a.client).Status(ctx, args[0])
			if err != nil {
				return workflowErr(err)
			}
			report := workflow.NewReport(run, time.Now())
			if err := a.emit(report, func() error { return report.WriteText(a.out, lines) }); err != nil {
				return err
			}
			if !run.State.Finished() {
				return nil
			}
			return runOutcome(workflow.WaitResult{Outcome: workflow.WaitFinished, Run: run})
		}),
	})
	cmd.Flags().IntVar(&lines, "lines", 10, "output lines shown per step")
	return cmd
}

func newWorkflowCancelCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:               "cancel <run>",
		Short:             "Cancel a run: its agents' tasks are cancelled and its commands stopped",
		Args:              exactArgs(1),
		ValidArgsFunction: completeRuns(a),
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			run, err := client.NewWorkflows(a.client).Cancel(ctx, args[0])
			if err != nil {
				return workflowErr(err)
			}
			return a.done(map[string]any{"run": run}, "cancelling run %s of %s", run.ID, run.Workflow)
		}),
	})
}

func newWorkflowRerunCmd(a *app) *cobra.Command {
	var (
		wait    bool
		timeout time.Duration
	)
	cmd := needsDaemon(&cobra.Command{
		Use:               "rerun <run>",
		Short:             "Run a run again: the same workflow text, inputs and trigger",
		Args:              exactArgs(1),
		ValidArgsFunction: completeRuns(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := client.NewWorkflows(a.client).Rerun(cmd.Context(), args[0], "cli")
			if err != nil {
				return workflowErr(err)
			}
			if !wait {
				return a.emit(run, func() error {
					fmt.Fprintf(a.out, "run %s of %s started, from run %s\n", run.ID, run.Workflow, run.Trigger.From)
					return nil
				})
			}
			return waitAndReportRun(cmd.Context(), a, a.client, run.ID, timeout)
		},
	})
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the run to finish and print its report")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "with --wait: give up waiting after this long")
	return cmd
}

func newWorkflowFireCmd(a *app) *cobra.Command {
	var data string
	cmd := needsDaemon(&cobra.Command{
		Use:   "fire <webhook>",
		Short: "Fire the workflows whose webhook trigger has this name",
		Long: `Fire the workflows with a webhook trigger of this name, as a plugin that
received a webhook does. --data is the payload, as JSON; expressions read it
as trigger.data.`,
		Args: exactArgs(1),
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var payload any
			if data != "" {
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					return &usageError{fmt.Errorf("--data is not JSON: %w", err)}
				}
			}
			runs, err := client.NewWorkflows(a.client).Fire(ctx, args[0], payload)
			if err != nil {
				return workflowErr(err)
			}
			if runs == nil {
				runs = []workflow.RunSummary{}
			}
			return a.emit(runs, func() error {
				if len(runs) == 0 {
					fmt.Fprintf(a.out, "no workflow fires on webhook %s (hive workflow list)\n", args[0])
				}
				for _, r := range runs {
					fmt.Fprintf(a.out, "run %s of %s started\n", r.ID, r.Workflow)
				}
				return nil
			})
		}),
	})
	cmd.Flags().StringVar(&data, "data", "", "the payload, as JSON")
	return cmd
}

func newWorkflowTrustCmd(a *app) *cobra.Command {
	var revoke bool
	cmd := needsDaemon(&cobra.Command{
		Use:   "trust <name>",
		Short: "Let a shared workflow's triggers fire, while its file keeps its current text",
		Long: `Trust a shared workflow (from [workflows] paths): its triggers fire while its
file has the text it has now. A change to the file, such as a commit to its
repository, needs trusting again. --revoke stops its triggers. Your own
workflows (~/.hive/workflows) need no trust.`,
		Args:              exactArgs(1),
		ValidArgsFunction: completeWorkflows(a),
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			w, err := client.NewWorkflows(a.client).Trust(ctx, args[0], revoke)
			if err != nil {
				return workflowErr(err)
			}
			if revoke {
				return a.done(map[string]any{"workflow": w}, "%s no longer fires by itself", w.Name)
			}
			return a.done(map[string]any{"workflow": w}, "%s fires while %s keeps this text (sha256 %s)", w.Name, w.Path, shortHash(w.Digest))
		}),
	})
	cmd.Flags().BoolVar(&revoke, "revoke", false, "stop its triggers")
	return cmd
}

func newWorkflowReloadCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "reload",
		Short: "Read the workflow files again",
		Args:  noArgs,
		RunE: withTimeout(workflowTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			list, err := client.NewWorkflows(a.client).Reload(ctx)
			if err != nil {
				return workflowErr(err)
			}
			return a.done(map[string]any{"workflows": list}, "%d workflows", len(list))
		}),
	})
}

// workflowErr explains an older daemon's unknown method.
func workflowErr(err error) error {
	if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeUnknownMethod {
		return fmt.Errorf("the hive daemon is too old for workflows; restart it with `hive daemon restart` (%w)", err)
	}
	return err
}

func completeWorkflows(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completeTimeout)
		defer cancel()
		list, err := client.NewWorkflows(a.client).List(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveDefault
		}
		var out []cobra.Completion
		for _, w := range list {
			out = append(out, cobra.CompletionWithDesc(w.Name, cmpStr(w.Description, triggersText(w))))
		}
		return out, cobra.ShellCompDirectiveDefault
	}
}

func completeRuns(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completeTimeout)
		defer cancel()
		runs, err := client.NewWorkflows(a.client).Runs(ctx, "", 50)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, r := range runs {
			out = append(out, cobra.CompletionWithDesc(r.ID, r.Workflow+" "+string(r.State)))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
