//go:build !windows

package web_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/web"
)

// pushService is a fake browser push service: it checks each message's
// VAPID signature, decrypts it with the device's key, and passes it on.
type pushService struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	got map[string]chan web.Notification // device path → messages
	ua  map[string]*ecdh.PrivateKey
	key map[string][]byte
}

func newPushService(t *testing.T) *pushService {
	p := &pushService{t: t, got: map[string]chan web.Notification{}, ua: map[string]*ecdh.PrivateKey{}, key: map[string][]byte{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pushService) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/gone" {
		w.WriteHeader(http.StatusGone)
		return
	}
	if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") == "" {
		p.t.Errorf("headers %v", r.Header)
	}
	checkVAPID(p.t, r.Header.Get("Authorization"), p.srv.URL)
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	ua, auth, ch := p.ua[r.URL.Path], p.key[r.URL.Path], p.got[r.URL.Path]
	p.mu.Unlock()
	if ua == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var n web.Notification
	if err := json.Unmarshal(open(p.t, body, ua, auth), &n); err != nil {
		p.t.Errorf("payload: %v", err)
	}
	ch <- n
	w.WriteHeader(http.StatusCreated)
}

// device makes a subscription the way a browser would.
func (p *pushService) device(path string) (map[string]any, chan web.Notification) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	ch := make(chan web.Notification, 32)
	p.mu.Lock()
	p.ua[path], p.key[path], p.got[path] = ua, auth, ch
	p.mu.Unlock()
	enc := base64.RawURLEncoding
	return map[string]any{"endpoint": p.srv.URL + path, "keys": map[string]string{"p256dh": enc.EncodeToString(ua.PublicKey().Bytes()), "auth": enc.EncodeToString(auth)}}, ch
}

func open(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt, idlen := body[:16], int(body[20])
	asPublic, ciphertext := body[21:21+idlen], body[21+idlen:]
	as, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := ua.ECDH(as)
	prkKey, _ := hkdf.Extract(sha256.New, shared, auth)
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPublic...)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return bytes.TrimRight(plain[:len(plain)-1], "\x00")
}

func checkVAPID(t *testing.T, header, aud string) {
	rest, _ := strings.CutPrefix(header, "vapid t=")
	jwt, k, _ := strings.Cut(rest, ", k=")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Errorf("authorization %q", header)
		return
	}
	enc := base64.RawURLEncoding
	claims, _ := enc.DecodeString(parts[1])
	if !strings.Contains(string(claims), `"aud":"`+aud+`"`) {
		t.Errorf("claims %s, want aud %s", claims, aud)
	}
	pub, _ := enc.DecodeString(k)
	pk, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	sig, _ := enc.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err != nil || len(sig) != 64 || !ecdsa.Verify(pk, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the VAPID signature does not verify")
	}
}

