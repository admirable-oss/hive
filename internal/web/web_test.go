//go:build !windows

package web_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/web"
)

// TestSignInFollowAndRevoke is the acceptance: a browser signs in with a
// one-time link, sees the agents it may, follows live updates, and is
// signed out the moment access is revoked.
func TestSignInFollowAndRevoke(t *testing.T) {
	h := newHarness(t)
	b := h.browser()

	// Without signing in, every page sends you to sign in.
	if code, at, _ := b.get("/"); code != http.StatusOK || !strings.HasPrefix(at, "/login") {
		t.Fatalf("anonymous: %d %s", code, at)
	}
	code := h.share(web.ShareRequest{Label: "watch", Scope: observer.Scope{All: true}, Screens: true})
	if at := b.signIn(code); at.Path != "/attention" {
		t.Fatalf("sign-in ended at %s", at)
	}
	// The link works once.
	other := h.browser()
	if at := other.signIn(code); at.RawQuery != "reason=refused" {
		t.Fatalf("a used link: %s", at)
	}

	status, _, body := b.get("/")
	if status != http.StatusOK {
		t.Fatalf("dashboard: %d", status)
	}
	// The dashboard may render before the runtime connects; the stream
	// brings the agents.
	s := b.stream("/")
	s.until(t, "both agents", patchHas("agents", "ops-agent"))
	_ = body

	// A new agent appears live.
	h.agentTab("dev", "late-agent")
	s.until(t, "the new agent", patchHas("agents", "late-agent"))

	// The pane page shows its screen.
	_, _, page := b.get("/r/local/p/" + h.devPane.ID)
	if !strings.Contains(page, "Screen") {
		t.Fatalf("pane page:\n%s", page)
	}
	ps := b.stream("/r/local/p/" + h.devPane.ID)
	ps.until(t, "the screen", patchHas("screen", "hello-from-dev-agent"))

	// Nothing on any page can drive Hive.
	for _, p := range []string{"/", "/r/local", "/r/local/e/dev", "/r/local/p/" + h.devPane.ID} {
		_, _, html := b.get(p)
		for _, bad := range []string{"<textarea", `method="post" action="/r`, "send", "prompt"} {
			if strings.Contains(strings.ToLower(html), bad) {
				t.Errorf("%s has %q", p, bad)
			}
		}
	}

	// Revoking ends the open streams at once and every later request.
	grants, _, _ := h.srv.Store().Grants()
	if _, ended, err := h.srv.Revoke(grants[0].ID); err != nil || len(ended) != 1 {
		t.Fatalf("revoke: %v %v", ended, err)
	}
	ev := s.until(t, "the end of the stream", func(ev sseEvent) bool { return ev.name == "end" })
	if !strings.Contains(ev.data, "revoked") {
		t.Fatalf("end = %s", ev.data)
	}
	if _, at, _ := b.get("/"); at != "/login?reason=revoked" {
		t.Fatalf("after revoking: %s", at)
	}
}

// TestRestrictedLink: a link for one environment, without screens, sees
// nothing of the other and no screen contents, in pages and streams alike.
func TestRestrictedLink(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	b.signIn(h.share(web.ShareRequest{Label: "dev only", Scope: observer.Scope{Environments: []string{"dev"}}}))

	s := b.stream("/")
	ev := s.until(t, "the dev agent", patchHas("agents", "dev-agent"))
	if strings.Contains(ev.data, "ops") {
		t.Fatalf("the dashboard shows ops: %s", ev.data)
	}
	for _, p := range []string{"/r/local/e/ops", "/r/local/p/" + h.opsPane.ID, "/r/nowhere"} {
		if code, _, html := b.get(p); code != http.StatusNotFound || strings.Contains(html, "ops-agent") {
			t.Errorf("%s: %d", p, code)
		}
	}
	// A stream for a page out of scope carries nothing of it.
	os := b.stream("/r/local/p/" + h.opsPane.ID)
	os.until(t, "the connection state", func(ev sseEvent) bool { return ev.name == "patch" && strings.Contains(ev.data, `"conn"`) })
	select {
	case ev := <-os.events:
		if strings.Contains(ev.data, "ops-agent") || strings.Contains(ev.data, `"screen"`) {
			t.Fatalf("out-of-scope stream sent %s", ev.data)
		}
	default:
	}
	// No screens: the pane page says so, and output history is refused.
	_, _, page := b.get("/r/local/p/" + h.devPane.ID)
	if !strings.Contains(page, "not shared with this link") || strings.Contains(page, "hello-from-dev-agent") {
		t.Fatalf("pane page without screens:\n%s", page)
	}
	if code, _, _ := b.get("/r/local/p/" + h.devPane.ID + "/output?lines=50"); code != http.StatusNotFound {
		t.Fatalf("output without history: %d", code)
	}
}

// TestHistoryIsExplicit: output history is read only when asked, only with
// a link that allows it, and each read is audited.
func TestHistoryIsExplicit(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	b.signIn(h.share(web.ShareRequest{Label: "history", Scope: observer.Scope{All: true}, Screens: true, History: true}))
	b.stream("/").until(t, "agents", patchHas("agents", "dev-agent"))
	var code int
	var frag string
	for range 100 { // the agent may still be drawing
		code, _, frag = b.get("/r/local/p/" + h.devPane.ID + "/output?lines=50")
		if code != http.StatusOK || strings.Contains(frag, "hello-from-dev-agent") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code != http.StatusOK || !strings.Contains(frag, "hello-from-dev-agent") {
		t.Fatalf("output: %d\n%s", code, frag)
	}
	var found bool
	entries, _ := auditOf(h)
	for _, e := range entries {
		found = found || e.Action == "history_read"
	}
	if !found {
		t.Fatalf("history read not audited: %+v", entries)
	}
}

func auditOf(h *harness) ([]observer.AuditEntry, error) {
	a, err := observer.OpenAudit(h.dir)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	return a.Read(0, "")
}

// TestReconnectsAfterTheRuntimeRestarts: the page says it is reconnecting
// while the runtime is gone, then catches up from a fresh snapshot.
func TestReconnectsAfterTheRuntimeRestarts(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	b.signIn(h.share(web.ShareRequest{Label: "restart", Scope: observer.Scope{All: true}}))
	s := b.stream("/")
	s.until(t, "live", patchHas("conn", "conn-live"))
	h.stopDaemon()
	s.until(t, "reconnecting", patchHas("conn", "conn-reconnecting"))
	h.startDaemon()
	if _, err := h.mod.Environments.Create(h.ctx, environment.CreateRequest{ID: "fresh"}); err != nil {
		t.Fatal(err)
	}
	h.agentTab("fresh", "after-restart")
	s.until(t, "live again", patchHas("conn", "conn-live"))
	s.until(t, "the agent made after the restart", patchHas("agents", "after-restart"))
}

// TestHealthAndHeaders: health checks need no sign-in and name nothing;
// every response forbids framing, caching and foreign scripts.
func TestHealthAndHeaders(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Get(h.ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var ready map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&ready)
	if resp.StatusCode != http.StatusOK || ready["runtimes"].(float64) != 1 {
		t.Fatalf("readyz: %d %v", resp.StatusCode, ready)
	}
	for k, want := range map[string]string{
		"Content-Security-Policy": "script-src 'self'",
		"X-Frame-Options":         "DENY",
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
	} {
		if !strings.Contains(resp.Header.Get(k), want) {
			t.Errorf("%s = %q", k, resp.Header.Get(k))
		}
	}
	// A cross-site form post is refused.
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/login", strings.NewReader("code=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r2.Body.Close()
	if r2.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site login: %d", r2.StatusCode)
	}
}
