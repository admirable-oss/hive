package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/runtime"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "ping":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := cmdPing(ctx); err != nil {
				fatal(err)
			}
			return
		case "status":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := cmdStatus(ctx); err != nil {
				fatal(err)
			}
			return
		case "stop":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := cmdStop(ctx); err != nil {
				fatal(err)
			}
			return
		case "-h", "--help", "help":
			usage()
			return
		default:
			fmt.Fprintf(os.Stderr, "hive: unknown command %q\n\n", os.Args[1])
			usage()
			os.Exit(2)
		}
	}

	daemon()
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: hive [command]

Commands:
  ping      Ping the running Hive runtime
  status    Show runtime status
  stop      Stop the running Hive runtime

With no command, hive starts as a background daemon on $HOME/.hive/hive.sock.`)
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

func cmdPing(ctx context.Context) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	if err := c.Ping(ctx); err != nil {
		return err
	}
	fmt.Println("pong")
	return nil
}

func cmdStatus(ctx context.Context) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Println("Hive Runtime")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintf(w, "Status\t%s\n", status.Status)
	fmt.Fprintf(w, "Socket\t%s\n", status.Socket)
	if !status.StartedAt.IsZero() {
		fmt.Fprintf(w, "Started\t%s\n", status.StartedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func cmdStop(ctx context.Context) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	if err := c.Shutdown(ctx); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
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
