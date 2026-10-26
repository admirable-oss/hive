//go:build !windows

package web_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/device"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/web"
)

// TestPhoneLayout: on a phone the pages fit the screen, every control is
// big enough to touch, the bottom tab bar leads to each section, and the
// agent's screen scrolls sideways inside its own box.
func TestPhoneLayout(t *testing.T) {
	for _, d := range []device.Info{device.IPhone13.Device(), device.Pixel5.Device()} {
		t.Run(d.Name, func(t *testing.T) {
			h := newHarness(t)
			ctx := newBrowser(t)
			h.step(h.devPane.ID)
			h.step(h.devPane.ID) // blocked: something on Attention
			run(ctx, t, 30*time.Second,
				chromedp.Emulate(d),
				chromedp.Navigate(signInLink(h, web.ShareRequest{Label: "phone", Scope: observer.Scope{All: true}, Screens: true})),
				until(`location.pathname === '/attention'`),
				waitText("#attention", "dev-agent"),
				waitText("#attention", "Blocked"),
			)
			check := func(where string) {
				t.Helper()
				overflow := get(ctx, t, chromedp.Evaluate[bool](`document.documentElement.scrollWidth > window.innerWidth + 1`))
				if overflow {
					t.Errorf("%s scrolls sideways at %d px", where, d.Width)
				}
				small := get(ctx, t, chromedp.Evaluate[string](`[...document.querySelectorAll('.tabbar a, .agent-card a, button:not([hidden]), .chip, select')]
					.filter(e => e.offsetParent !== null)
					.filter(e => { const r = e.getBoundingClientRect(); return r.height < 43.5 })
					.map(e => e.outerHTML.slice(0, 80)).join('\n')`))
				if small != "" {
					t.Errorf("%s has touch targets under 44 px:\n%s", where, small)
				}
				if !get(ctx, t, chromedp.Evaluate[bool](`getComputedStyle(document.querySelector('.tabbar')).display !== 'none'`)) {
					t.Errorf("%s has no tab bar on a phone", where)
				}
			}
			check("Attention")
			// The card leads to the agent; its screen scrolls inside its box.
			run(ctx, t, 30*time.Second,
				chromedp.Click(chromedp.CSS(".agent-card a")),
				until(`location.pathname.startsWith('/r/local/p/')`),
				waitText("#screen", "Do you want to proceed"),
				waitText("main", "‹ Attention"),
			)
			check("the pane")
			// A line wider than the phone scrolls inside the box, not the page.
			if !get(ctx, t, chromedp.Evaluate[bool](`(() => { const p = document.querySelector('#screen pre.screen'); p.textContent += '\n' + 'x'.repeat(220); return p.scrollWidth > p.clientWidth && getComputedStyle(p).overflowX === 'auto' && document.documentElement.scrollWidth <= window.innerWidth + 1; })()`)) {
				t.Error("a wide screen does not scroll sideways in its own box")
			}
			// Fit makes the whole width readable without scrolling. The agent
			// keeps drawing, and each update replaces the screen and its
			// tools, so a click can land on a button just replaced: Fit is
			// pressed again until the text is smaller.
			run(ctx, t, 10*time.Second, until(`(() => {
				const p = document.querySelector('#screen pre.screen');
				if (p && parseFloat(getComputedStyle(p).fontSize) < 13) return true;
				document.querySelector('[data-screen="fit"]')?.click();
				return false;
			})()`))
			// Back returns to Attention; the tab bar reaches every section.
			run(ctx, t, 15*time.Second,
				chromedp.Click(chromedp.CSS("a[data-back]")),
				until(`location.pathname === '/attention'`),
				chromedp.Click(chromedp.CSS(`.tabbar a[href="/machines"]`)),
				waitText("#machines", "local"),
			)
			check("Machines")
			run(ctx, t, 15*time.Second, chromedp.Click(chromedp.CSS(`.tabbar a[href="/settings"]`)), waitText("main", "Notifications on this device"))
			check("Settings")
		})
	}
}

