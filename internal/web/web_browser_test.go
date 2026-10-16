//go:build !windows

package web_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/web"
)

// Browser tests drive a real headless Chrome through the web observer.
// They are skipped where no Chrome is installed, unless
// HIVE_BROWSER_TESTS=require (CI sets it where Chrome is known to be).

func newBrowser(t *testing.T) context.Context {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox, chromedp.WindowSize(1280, 900))
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(alloc)
	t.Cleanup(func() { cancel(); cancelAlloc() })
	// The first run starts the browser; it lives as long as ctx.
	if err := chromedp.Do(ctx); err != nil {
		if os.Getenv("HIVE_BROWSER_TESTS") == "require" {
			t.Fatalf("no browser: %v", err)
		}
		t.Skipf("no browser to test with: %v", err)
	}
	return ctx
}

// run runs steps with a deadline.
func run(ctx context.Context, t *testing.T, d time.Duration, steps ...chromedp.Action[chromedp.Void]) {
	t.Helper()
	tctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	if err := chromedp.Do(tctx, steps...); err != nil {
		html, _ := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('main') ? document.querySelector('main').outerHTML : document.documentElement.outerHTML`))
		t.Fatalf("browser: %v\npage:\n%s", err, html)
	}
}

// get runs one action that returns a value.
func get[T any](ctx context.Context, t *testing.T, a chromedp.Action[T]) T {
	t.Helper()
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	v, err := chromedp.Run(tctx, a)
	if err != nil {
		t.Fatalf("browser: %v", err)
	}
	return v
}

// waitText waits until the element sel contains text.
func waitText(sel, text string) chromedp.Action[chromedp.Void] {
	return until(`(() => { const e = document.querySelector(` + jsString(sel) + `); return !!e && e.textContent.includes(` + jsString(text) + `); })()`)
}

// until waits for a JavaScript condition, across page loads (a poll inside
// the page dies with it).
func until(expr string) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		for {
			ok, err := chromedp.Evaluate[bool](`!!(`+expr+`)`)(ctx, t)
			if err == nil && ok {
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("waiting for %s: %w (last: %w)", expr, ctx.Err(), err)
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
}

func jsString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func signInLink(h *harness, req web.ShareRequest) string {
	return h.ts.URL + "/login#" + h.share(req)
}

// TestBrowserFollowsAndSwitches: sign in from a link, follow an agent
// live, move between runtime, environment and pane pages, and filter.
func TestBrowserFollowsAndSwitches(t *testing.T) {
	h := newHarness(t)
	ctx := newBrowser(t)
	run(ctx, t, 30*time.Second,
		chromedp.Navigate(signInLink(h, web.ShareRequest{Label: "browser", Scope: observer.Scope{All: true}, Screens: true})),
		waitText("#agents", "dev-agent"),
		waitText("#agents", "ops-agent"),
		waitText("#conn", "Live"),
	)
	url := get(ctx, t, chromedp.Location())
	code := get(ctx, t, chromedp.Evaluate[string](`document.cookie`))
	if strings.Contains(url, "hwc_") || strings.Contains(url, "#") {
		t.Fatalf("the code stayed in the URL: %s", url)
	}
	if code != "" {
		t.Fatalf("the session cookie is readable by scripts: %q", code)
	}

	// Filtering: typing narrows the list (and keeps it in the URL).
	run(ctx, t, 15*time.Second,
		chromedp.SendKeys(chromedp.ID("q"), "ops"),
		until(`!document.querySelector('#agents').textContent.includes('dev-agent')`),
	)
	url = get(ctx, t, chromedp.Location())
	if !strings.Contains(url, "q=ops") {
		t.Fatalf("filter not in the URL: %s", url)
	}

	// Switching: runtime → environment → pane, each live.
	run(ctx, t, 30*time.Second,
		chromedp.Navigate(h.ts.URL+"/r/local"),
		waitText("#envs", "dev"),
		chromedp.Click(chromedp.CSS(`#envs a[href="/r/local/e/dev"]`)),
		waitText("#tabs", "dev-agent"),
		chromedp.Click(chromedp.CSS(`#tabs a[href="/r/local/p/`+h.devPane.ID+`"]`)),
		waitText("#screen", "hello-from-dev-agent"),
	)
	// The agent moves on: the page follows without reloading.
	if err := h.mod.Panes.SendText(h.ctx, h.devPane.ID, "\r", false); err != nil {
		t.Fatal(err)
	}
	run(ctx, t, 20*time.Second,
		waitText("#screen", "Osmosing"),
		waitText("#pane-head", "Working"),
	)

	// Keyboard: "/" goes to the search box on the dashboard.
	run(ctx, t, 15*time.Second,
		chromedp.Navigate(h.ts.URL+"/"),
		waitText("#agents", "dev-agent"),
		chromedp.KeyEvent("/"),
	)
	focused := get(ctx, t, chromedp.Evaluate[string](`document.activeElement.id`))
	if focused != "q" {
		t.Fatalf("focus after / = %q", focused)
	}
}

