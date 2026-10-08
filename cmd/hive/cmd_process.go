package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/process"
)

func newProcessCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "process",
		Aliases: []string{"ps"},
		Short:   "Start, inspect and stop agent processes",
	}
	cmd.AddCommand(
		newProcessStartCmd(a),
		needsDaemon(&cobra.Command{
			Use:               "list [env]",
			Aliases:           []string{"ls"},
			Short:             "List processes (all environments when none is given)",
			Args:              rangeArgs(0, 1),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(10*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
				envID := ""
				if len(args) > 0 {
					envID = args[0]
				}
				procs, err := a.client.ProcessList(ctx, envID)
				if err != nil {
					return err
				}
				return a.emit(procs, func() error {
					w := a.table()
					fmt.Fprintln(w, "ID\tENV\tPID\tSTATUS\tSTARTED\tCOMMAND")
					for _, p := range procs {
						fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", p.ID, p.EnvironmentID, p.PID, p.Status, formatTime(p.StartedAt), summary(p))
					}
					return w.Flush()
				})
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:               "get <id>",
			Short:             "Show a process",
			Args:              exactArgs(1),
			ValidArgsFunction: completeProcesses(a, false),
			RunE: withTimeout(10*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
				p, err := a.client.ProcessGet(ctx, args[0])
				if err != nil {
					return err
				}
				return a.emit(p, func() error {
					w := a.table()
					fmt.Fprintf(w, "ID\t%s\n", p.ID)
					fmt.Fprintf(w, "PID\t%d\n", p.PID)
					fmt.Fprintf(w, "Status\t%s\n", p.Status)
					fmt.Fprintf(w, "Environment\t%s\n", p.EnvironmentID)
					fmt.Fprintf(w, "Terminal\t%t\n", p.Terminal)
					fmt.Fprintf(w, "Command\t%s\n", strings.Join(append([]string{p.Command}, p.Args...), " "))
					fmt.Fprintf(w, "Started\t%s\n", formatTime(p.StartedAt))
					if p.EndedAt != nil {
						fmt.Fprintf(w, "Ended\t%s\n", formatTime(*p.EndedAt))
					}
					if p.ExitCode != nil {
						fmt.Fprintf(w, "Exit code\t%d\n", *p.ExitCode)
					}
					return w.Flush()
				})
			}),
		}),
		newProcessLogsCmd(a),
		needsDaemon(&cobra.Command{
			Use:               "stop <id>",
			Short:             "Stop a process and everything it started",
			Args:              exactArgs(1),
			ValidArgsFunction: completeProcesses(a, true),
			RunE: withTimeout(10*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
				if err := a.client.ProcessStop(ctx, args[0]); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "stopped process %s\n", args[0])
				return nil
			}),
		}),
	)
	return cmd
}

func newProcessStartCmd(a *app) *cobra.Command {
	var (
		req           process.StartRequest
		width, height uint16
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "start [-t] <env> [--] <command> [args...]",
		Short: "Start a process in an environment",
		Example: `  hive ps start dev -- npm test
  hive ps start -t dev -- claude --model opus`,
		Args:              minArgs(2),
		ValidArgsFunction: completeEnvironments(a),
		RunE: withTimeout(10*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			env, rest := args[0], args[1:]
			// Accept the original form `start <env> -t -- cmd` too: flags
			// stop parsing at <env> so the command's own flags pass through.
			if rest[0] == "-t" || rest[0] == "--terminal" {
				req.Terminal, rest = true, rest[1:]
			}
			if len(rest) > 0 && rest[0] == "--" {
				rest = rest[1:]
			}
			if len(rest) == 0 {
				return &usageError{fmt.Errorf("missing the command to run")}
			}
			req.EnvironmentID, req.Command, req.Args = env, rest[0], rest[1:]
			req.Width, req.Height = width, height

			p, err := a.client.ProcessStart(ctx, req)
			if err != nil {
				return err
			}
			return a.emit(p, func() error {
				fmt.Fprintf(a.out, "started process %s (pid %d)\n", p.ID, p.PID)
				return nil
			})
		}),
	})
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVarP(&req.Terminal, "terminal", "t", false, "run the process in a terminal (needed for interactive agents)")
	cmd.Flags().Uint16Var(&width, "width", 0, "terminal width (default from config)")
	cmd.Flags().Uint16Var(&height, "height", 0, "terminal height (default from config)")
	return cmd
}

func newProcessLogsCmd(a *app) *cobra.Command {
	var (
		tail   int
		follow bool
		stderr bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:               "logs <id> [lines]",
		Short:             "Print a process's output",
		Args:              rangeArgs(1, 2),
		ValidArgsFunction: completeProcesses(a, false),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 { // `logs <id> <n>`, the original form
				n, err := strconv.Atoi(args[1])
				if err != nil || n < 0 {
					return &usageError{fmt.Errorf("invalid line count %q", args[1])}
				}
				tail = n
			}
			req := process.LogsRequest{ID: args[0], Tail: tail, Follow: follow}
			if stderr {
				req.Stream = process.StreamStderr
			}
			ctx := cmd.Context()
			if !follow {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
			}
			return a.client.ProcessLogsStream(ctx, req, a.out)
		},
	})
	cmd.Flags().IntVarP(&tail, "lines", "n", 50, "number of lines to show (0 for the whole log)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new output until the process exits")
	cmd.Flags().BoolVar(&stderr, "stderr", false, "show standard error instead of standard output")
	return cmd
}

// summary is a one-line, at most 50-column description of what p runs.
func summary(p process.Process) string {
	s := p.DisplayName()
	if s == p.Command && len(p.Args) > 0 {
		s += " " + strings.Join(p.Args, " ")
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + "..."
	}
	if r := []rune(s); len(r) > 50 {
		s = string(r[:47]) + "..."
	}
	return s
}
