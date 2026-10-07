package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/admirable-oss/hive/internal/process"
)

func cmdProcess(ctx context.Context, a *app, args []string) error {
	return subcommands{
		"start": procStart,
		"list":  procList,
		"get":   procGet,
		"logs":  procLogs,
		"stop":  procStop,
	}.dispatch(ctx, a, "process", args)
}

// procStart: hive process start <env> [-t|--terminal] [--] <command> [args...]
func procStart(ctx context.Context, a *app, args []string) error {
	const usage = "process start <env> [--terminal] [--] <command> [args...]"
	if err := need(args, 2, usage); err != nil {
		return err
	}
	req := process.StartRequest{EnvironmentID: args[0]}
	rest := args[1:]
	if rest[0] == "-t" || rest[0] == "--terminal" {
		req.Terminal, rest = true, rest[1:]
	}
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: hive %s", usage)
	}
	req.Command, req.Args = rest[0], rest[1:]

	p, err := a.client.ProcessStart(ctx, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "started process %s (pid %d)\n", p.ID, p.PID)
	return nil
}

// procList: hive process list [env] — every environment when env is omitted.
func procList(ctx context.Context, a *app, args []string) error {
	envID := ""
	if len(args) > 0 {
		envID = args[0]
	}
	procs, err := a.client.ProcessList(ctx, envID)
	if err != nil {
		return err
	}
	w := a.table()
	fmt.Fprintln(w, "ID\tENV\tPID\tSTATUS\tCOMMAND")
	for _, p := range procs {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", p.ID, p.EnvironmentID, p.PID, p.Status, summary(p))
	}
	return w.Flush()
}

func procGet(ctx context.Context, a *app, args []string) error {
	if err := need(args, 1, "process get <id>"); err != nil {
		return err
	}
	p, err := a.client.ProcessGet(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, "Process")
	w := a.table()
	fmt.Fprintf(w, "  ID\t%s\n", p.ID)
	fmt.Fprintf(w, "  PID\t%d\n", p.PID)
	fmt.Fprintf(w, "  Status\t%s\n", p.Status)
	fmt.Fprintf(w, "  Environment\t%s\n", p.EnvironmentID)
	fmt.Fprintf(w, "  Terminal\t%t\n", p.Terminal)
	fmt.Fprintf(w, "  Command\t%s\n", strings.Join(append([]string{p.Command}, p.Args...), " "))
	if p.ExitCode != nil {
		fmt.Fprintf(w, "  Exit Code\t%d\n", *p.ExitCode)
	}
	return w.Flush()
}

// procLogs: hive process logs <id> [lines]
func procLogs(ctx context.Context, a *app, args []string) error {
	if err := need(args, 1, "process logs <id> [lines]"); err != nil {
		return err
	}
	tail := 50
	if len(args) > 1 {
		n, err := strconv.Atoi(args[1])
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid line count %q", args[1])
		}
		tail = n
	}
	logs, err := a.client.ProcessLogs(ctx, args[0], tail)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, logs)
	return nil
}

func procStop(ctx context.Context, a *app, args []string) error {
	if err := need(args, 1, "process stop <id>"); err != nil {
		return err
	}
	if err := a.client.ProcessStop(ctx, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "stopped process %s\n", args[0])
	return nil
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
