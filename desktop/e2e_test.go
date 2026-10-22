//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
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

// tinyPNG is a 6×3 PNG, as a program would print it (base64).
var tinyPNG = func() string {
	img := image.NewNRGBA(image.Rect(0, 0, 6, 3))
	for x := range 6 {
		img.Set(x, 1, color.NRGBA{0xf2, 0xc6, 0x6d, 0xff})
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return base64.StdEncoding.EncodeToString(b.Bytes())
}()

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
		text("nav [role=status]", "default"), // connected, to the default session
		text("nav", "Demo"),
		text("[role=tab]", "work"),
		until(`document.querySelector('.term-row')`),
	)
	// The shell is up once its prompt is on the screen; then type into it.
	step(30*time.Second,
		until(`[...document.querySelectorAll('.term-row')].some(r => r.textContent.trim() !== '')`),
		chromedp.Focus(chromedp.CSS(`section[data-focused] textarea`)),
		chromedp.SendKeys(chromedp.CSS(`section[data-focused] textarea`), "echo e2e-$((6*7))"),
		chromedp.KeyEvent("\r"),
		text(".term-row", "e2e-42"),
	)
	// Keystroke-to-screen latency: a key typed into the focused pane until
	// the shell's echo of it is on the screen, measured in the page (the
	// app, its Go side, the daemon, the shell and back). This is a browser
	// over loopback, not the native webview; the budget (M18) is 25 ms at
	// the 99th percentile on a developer machine. CI machines are slower
	// and shared (a hosted macOS runner stalls for 100 ms and more now and
	// then), so CI holds the measurement to HIVE_DESKTOP_LATENCY_MS
	// (default 25) and reports it either way.
	budget := 25.0
	if v := os.Getenv("HIVE_DESKTOP_LATENCY_MS"); v != "" {
		if _, err := fmt.Sscan(v, &budget); err != nil {
			t.Fatalf("HIVE_DESKTOP_LATENCY_MS=%q: %v", v, err)
		}
	}
	var lat struct{ P50, P99, Max float64 }
	{
		tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		out, err := chromedp.Run(tctx, chromedp.Evaluate[string](`(async (n) => {
			const ta = document.querySelector('section[data-focused] textarea');
			const scr = document.querySelector('section[data-focused] .term-screen');
			const times = [];
			for (let i = 0; i < n; i++) {
				const before = scr.textContent;
				const t0 = performance.now();
				await new Promise((resolve) => {
					const mo = new MutationObserver(() => { if (scr.textContent !== before) { mo.disconnect(); resolve(); } });
					mo.observe(scr, { subtree: true, childList: true, characterData: true });
					ta.value = 'abcdefghijklmnopqrstuvwxyz'[i % 26];
					ta.dispatchEvent(new InputEvent('input', { bubbles: true }));
					setTimeout(() => { mo.disconnect(); resolve(); }, 2000);
				});
				times.push(performance.now() - t0);
				if (i % 20 === 19) { // keep the line short: erase what was typed
					ta.value = '\u0015'; ta.dispatchEvent(new InputEvent('input', { bubbles: true }));
					await new Promise((r) => setTimeout(r, 100));
				}
			}
			times.sort((a, b) => a - b);
			// Nearest rank: the p-th percentile is the ceil(p·n)-th smallest.
			const pct = (p) => times[Math.max(0, Math.ceil(n * p) - 1)];
			return JSON.stringify({ P50: pct(0.5), P99: pct(0.99), Max: times[n - 1] });
		})(200)`, chromedp.EvalAwaitPromise))
		cancel()
		if err != nil {
			t.Fatalf("latency: %v", err)
		}
		if err := json.Unmarshal([]byte(out), &lat); err != nil {
			t.Fatalf("latency: %v (%s)", err, out)
		}
	}
	t.Logf("keystroke to screen: p50 %.1f ms, p99 %.1f ms, max %.1f ms (budget p99 %.0f ms)", lat.P50, lat.P99, lat.Max, budget)
	if lat.P99 > budget {
		t.Errorf("keystroke-to-screen p99 is %.1f ms, over the %.0f ms budget", lat.P99, budget)
	}
	step(10*time.Second, chromedp.KeyEvent("\r"))

	// An image a program prints shows over its cells.
	step(30*time.Second,
		chromedp.SendKeys(chromedp.CSS(`section[data-focused] textarea`), `printf '\033_Ga=T,f=100,i=7,c=8,r=4;`+tinyPNG+`\033\\'`),
		chromedp.KeyEvent("\r"),
		until(`[...document.querySelectorAll('.term-image')].some(i => i.src.startsWith('data:image/png;base64,') && i.complete && i.naturalWidth === 6)`),
	)
	if shots := os.Getenv("HIVE_DESKTOP_SHOTS"); shots != "" {
		png, err := chromedp.Run(ctx, chromedp.FullScreenshot(90))
		if err == nil {
			_ = os.WriteFile(filepath.Join(shots, "desktop.png"), png, 0o644)
		}
	}
	// Accessibility, in a real browser and both colour schemes: no serious
	// or critical axe violations, colour contrast included. Terminal screens
	// are left out: their colours are the programs'.
	axeSrc, err := os.ReadFile("frontend/node_modules/axe-core/axe.min.js")
	if err != nil {
		t.Fatalf("axe-core (npm ci in frontend): %v", err)
	}
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](string(axeSrc)+";true")); err != nil {
		t.Fatalf("load axe: %v", err)
	}
	for _, scheme := range []string{"dark", "light"} {
		_, err := chromedp.Call(ctx, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
			Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}},
		})
		if err != nil {
			t.Fatal(err)
		}
		step(10*time.Second, until(`document.querySelector('[data-scheme]').dataset.scheme === '`+scheme+`'`))
		found, err := chromedp.Run(ctx, chromedp.Evaluate[string](`axe.run({include: [['#root']], exclude: [['.term-screen']]}, {resultTypes: ['violations']}).then(r =>
			JSON.stringify(r.violations.filter(v => v.impact === 'serious' || v.impact === 'critical')
				.map(v => v.id + ' (' + v.nodes.length + '): ' + v.nodes.slice(0, 3).map(n => n.target.join(' ')).join(', '))))`, chromedp.EvalAwaitPromise))
		if err != nil {
			t.Fatalf("axe: %v", err)
		}
		if found != "[]" {
			t.Errorf("accessibility, %s scheme: %s", scheme, found)
		}
	}

	// Splitting goes through the daemon, and the new pane shows up.
	step(10*time.Second, chromedp.Click(chromedp.CSS(`button[aria-label="Split right"]`)))
	tctx, tcancel := context.WithTimeout(ctx, 30*time.Second)
	err = chromedp.Do(tctx, until(`document.querySelectorAll('section[data-pane]').length === 2`))
	tcancel()
	if err != nil {
		n, _ := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelectorAll('section[data-pane]').length + ' panes; ' + document.body.innerText.slice(-300)`))
		t.Fatalf("no second pane: %v (page: %s)\ndaemon: %s", err, n, hiveRun("pane", "list", "--json"))
	}
	if out := hiveRun("pane", "list", "--json"); strings.Count(out, `"tab_id"`) < 2 {
		t.Fatalf("the daemon has no second pane: %s", out)
	}
}
