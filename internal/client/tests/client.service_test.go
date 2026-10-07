package client_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/runtime"
)

func sockPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "hive.sock")
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(path)
		if err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return os.ErrNotExist
}

func TestServicePing(t *testing.T) {
	ctx := context.Background()
	path := sockPath(t)

	mod := runtime.NewModule(runtime.Config{
		SocketPath: path,
		Listener:   runtime.NewNetListenerFactory(),
	})
	if err := mod.Service.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = mod.Service.Stop(stopCtx)
	}()

	if err := waitForSocket(path, 2*time.Second); err != nil {
		t.Fatalf("socket did not appear: %v", err)
	}

	c := client.NewService(client.Config{SocketPath: path})

	ctxPing, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.Ping(ctxPing); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestServiceStatus(t *testing.T) {
	ctx := context.Background()
	path := sockPath(t)

	mod := runtime.NewModule(runtime.Config{
		SocketPath: path,
		Listener:   runtime.NewNetListenerFactory(),
	})
	if err := mod.Service.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = mod.Service.Stop(stopCtx)
	}()

	if err := waitForSocket(path, 2*time.Second); err != nil {
		t.Fatalf("socket did not appear: %v", err)
	}

	c := client.NewService(client.Config{SocketPath: path})

	ctxStatus, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status, err := c.Status(ctxStatus)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Status != "running" {
		t.Fatalf("expected status running, got %q", status.Status)
	}
	if status.Socket != path {
		t.Fatalf("expected socket %q, got %q", path, status.Socket)
	}
	if status.StartedAt.IsZero() {
		t.Fatal("expected StartedAt to be populated")
	}
}

func TestServiceShutdown(t *testing.T) {
	ctx := context.Background()
	path := sockPath(t)

	mod := runtime.NewModule(runtime.Config{
		SocketPath: path,
		Listener:   runtime.NewNetListenerFactory(),
	})
	if err := mod.Service.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}

	if err := waitForSocket(path, 2*time.Second); err != nil {
		t.Fatalf("socket did not appear: %v", err)
	}

	c := client.NewService(client.Config{SocketPath: path})

	ctxShutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Shutdown(ctxShutdown); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
