//go:build !windows

package web_test

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/web"
)

// TestManyViewersAndASlowOne measures how fast a change reaches twenty
// open dashboards while another viewer has stopped reading altogether: the
// stalled one must not hold the others back.
func TestManyViewersAndASlowOne(t *testing.T) {
	h := newHarness(t)
	for i := range 4 {
		h.agentTab("dev", fmt.Sprintf("busy-%d", i))
	}
	code := h.share(web.ShareRequest{Label: "load", Scope: observer.Scope{All: true}, MaxUses: web.MaxLinkUses})
	const viewers = web.MaxLinkUses - 1
	var streams []*sse
	for range viewers {
		b := h.browser()
		b.signIn(code)
		s := b.stream("/")
		s.until(t, "the dashboard", patchHas("agents", "busy-3"))
		streams = append(streams, s)
	}

	// A viewer whose connection stops reading: it signs in, opens the
	// stream, and never reads a byte.
	slow := h.browser()
	slow.signIn(code)
	u, _ := url.Parse(h.ts.URL)
	cookies := slow.c.Jar.Cookies(u)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(1024)
	fmt.Fprintf(conn, "GET /stream?path=%%2F HTTP/1.1\r\nHost: %s\r\nCookie: %s=%s\r\n\r\n", u.Host, cookies[0].Name, cookies[0].Value)

	var latencies []time.Duration
	for round := range 5 {
		name := fmt.Sprintf("round-%d", round)
		start := time.Now()
		h.agentTab("dev", name)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, s := range streams {
			wg.Go(func() {
				s.until(t, name, patchHas("agents", name))
				mu.Lock()
				latencies = append(latencies, time.Since(start))
				mu.Unlock()
			})
		}
		wg.Wait()
	}
	slices.Sort(latencies)
	p50, p95, worst := latencies[len(latencies)/2], latencies[len(latencies)*95/100], latencies[len(latencies)-1]
	t.Logf("%d viewers, one stalled: a new agent reached them in p50 %s, p95 %s, max %s (including the agent's detection)", viewers, p50, p95, worst)
	if p95 > 3*time.Second {
		t.Fatalf("p95 %s: viewers are held back", p95)
	}
	st := h.srv.Status()
	if st.Viewers < viewers {
		t.Fatalf("viewers = %d", st.Viewers)
	}
	// Health stays answerable under load.
	resp, err := http.Get(h.ts.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(fmt.Sprint(st.Runtimes), web.StateLive) {
		t.Fatalf("runtime: %+v", st.Runtimes)
	}
}
