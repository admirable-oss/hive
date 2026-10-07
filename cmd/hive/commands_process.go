package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/admirable-oss/hive/internal/process"
)

func cmdProcess(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing process command (start, get, list, stop)")
	}
	switch args[0] {
	case "start":
		return cmdProcessStart(ctx, args[1:])
	case "get":
		return cmdProcessGet(ctx, args[1:])
	case "list":
		return cmdProcessList(ctx, args[1:])
	case "stop":
		return cmdProcessStop(ctx, args[1:])
	default:
		return fmt.Errorf("unknown process command %q", args[0])
	}
}

func cmdProcessStart(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing environment id")
	}
	envID := args[0]
	rest := args[1:]

	useTerminal := false
	var cmdParts []string

	for i := 0; i < len(rest); i++ {
		if rest[i] == "--terminal" || rest[i] == "-t" {
			useTerminal = true
			continue
		}
		if rest[i] == "--" {
			cmdParts = rest[i+1:]
			break
		}
		cmdParts = rest[i:]
		break
	}

	if len(cmdParts) == 0 {
		return fmt.Errorf("missing command to run")
	}

	cmdName := cmdParts[0]
	cmdArgs := cmdParts[1:]

	c, err := newClient()
	if err != nil {
		return err
	}
	p, err := c.ProcessStartRequest(ctx, process.StartRequest{
		EnvironmentID: envID,
		Command:       cmdName,
		Args:          cmdArgs,
		Terminal:      useTerminal,
	})
	if err != nil {
		return err
	}
	fmt.Printf("started process %s\n", p.ID)
	return nil
}

func cmdProcessGet(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing process id")
	}
	id := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	p, err := c.ProcessGet(ctx, id)
	if err != nil {
		return err
	}

	fmt.Println("Process")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintf(w, "  ID\t%s\n", p.ID)
	fmt.Fprintf(w, "  PID\t%d\n", p.PID)
	fmt.Fprintf(w, "  Status\t%s\n", p.Status)
	fmt.Fprintf(w, "  Environment\t%s\n", p.EnvironmentID)
	if p.Terminal {
		fmt.Fprintf(w, "  Terminal\ttrue\n")
	}
	cmdStr := p.Command
	if len(p.Args) > 0 {
		cmdStr += " " + strings.Join(p.Args, " ")
	}
	fmt.Fprintf(w, "  Command\t%s\n", cmdStr)
	if p.ExitCode != nil {
		fmt.Fprintf(w, "  Exit Code\t%d\n", *p.ExitCode)
	}
	return w.Flush()
}

func cmdProcessList(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing environment id")
	}
	envID := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	procs, err := c.ProcessList(ctx, envID)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "ID\tPID\tSTATUS\tCOMMAND")
	for _, p := range procs {
		cmdStr := p.Command
		if len(p.Args) > 0 {
			cmdStr += " " + strings.Join(p.Args, " ")
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", p.ID, p.PID, p.Status, cmdStr)
	}
	return w.Flush()
}

func cmdProcessStop(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing process id")
	}
	id := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	err = c.ProcessStop(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("stopped process %s\n", id)
	return nil
}