// TestInstallableAndOffline: the app has a manifest and a service worker;
// the worker keeps only the shell, and with no network the app shows its
// offline page, which knows nothing about any agent.
func TestInstallableAndOffline(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Get(h.ts.URL + "/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Display  string `json:"display"`
		StartURL string `json:"start_url"`
		Icons    []struct {
			Src, Sizes, Purpose string
		} `json:"icons"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&m)
	_ = resp.Body.Close()
	if m.Display != "standalone" || !strings.HasPrefix(m.StartURL, "/attention") || len(m.Icons) < 3 {
		t.Fatalf("manifest = %+v", m)
	}
	for _, ic := range m.Icons {
		r, err := http.Get(h.ts.URL + ic.Src)
		if err != nil || r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("icon %s: %v %v", ic.Src, err, r)
		}
		_ = r.Body.Close()
	}

	ctx := newBrowser(t)
	run(ctx, t, 30*time.Second,
		chromedp.Emulate(device.IPhone13.Device()),
		chromedp.Navigate(signInLink(h, web.ShareRequest{Label: "app", Scope: observer.Scope{All: true}, Screens: true})),
		waitText("#attention", ""),
		until(`navigator.serviceWorker.controller !== null || (navigator.serviceWorker.ready.then(() => location.reload()), false)`),
		until(`navigator.serviceWorker.controller !== null`),
	)
	// Nothing but the shell is cached.
	cached := get(ctx, t, chromedp.Evaluate[string](`(async () => {
		const out = [];
		for (const k of await caches.keys()) for (const r of await (await caches.open(k)).keys()) out.push(new URL(r.url).pathname);
		return out.sort().join(' ');
	})()`, chromedp.EvalAwaitPromise))
	for _, path := range strings.Fields(cached) {
		if path != "/offline" && !strings.HasPrefix(path, "/assets/") {
			t.Errorf("the worker cached %s", path)
		}
	}
	if !strings.Contains(cached, "/offline") || !strings.Contains(cached, "/assets/app.css") {
		t.Fatalf("cached: %q", cached)
	}

	// The network goes away: pages fall back to the offline page.
	h.ts.CloseClientConnections()
	h.ts.Close()
	run(ctx, t, 20*time.Second,
		chromedp.Navigate(h.ts.URL+"/r/local/p/"+h.devPane.ID),
		waitText("main", "You are offline"),
	)
	if body := get(ctx, t, chromedp.Text(chromedp.CSS("main"))); strings.Contains(body, "dev-agent") || strings.Contains(body, "claude") {
		t.Fatalf("the offline page shows agent data: %s", body)
	}
}

// TestNotificationDeepLinkSignsInFirst: a notification's link opened in a
// browser that is not signed in goes through sign-in and back to the pane.
func TestNotificationDeepLinkSignsInFirst(t *testing.T) {
	h := newHarness(t)
	ctx := newBrowser(t)
	target := "/r/local/p/" + h.devPane.ID + "?from=notification"
	run(ctx, t, 20*time.Second,
		chromedp.Navigate(h.ts.URL+target),
		until(`location.pathname === '/login'`),
	)
	url := get(ctx, t, chromedp.Location())
	if !strings.Contains(url, "next=%2Fr%2Flocal%2Fp%2F") {
		t.Fatalf("sign-in page %s does not keep the target", url)
	}
	code := h.share(web.ShareRequest{Label: "deep", Scope: observer.Scope{All: true}, Screens: true})
	run(ctx, t, 30*time.Second,
		chromedp.SendKeys(chromedp.ID("code"), code),
		chromedp.Submit(chromedp.ID("code")),
		until(`location.pathname === '/r/local/p/`+h.devPane.ID+`'`),
		waitText("#screen", "hello-from-dev-agent"),
		waitText("main", "‹ Attention"),
	)
}
