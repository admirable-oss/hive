package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/runtime"
	"github.com/admirable-oss/hive/internal/vt"
)

// TestFrames_HyperlinksOnlyForClientsThatAskForThem checks frames/2
// negotiation: a current client gets an agent's OSC 8 links, and a client
// that only speaks frames/1 (an older build) gets the same screen without
// them rather than frames it cannot read.
func TestFrames_HyperlinksOnlyForClientsThatAskForThem(t *testing.T) {
	path := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: path})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = mod.Service.Stop(ctx)
	})
	current := client.NewService(client.Config{SocketPath: path})
	older := client.NewService(client.Config{SocketPath: path, Capabilities: []string{vt.FrameCapability}})
	t.Cleanup(func() { _ = current.Close(); _ = older.Close() })

	ctx := context.Background()
	if _, err := current.EnvironmentCreate(ctx, environment.CreateRequest{ID: "links"}); err != nil {
		t.Fatal(err)
	}
	p, err := current.ProcessStart(ctx, process.StartRequest{
		EnvironmentID: "links", Command: "sh", Terminal: true, Width: 40, Height: 3,
		Args: []string{"-c", `printf '\033]8;;https://example.com\007docs\033]8;;\007 here'; sleep 30`},
	})
	if err != nil {
		t.Fatal(err)
	}

	screen := func(c client.Client) *vt.Screen {
		t.Helper()
		fs, err := c.TerminalFrames(ctx, client.ViewRequest{ProcessID: p.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer fs.Close()
		s := &vt.Screen{}
		deadline := time.Now().Add(5 * time.Second)
		for s.Rows == 0 || s.LineText(0) != "docs here" {
			if time.Now().After(deadline) {
				t.Fatalf("screen never showed the output: %q", s.Text())
			}
			f, err := fs.Next()
			if err != nil {
				t.Fatal(err)
			}
			s.Apply(f)
		}
		return s
	}
	if got := screen(current).Lines[0][0].Link; got != "https://example.com" {
		t.Fatalf("a current client gets the link, got %q", got)
	}
	if got := screen(older).Lines[0][0].Link; got != "" {
		t.Fatalf("a frames/1 client gets no link, got %q", got)
	}
}
