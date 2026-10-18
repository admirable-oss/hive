//go:build !windows

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// The end-to-end test runs the app as a server build (-tags server: the
// same app and services, served to a browser) against a real daemon, and
// drives it with headless Chrome. It needs the frontend built
// (frontend/dist) and Chrome; it is skipped without them, unless
// HIVE_DESKTOP_E2E=require (CI sets it).

func requireE2E(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("HIVE_DESKTOP_E2E") == "require" {
		t.Fatal(why)
	}
	t.Skip(why)
}

func build(t *testing.T, dir, out string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"build", "-o", out}, args...)...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %v: %v\n%s", args, err, b)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func until(expr string) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		var last error
		for {
			ok, err := chromedp.Evaluate[bool](`!!(`+expr+`)`)(ctx, t)
			if err == nil && ok {
				return nil
			}
			last = err
			select {
			case <-ctx.Done():
				return fmt.Errorf("waiting for %s: %w (last: %w)", expr, ctx.Err(), last)
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
}

func text(sel, want string) chromedp.Action[chromedp.Void] {
	q := func(s string) string { return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'" }
	return until(`[...document.querySelectorAll(` + q(sel) + `)].some(e => e.textContent.includes(` + q(want) + `))`)
}

// TestDesktopEndToEnd: the app connects to the daemon, lists its
// environments and agents' tabs, shows a pane's live screen, and types
// into it; it opens a tab and splits a pane through the daemon.
func TestDesktopEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	if _, err := os.Stat("frontend/dist/index.html"); err != nil {
		requireE2E(t, "the frontend is not built (cd frontend && npm run build)")
	}
	bin := t.TempDir()
	hive, app := filepath.Join(bin, "hive"), filepath.Join(bin, "app")
	build(t, "..", hive, "./cmd/hive")
	build(t, ".", app, "-tags", "server,production", ".")

	home, err := os.MkdirTemp("", "hde")
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "HIVE_HOME="+home, "HIVE_CONFIG="+filepath.Join(home, "config.toml"), "HIVE_BIN="+hive, "HIVE_SOCKET_PATH=", "HIVE_SESSION=")
	hiveRun := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(hive, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hive %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	t.Cleanup(func() {
		cmd := exec.Command(hive, "stop")
		cmd.Env = env
		_ = cmd.Run()
		_ = os.RemoveAll(home)
	})
	project := t.TempDir()
	hiveRun("env", "create", "demo", "--cwd", project)
	hiveRun("tab", "create", "demo", "--name", "work")

	port := freePort(t)
	srv := exec.Command(app)
	srv.Env = append(slices.Clone(env), fmt.Sprintf("HIVE_DESKTOP_TEST_PORT=%d", port))
	logs, _ := os.Create(filepath.Join(bin, "app.log"))
	srv.Stdout, srv.Stderr = logs, logs
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(bin, "app.log"))
			t.Logf("app log:\n%s", b)
		}
	})
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the app does not serve: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox, chromedp.WindowSize(1280, 800))
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(alloc)
	defer cancel()
	if err := chromedp.Do(ctx); err != nil {
		requireE2E(t, fmt.Sprintf("no browser: %v", err))
	}
	step := func(d time.Duration, steps ...chromedp.Action[chromedp.Void]) {
		t.Helper()
		tctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		if err := chromedp.Do(tctx, steps...); err != nil {
			html, _ := chromedp.Run(ctx, chromedp.Evaluate[string](`document.body.outerHTML`))
			t.Fatalf("browser: %v\npage:\n%s", err, html)
		}
	}

	step(45*time.Second,
		chromedp.Navigate(url),
		text(".conn", "default"), // connected, to the default session
		text(".sidebar", "Demo"),
		text(".tab", "work"),
		until(`document.querySelector('.term-row')`),
	)
	// The shell is up once its prompt is on the screen; then type into it.
	step(30*time.Second,
		until(`[...document.querySelectorAll('.term-row')].some(r => r.textContent.trim() !== '')`),
		chromedp.Focus(chromedp.CSS(`.pane.is-focused .term-input`)),
		chromedp.SendKeys(chromedp.CSS(`.pane.is-focused .term-input`), "echo e2e-$((6*7))"),
		chromedp.KeyEvent("\r"),
		text(".term-row", "e2e-42"),
	)
	if shots := os.Getenv("HIVE_DESKTOP_SHOTS"); shots != "" {
		png, err := chromedp.Run(ctx, chromedp.FullScreenshot(90))
		if err == nil {
			_ = os.WriteFile(filepath.Join(shots, "desktop.png"), png, 0o644)
		}
	}
	// Splitting goes through the daemon, and the new pane shows up.
	step(10*time.Second, chromedp.Click(chromedp.CSS(`button[aria-label="Split right"]`)))
	tctx, tcancel := context.WithTimeout(ctx, 30*time.Second)
	err = chromedp.Do(tctx, until(`document.querySelectorAll('.pane').length === 2`))
	tcancel()
	if err != nil {
		n, _ := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelectorAll('.pane').length + ' panes; ' + document.querySelector('.toasts').textContent`))
		t.Fatalf("no second pane: %v (page: %s)\ndaemon: %s", err, n, hiveRun("pane", "list", "--json"))
	}
	if out := hiveRun("pane", "list", "--json"); strings.Count(out, `"tab_id"`) < 2 {
		t.Fatalf("the daemon has no second pane: %s", out)
	}
}
