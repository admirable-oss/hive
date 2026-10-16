//go:build !windows

package client_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/runtime"
)

// imageDaemon runs a daemon that saves pasted images, and returns its
// socket.
func imageDaemon(t testing.TB) string {
	t.Helper()
	path := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: path, Images: runtime.ImagesConfig{Keep: time.Hour}})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := mod.Service.Stop(ctx); err != nil {
			t.Errorf("stop daemon: %v", err)
		}
	})
	return path
}

// startImageAgent runs a stand-in for Claude Code (HIVE_AGENT pins its
// manifest, which takes images as paths): it writes the line it reads to
// out.
func startImageAgent(t testing.TB, c client.Client, out string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := c.EnvironmentCreate(ctx, environment.CreateRequest{ID: "img"}); err != nil {
		t.Fatal(err)
	}
	p, err := c.ProcessStart(ctx, process.StartRequest{
		EnvironmentID: "img", Command: "sh", Args: []string{"-c", `while IFS= read -r p; do printf '%s\n' "$p" >> "$OUT"; done`},
		Env: map[string]string{"HIVE_AGENT": "claude", "OUT": out}, Terminal: true, Width: 200, Height: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// pasteImage sends data, waiting for the daemon to recognise the agent
// first (until then it takes no images).
func pasteImage(t testing.TB, c client.Client, procID string, data []byte) client.ImageResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for {
		res, err := c.TerminalImage(ctx, client.ImageRequest{ProcessID: procID, PNG: data})
		if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeUnsupported && ctx.Err() == nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
}

// receivedPaths waits until the agent has read n lines.
func receivedPaths(t testing.TB, c client.Client, procID, out string, n int) []string {
	t.Helper()
	if err := c.TerminalInput(context.Background(), procID, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(out)
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		if len(data) > 0 && len(lines) >= n {
			var got []string
			for _, l := range lines {
				got = append(got, string(l))
			}
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent read %q", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestImageWorkflow_Local: a paste on this machine reaches the agent as a
// private file whose path it reads, byte for byte.
func TestImageWorkflow_Local(t *testing.T) {
	sock := imageDaemon(t)
	c := client.NewService(client.Config{SocketPath: sock})
	t.Cleanup(func() { _ = c.Close() })
	out := filepath.Join(t.TempDir(), "agent.out")
	procID := startImageAgent(t, c, out)
	data := testPNG(t, 320, 200, 256<<10)
	start := time.Now()
	res := pasteImage(t, c, procID, data)
	t.Logf("local: %d KiB delivered in %v", len(data)>>10, time.Since(start))
	if res.Delivery != "path" {
		t.Fatalf("result %+v", res)
	}
	if got := receivedPaths(t, c, procID, out, 1); got[0] != res.Path {
		t.Fatalf("the agent read %q, want %q", got[0], res.Path)
	}
	saved, err := os.ReadFile(res.Path)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("saved image differs: %v", err)
	}
}

// slowLink stands in for an SSH link: client writes leave at rate bytes
// per second.
type slowLink struct {
	net.Conn
	rate int
	mu   sync.Mutex
}

func (l *slowLink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	time.Sleep(time.Duration(len(p)) * time.Second / time.Duration(l.rate))
	return l.Conn.Write(p)
}

// TestImageWorkflow_RemoteStaysResponsive: over a remote client's link the
// upload is paced within its budget, and keystrokes sent meanwhile on the
// same connection still arrive promptly.
func TestImageWorkflow_RemoteStaysResponsive(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	const budget = 512 << 10
	sock := imageDaemon(t)
	local := client.NewService(client.Config{SocketPath: sock})
	t.Cleanup(func() { _ = local.Close() })
	out := filepath.Join(t.TempDir(), "agent.out")
	procID := startImageAgent(t, local, out)
	pasteImage(t, local, procID, testPNG(t, 2, 2, 0)) // the agent is recognised
	receivedPaths(t, local, procID, out, 1)

	remote := client.NewService(client.Config{
		Remote: true, ImageBudget: budget,
		Dial: func(ctx context.Context) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
			if err != nil {
				return nil, err
			}
			return &slowLink{Conn: conn, rate: 4 << 20}, nil
		},
	})
	t.Cleanup(func() { _ = remote.Close() })
	if err := remote.TerminalInput(context.Background(), procID, nil); err != nil {
		t.Fatal(err)
	}

	data := testPNG(t, 640, 480, 1<<20)
	done := make(chan client.ImageResult, 1)
	start := time.Now()
	go func() {
		res, err := remote.TerminalImage(context.Background(), client.ImageRequest{ProcessID: procID, PNG: data})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	var rtts []time.Duration
	var res client.ImageResult
wait:
	for {
		select {
		case res = <-done:
			break wait
		case <-time.After(50 * time.Millisecond):
			s := time.Now()
			if err := remote.TerminalInput(context.Background(), procID, nil); err != nil {
				t.Fatal(err)
			}
			rtts = append(rtts, time.Since(s))
		}
	}
	elapsed := time.Since(start)
	slices.Sort(rtts)
	if len(rtts) == 0 {
		t.Fatal("no keystrokes were sent during the upload")
	}
	p50, worst := rtts[len(rtts)/2], rtts[len(rtts)-1]
	t.Logf("remote: %d KiB in %v at a %d KiB/s budget; %d keystrokes meanwhile, p50 %v, max %v", len(data)>>10, elapsed, budget>>10, len(rtts), p50, worst)
	if minimum := time.Duration(len(data)) * time.Second / budget * 8 / 10; elapsed < minimum {
		t.Errorf("upload took %v; the budget allows no less than %v", elapsed, minimum)
	}
	if worst > 500*time.Millisecond {
		t.Errorf("a keystroke took %v during the upload", worst)
	}
	if got := receivedPaths(t, local, procID, out, 2); got[len(got)-1] != res.Path {
		t.Fatalf("the agent read %q, want %q", got, res.Path)
	}
}

// BenchmarkTerminalImage measures a local paste end to end: upload,
// validation, saving and typing the path.
func BenchmarkTerminalImage(b *testing.B) {
	sock := imageDaemon(b)
	c := client.NewService(client.Config{SocketPath: sock})
	b.Cleanup(func() { _ = c.Close() })
	procID := startImageAgent(b, c, filepath.Join(b.TempDir(), "agent.out"))
	data := testPNG(b, 640, 480, 1<<20)
	pasteImage(b, c, procID, data)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.TerminalImage(context.Background(), client.ImageRequest{ProcessID: procID, PNG: data}); err != nil {
			b.Fatal(err)
		}
	}
}
