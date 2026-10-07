package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
)

func cmdPing(ctx context.Context, args []string) error {
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

func cmdStatus(ctx context.Context, args []string) error {
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

func cmdStop(ctx context.Context, args []string) error {
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