// post sends JSON or a form as the page does.
func (b *browser) post(path string, body any) int {
	b.h.t.Helper()
	var req *http.Request
	switch v := body.(type) {
	case url.Values:
		req, _ = http.NewRequestWithContext(b.h.ctx, http.MethodPost, b.h.ts.URL+path, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	default:
		raw, _ := json.Marshal(v)
		req, _ = http.NewRequestWithContext(b.h.ctx, http.MethodPost, b.h.ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", b.h.ts.URL)
	resp, err := b.c.Do(req)
	if err != nil {
		b.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func next(t *testing.T, ch chan web.Notification, what string) web.Notification {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(20 * time.Second):
		t.Fatalf("no notification: %s", what)
		return web.Notification{}
	}
}

func none(t *testing.T, ch chan web.Notification, d time.Duration, what string) {
	t.Helper()
	select {
	case n := <-ch:
		t.Fatalf("%s, but got %+v", what, n)
	case <-time.After(d):
	}
}

// step moves a fake agent to its next screen: working, blocked, done.
func (h *harness) step(paneID string) {
	h.t.Helper()
	if err := h.mod.Panes.SendText(h.ctx, paneID, "\r", false); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) waitPushReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !h.srv.PushStatus().Runtimes["local"] {
		if time.Now().After(deadline) {
			t.Fatalf("notifications never got ready: %+v", h.srv.PushStatus())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestNotifications: a device hears once about each agent that blocks or
// finishes where its link can see, with a deep link and no screen
// contents; mutes, private detail and revocation are honoured.
func TestNotifications(t *testing.T) {
	h := newHarness(t)
	push := newPushService(t)
	b := h.browser()
	b.signIn(h.share(web.ShareRequest{Label: "phone", Scope: observer.Scope{All: true}}))
	sub, phone := push.device("/phone")
	if code := b.post("/push/subscribe", sub); code != http.StatusOK {
		t.Fatalf("subscribe: %d", code)
	}
	// A device on a dev-only link hears nothing about ops.
	narrow := h.browser()
	narrow.signIn(h.share(web.ShareRequest{Label: "dev only", Scope: observer.Scope{Environments: []string{"dev"}}}))
	nsub, devOnly := push.device("/dev-only")
	narrow.post("/push/subscribe", nsub)
	h.waitPushReady(t)

	h.step(h.devPane.ID) // working: no notification
	h.step(h.devPane.ID) // blocked
	n := next(t, phone, "blocked")
	if n.Kind != web.KindBlocked || !strings.Contains(n.Title, "dev-agent") || n.URL != "/r/local/p/"+h.devPane.ID+"?from=notification" {
		t.Fatalf("blocked notification = %+v", n)
	}
	raw, _ := json.Marshal(n)
	if strings.Contains(string(raw), "Do you want to proceed") || strings.Contains(string(raw), "Bash command") {
		t.Fatalf("a notification carries screen contents: %s", raw)
	}
	if got := next(t, devOnly, "the dev-only device"); got.Kind != web.KindBlocked {
		t.Fatalf("dev-only device: %+v", got)
	}
	none(t, phone, time.Second, "one transition notifies once")
	h.step(h.devPane.ID) // done
	if n := next(t, phone, "done"); n.Kind != web.KindDone {
		t.Fatalf("done notification = %+v", n)
	}
	next(t, devOnly, "done on the dev-only device")

	// Mute the ops agent, and ask for private notifications.
	var opsAgent string
	for id, a := range h.srvAgents() {
		if a.PaneID == h.opsPane.ID {
			opsAgent = id
		}
	}
	if code := b.post("/settings", url.Values{"action": {"mute"}, "runtime": {"local"}, "agent": {opsAgent}, "for": {"1h"}}); code != http.StatusOK {
		t.Fatalf("mute: %d", code)
	}
	h.step(h.opsPane.ID)
	h.step(h.opsPane.ID) // ops blocked: muted for phone, out of scope for dev-only
	none(t, phone, 2*time.Second, "a muted agent notified")
	none(t, devOnly, 0, "an out-of-scope agent notified")
	b.post("/settings", url.Values{"action": {"prefs"}, "blocked": {"1"}, "done": {"1"}, "detail": {"private"}, "on_local": {"1"}})
	h.step(h.opsPane.ID) // ops done, still muted
	none(t, phone, time.Second, "a muted agent notified")
	late := h.agentTab("dev", "late-agent")
	h.step(late.ID)
	h.step(late.ID)
	if n := next(t, phone, "private blocked"); n.Title != "Hive" || strings.Contains(n.Body, "late-agent") || strings.Contains(n.URL, "/p/") {
		t.Fatalf("private notification = %+v", n)
	}

	// A device the push service says is gone is forgotten.
	gone, _ := push.device("/gone")
	b.post("/push/subscribe", gone)
	h.step(late.ID) // done
	next(t, phone, "late done")
	deadline := time.Now().Add(10 * time.Second)
	for {
		devices, _ := h.srv.Store().Subscriptions("")
		if !strings.Contains(fmtEndpoints(devices), "/gone") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a gone device was kept")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Revoking the link removes its devices; nothing more is sent.
	grants, _, _ := h.srv.Store().Grants()
	for _, g := range grants {
		if g.Label == "phone" {
			if _, _, err := h.srv.Revoke(g.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if devices, _ := h.srv.Store().Subscriptions(""); strings.Contains(fmtEndpoints(devices), "/phone") {
		t.Fatal("a revoked link's device is kept")
	}
	other := h.agentTab("dev", "after-revoke")
	h.step(other.ID)
	h.step(other.ID)
	next(t, devOnly, "the dev-only device still hears")
	none(t, phone, time.Second, "a revoked device was notified")
}

func fmtEndpoints(devices []web.Subscription) string {
	var b strings.Builder
	for _, d := range devices {
		b.WriteString(d.Endpoint + " ")
	}
	return b.String()
}

func (h *harness) srvAgents() map[string]observer.Agent {
	out := map[string]observer.Agent{}
	list, _ := h.mod.Agents.List(h.ctx, false)
	for _, a := range list {
		out[a.ID] = observer.Agent{ID: a.ID, PaneID: a.PaneID, EnvironmentID: a.EnvironmentID}
	}
	return out
}

// TestUnsubscribeNeedsItsEndpoint: a body that cannot be read removes no
// device (an empty endpoint would remove them all), and one endpoint
// removes only its device.
func TestUnsubscribeNeedsItsEndpoint(t *testing.T) {
	h := newHarness(t)
	push := newPushService(t)
	b := h.browser()
	b.signIn(h.share(web.ShareRequest{Label: "phone", Scope: observer.Scope{All: true}}))
	one, _ := push.device("/one")
	two, _ := push.device("/two")
	b.post("/push/subscribe", one)
	b.post("/push/subscribe", two)
	if code := b.post("/push/unsubscribe", "not an object"); code != http.StatusBadRequest {
		t.Fatalf("a bad body: %d", code)
	}
	if devices, _ := h.srv.Store().Subscriptions(""); len(devices) != 2 {
		t.Fatalf("a bad body removed devices: %s", fmtEndpoints(devices))
	}
	if code := b.post("/push/unsubscribe", map[string]any{"endpoint": one["endpoint"]}); code != http.StatusOK {
		t.Fatalf("unsubscribe: %d", code)
	}
	if devices, _ := h.srv.Store().Subscriptions(""); len(devices) != 1 || !strings.Contains(fmtEndpoints(devices), "/two") {
		t.Fatalf("after unsubscribing one: %s", fmtEndpoints(devices))
	}
}
