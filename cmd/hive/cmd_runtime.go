package main

import (
	"context"
	"fmt"
)

func cmdPing(ctx context.Context, a *app, _ []string) error {
	if err := a.client.Ping(ctx); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "pong")
	return nil
}

func cmdStatus(ctx context.Context, a *app, _ []string) error {
	status, err := a.client.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, "Hive Runtime")
	w := a.table()
	fmt.Fprintf(w, "Status\t%s\n", status.Status)
	fmt.Fprintf(w, "Socket\t%s\n", status.Socket)
	if !status.StartedAt.IsZero() {
		fmt.Fprintf(w, "Started\t%s\n", status.StartedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func cmdStop(ctx context.Context, a *app, _ []string) error {
	if err := a.client.Shutdown(ctx); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "stopped")
	return nil
}
