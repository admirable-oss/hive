// Command hive is the CLI. `hive daemon` runs the runtime; every other command
// is a thin client of it. With no arguments, hive opens the dashboard.
//
// Call chain: main → run → newApp (composition root) → command.run → client →
// socket → daemon (see internal/runtime for the server side).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type command struct {
	name, alias string
	summary     string
	timeout     time.Duration // 0 means run until interrupted
	run         func(ctx context.Context, a *app, args []string) error
}

// commands is the whole CLI surface, in the order usage lists it.
var commands = []command{
	{name: "ui", alias: "tui", summary: "Open the interactive dashboard (default)", run: cmdTUI},
	{name: "daemon", summary: "Start the Hive runtime in the foreground", run: cmdDaemon},
	{name: "status", summary: "Show runtime status", timeout: 5 * time.Second, run: cmdStatus},
	{name: "ping", summary: "Check that the runtime answers", timeout: 5 * time.Second, run: cmdPing},
	{name: "stop", summary: "Stop the runtime and every agent it runs", timeout: 15 * time.Second, run: cmdStop},
	{name: "environment", alias: "env", summary: "Manage environments: list | create | get | rm", timeout: 15 * time.Second, run: cmdEnvironment},
	{name: "process", alias: "ps", summary: "Manage processes: start | list | get | logs | stop", timeout: 10 * time.Second, run: cmdProcess},
	{name: "terminal", summary: "Agent terminals: attach | resize | input", run: cmdTerminal},
	{name: "demo", summary: "Launch four demo agents to explore the dashboard", timeout: 10 * time.Second, run: cmdDemo},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hive:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		args = []string{"ui"}
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	}
	cmd, ok := lookup(args[0])
	if !ok {
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}

	a, err := newApp()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if cmd.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, cmd.timeout)
		defer cancel()
	}
	return cmd.run(ctx, a, args[1:])
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name || (c.alias != "" && c.alias == name) {
			return c, true
		}
	}
	return command{}, false
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: hive [command] [args]\n\nCommands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-12s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w, "\nStart the runtime first with `hive daemon`. Data lives in ~/.hive (override with HIVE_HOME).")
}