// TestBrowserRevocationAndRestriction: a link for one environment shows
// nothing of the other; revoking it sends the open page to sign-in.
func TestBrowserRevocationAndRestriction(t *testing.T) {
	h := newHarness(t)
	ctx := newBrowser(t)
	run(ctx, t, 30*time.Second,
		chromedp.Navigate(signInLink(h, web.ShareRequest{Label: "dev only", Scope: observer.Scope{Environments: []string{"dev"}}})),
		waitText("#agents", "dev-agent"),
	)
	body := get(ctx, t, chromedp.Text(chromedp.CSS("main")))
	if strings.Contains(body, "ops") {
		t.Fatalf("the dev-only page shows ops:\n%s", body)
	}
	run(ctx, t, 15*time.Second, chromedp.Navigate(h.ts.URL+"/r/local/p/"+h.opsPane.ID))
	status := get(ctx, t, chromedp.Text(chromedp.CSS("h1")))
	if status != "Not found" {
		t.Fatalf("an out-of-scope pane: %q", status)
	}
	run(ctx, t, 15*time.Second, chromedp.Navigate(h.ts.URL+"/r/local/p/"+h.devPane.ID), waitText("#screen", "not shared"))

	grants, _, _ := h.srv.Store().Grants()
	if _, _, err := h.srv.Revoke(grants[0].ID); err != nil {
		t.Fatal(err)
	}
	run(ctx, t, 20*time.Second,
		until(`location.pathname === '/login'`),
		waitText("main", "revoked"),
	)
	url := get(ctx, t, chromedp.Location())
	if !strings.Contains(url, "reason=revoked") {
		t.Fatalf("after revoking: %s", url)
	}
}

// TestBrowserReconnects: the page says when the runtime is gone and shows
// what is new once it is back.
func TestBrowserReconnects(t *testing.T) {
	h := newHarness(t)
	ctx := newBrowser(t)
	run(ctx, t, 30*time.Second,
		chromedp.Navigate(signInLink(h, web.ShareRequest{Label: "reconnect", Scope: observer.Scope{All: true}})),
		waitText("#conn", "Live"),
	)
	h.stopDaemon()
	run(ctx, t, 30*time.Second, waitText("#conn", "Reconnecting"))
	h.startDaemon()
	h.agentTabIn(t, "back", "after-restart")
	run(ctx, t, 60*time.Second,
		waitText("#conn", "Live"),
		waitText("#agents", "after-restart"),
	)
}

// TestBrowserTheme: the theme toggle switches colours and is remembered.
func TestBrowserTheme(t *testing.T) {
	h := newHarness(t)
	ctx := newBrowser(t)
	run(ctx, t, 30*time.Second,
		chromedp.Navigate(signInLink(h, web.ShareRequest{Label: "theme", Scope: observer.Scope{All: true}})),
		waitText("#agents", "dev-agent"),
		chromedp.Click(chromedp.ID("theme")), // auto → light
	)
	before := get(ctx, t, chromedp.Evaluate[string](`getComputedStyle(document.body).backgroundColor`))
	run(ctx, t, 5*time.Second, chromedp.Click(chromedp.ID("theme"))) // light → dark
	after := get(ctx, t, chromedp.Evaluate[string](`getComputedStyle(document.body).backgroundColor`))
	stored := get(ctx, t, chromedp.Evaluate[string](`localStorage.getItem('hive-theme')`))
	if after == before || stored != "dark" {
		t.Fatalf("theme: %s → %s, stored %q", before, after, stored)
	}
	// Nothing but the theme is kept in the browser.
	keys := get(ctx, t, chromedp.Evaluate[string](`Object.keys(localStorage).join(',') + '|' + Object.keys(sessionStorage).join(',')`))
	if keys != "hive-theme|" {
		t.Fatalf("browser storage holds %q", keys)
	}
}
