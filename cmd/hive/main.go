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
	run  func(context.Context, []string) error
	desc string
}

var commands map[string]command

func init() {
	commands = map[string]command{
		"ping":   {run: cmdPing, desc: "Ping the running Hive runtime"},
		"status": {run: cmdStatus, desc: "Show runtime status"},
		"stop":        {run: cmdStop, desc: "Stop the running Hive runtime"},
		"environment": {run: cmdEnvironment, desc: "Manage environments"},
		"process":     {run: cmdProcess, desc: "Manage processes"},
		"terminal":    {run: cmdTerminal, desc: "Manage terminal sessions"},
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

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cmd.run(ctx, os.Args[2:]); err != nil {
			fatal(err)
		}
		return
	}

	daemon()
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: hive [command]\n\nCommands:")
	// Print in a fixed order for now
	fmt.Fprintln(os.Stderr, "  ping         Ping the running Hive runtime")
	fmt.Fprintln(os.Stderr, "  status       Show runtime status")
	fmt.Fprintln(os.Stderr, "  stop         Stop the running Hive runtime")
	fmt.Fprintln(os.Stderr, "  environment  Manage environments")
	fmt.Fprintln(os.Stderr, "  process      Manage processes")
	fmt.Fprintln(os.Stderr, "  terminal     Manage terminal sessions")
	fmt.Fprintln(os.Stderr, "\nWith no command, hive starts as a background daemon on $HOME/.hive/hive.sock.")
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

	<-ctx.Done()

	_ = module.Service.Stop(ctx)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "hive:", err)
	os.Exit(1)
}
