package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/runtime"
)

type command struct {
	run     func(context.Context, []string) error
	desc    string
	noLimit bool // skip the 5-second timeout
}

var commands map[string]command

func init() {
	commands = map[string]command{
		"daemon":      {run: cmdDaemon, desc: "Start the Hive runtime daemon (foreground)", noLimit: true},
		"ping":        {run: cmdPing, desc: "Ping the running Hive runtime"},
		"status":      {run: cmdStatus, desc: "Show runtime status"},
		"stop":        {run: cmdStop, desc: "Stop the running Hive runtime"},
		"environment": {run: cmdEnvironment, desc: "Manage environments"},
		"process":     {run: cmdProcess, desc: "Manage processes"},
		"terminal":    {run: cmdTerminal, desc: "Manage terminal sessions", noLimit: true},
		"ui":          {run: cmdTUI, desc: "Open interactive TUI dashboard", noLimit: true},
		"tui":         {run: cmdTUI, desc: "Open interactive TUI dashboard", noLimit: true},
	}
}

func main() {
	if len(os.Args) >= 2 {
		cmdName := os.Args[1]
		if cmdName == "-h" || cmdName == "--help" || cmdName == "help" {
			usage()
			return
		}

		cmd, ok := commands[cmdName]
		if !ok {
			fmt.Fprintf(os.Stderr, "hive: unknown command %q\n\n", cmdName)
			usage()
			os.Exit(2)
		}

		var ctx context.Context
		var cancel context.CancelFunc
		if cmd.noLimit {
			ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		} else {
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		}
		defer cancel()

		if err := cmd.run(ctx, os.Args[2:]); err != nil {
			fatal(err)
		}
		return
	}

	// No args: open TUI.
	// If the daemon isn't running, tell the user how to start it.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	c, err := newClient()
	if err != nil {
		fatal(err)
	}

	// Quick connectivity check.
	pingCtx, pingCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer pingCancel()
	if err := c.Ping(pingCtx); err != nil {
		fmt.Fprintln(os.Stderr, "hive: runtime is not running.")
		fmt.Fprintln(os.Stderr, "      start it with: hive daemon")
		os.Exit(1)
	}

	if err := cmdTUI(ctx, nil); err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: hive [command]\n\nCommands:")
	fmt.Fprintln(os.Stderr, "  daemon       Start the Hive runtime daemon (foreground)")
	fmt.Fprintln(os.Stderr, "  ui           Open the interactive TUI dashboard")
	fmt.Fprintln(os.Stderr, "  ping         Ping the running Hive runtime")
	fmt.Fprintln(os.Stderr, "  status       Show runtime status")
	fmt.Fprintln(os.Stderr, "  stop         Stop the running Hive runtime")
	fmt.Fprintln(os.Stderr, "  environment  Manage environments")
	fmt.Fprintln(os.Stderr, "  process      Manage processes")
	fmt.Fprintln(os.Stderr, "  terminal     Manage terminal sessions")
	fmt.Fprintln(os.Stderr, "\nWith no command, hive opens the interactive TUI.")
	fmt.Fprintln(os.Stderr, "Run the daemon first:  hive daemon")
}

func socketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hive", "hive.sock"), nil
}

func newClient() (client.Client, error) {
	path, err := socketPath()
	if err != nil {
		return nil, err
	}
	mod := client.NewModule(client.Config{SocketPath: path})
	return mod.Client, nil
}

// daemon starts the Hive runtime and blocks until interrupted.
func daemon() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	path, err := socketPath()
	if err != nil {
		fatal(err)
	}

	module := runtime.NewModule(runtime.Config{
		SocketPath: path,
		Listener:   runtime.NewNetListenerFactory(),
	})

	if err := module.Service.Start(ctx); err != nil {
		fatal(err)
	}

	fmt.Println("hive daemon running. Press Ctrl+C to stop.")
	<-ctx.Done()

	_ = module.Service.Stop(context.Background())
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "hive:", err)
	os.Exit(1)
}
