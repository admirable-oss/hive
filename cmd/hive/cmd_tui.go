package main

import (
	"context"
	"fmt"
	"time"

	"github.com/admirable-oss/hive/internal/tui"
)

func cmdTUI(ctx context.Context, a *app, _ []string) error {
	pingCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := a.client.Ping(pingCtx); err != nil {
		return fmt.Errorf("the runtime is not running; start it with: hive daemon")
	}
	return tui.Run(a.client)
}
