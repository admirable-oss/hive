package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/admirable-oss/hive/internal/runtime"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	home, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}

	module := runtime.NewModule(runtime.Config{
		SocketPath: filepath.Join(
			home,
			".hive",
			"hive.sock",
		),
		Listener: runtime.NewNetListenerFactory(),
	})

	if err := module.Service.Start(ctx); err != nil {
		fatal(err)
	}

	<-ctx.Done()

	if err := module.Service.Stop(ctx); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "hive:", err)
	os.Exit(1)
}