package main

import (
	"context"

	"github.com/admirable-oss/hive/internal/tui"
)

func cmdTUI(ctx context.Context, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	return tui.Run(c)
}

func cmdDaemon(ctx context.Context, args []string) error {
	daemon()
	return nil
}
