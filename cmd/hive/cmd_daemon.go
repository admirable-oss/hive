package main

import (
	"context"
	"fmt"
	"time"

	"github.com/admirable-oss/hive/internal/runtime"
)

// cmdDaemon runs the runtime until Ctrl+C, SIGTERM or `hive stop`.
func cmdDaemon(ctx context.Context, a *app, _ []string) error {
	mod := runtime.NewModule(runtime.Config{SocketPath: a.socket, BaseDir: a.root})
	if err := mod.Service.Start(ctx); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "hive daemon running on %s. Press Ctrl+C to stop.\n", a.socket)

	select {
	case <-ctx.Done():
	case <-mod.Service.Done(): // stopped remotely
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := mod.Service.Stop(stopCtx)
	fmt.Fprintln(a.out, "hive daemon stopped")
	return err
}
