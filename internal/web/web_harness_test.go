//go:build !windows

package web_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/runtime"
	"github.com/admirable-oss/hive/internal/web"
)

// fakeClaude draws Claude Code's screens, one more on each line of input:
// idle, working, a permission dialog, idle again.
const fakeClaude = `#!/bin/sh
bottom='\033[999;1H'
printf "\033[H\033[2J ▐▛███▛█   Claude Code\r\nhello-from-NAME${bottom}\033[2A❯ ${bottom}  ? for shortcuts"
read x
printf "\033[H\033[2J ▐▛███▛█   Claude Code\r\n✻ Osmosing…${bottom}\033[2A❯ ${bottom}  esc to interrupt"
read x
printf '\033[H\033[2J Bash command\r\n Do you want to proceed?\r\n ❯ 1. Yes\r\n   2. No\r\n Esc to cancel · Tab to amend'
read x
printf "\033[H\033[2J ▐▛███▛█   Claude Code\r\n⏺ Done.${bottom}\033[2A❯ ${bottom}  ? for shortcuts"
read x
`

// harness is a daemon with two environments, an agent in each, and a web
// observer allowed to show it all.
type harness struct {
	t        *testing.T
	ctx      context.Context
	sock     string
	mod      *runtime.Module
	control  client.Client
	srv      *web.Server
	ts       *httptest.Server
	dir      string
	devPane  pane.Pane
	opsPane  pane.Pane
	bin      string
	credID   string
	modStops []func()
}

func shortTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	h := &harness{t: t, ctx: ctx, sock: filepath.Join(shortTemp(t), "hive.sock"), bin: t.TempDir()}
	h.startDaemon()
	for _, env := range []string{"dev", "ops"} {
		if _, err := h.mod.Environments.Create(ctx, environment.CreateRequest{ID: env}); err != nil {
			t.Fatal(err)
		}
	}
	h.devPane = h.agentTab("dev", "dev-agent")
	h.opsPane = h.agentTab("ops", "ops-agent")

	var created runtime.ObserverCreated
	if err := h.control.Call(ctx, "observer.create", runtime.ObserverCreateParams{Name: "hive-web:local", Scope: observer.Scope{All: true}, Screens: true}, &created); err != nil {
		t.Fatal(err)
	}
	h.credID = created.Credential.ID
	h.dir = t.TempDir()
	store := web.NewStore(h.dir)
	tokenFile, err := store.WriteToken("local", created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRuntime(web.Runtime{
		Name: "local", Socket: created.Socket, Credential: created.Credential.ID, TokenFile: tokenFile,
		Scope: created.Credential.Scope, Screens: true, ExpiresAt: created.Credential.ExpiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	h.srv, err = web.New(web.Config{Dir: h.dir, Listen: "127.0.0.1:0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h.ts = httptest.NewServer(h.srv.Handler())
	t.Cleanup(h.ts.Close)
	return h
}

func (h *harness) startDaemon() {
	h.t.Helper()
	mod := runtime.NewModule(runtime.Config{SocketPath: h.sock, Shell: []string{"sh"}})
	if err := mod.Service.Start(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.mod = mod
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = mod.Service.StopWithAgents(context.Background())
		}
	}
	h.modStops = append(h.modStops, stop)
	h.t.Cleanup(stop)
	h.control = client.NewService(client.Config{SocketPath: h.sock})
	h.t.Cleanup(func() { _ = h.control.Close() })
}

// stopDaemon stops the daemon, its agents with it.
func (h *harness) stopDaemon() {
	h.modStops[len(h.modStops)-1]()
}

// agentTabIn makes environment env and an agent tab in it.
func (h *harness) agentTabIn(t *testing.T, env, name string) pane.Pane {
	t.Helper()
	if _, err := h.mod.Environments.Create(h.ctx, environment.CreateRequest{ID: env}); err != nil {
		t.Fatal(err)
	}
	return h.agentTab(env, name)
}

// agentTab opens a tab in env running a fake Claude that says name.
func (h *harness) agentTab(env, name string) pane.Pane {
	h.t.Helper()
	script := filepath.Join(h.bin, name, "claude")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(fakeClaude, "NAME", name)), 0o755); err != nil {
		h.t.Fatal(err)
	}
	_, p, err := h.mod.Panes.CreateTab(h.ctx, pane.CreateTabRequest{EnvironmentID: env, Name: name, Pane: pane.Spec{Name: name, Command: []string{script}}})
	if err != nil {
		h.t.Fatal(err)
	}
	h.waitAgent(p.ID)
	return p
}

func (h *harness) waitAgent(paneID string) {
	h.t.Helper()
	for {
		list, _ := h.mod.Agents.List(h.ctx, false)
		for _, a := range list {
			if a.PaneID == paneID {
				return
			}
		}
		if h.ctx.Err() != nil {
			h.t.Fatal("the agent was never detected")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (h *harness) share(req web.ShareRequest) string {
	h.t.Helper()
	if req.Runtimes == nil {
		req.Runtimes = []string{"local"}
	}
	_, link, err := h.srv.Share(req)
	if err != nil {
		h.t.Fatal(err)
	}
	u, _ := url.Parse(link)
	return u.Fragment // the code
}

// browser is a cookie-keeping HTTP client against the server.
type browser struct {
	h *harness
	c *http.Client
}

func (h *harness) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{h: h, c: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

// signIn redeems code as the login page does, and returns where it ended.
func (b *browser) signIn(code string) *url.URL {
	b.h.t.Helper()
	req, _ := http.NewRequestWithContext(b.h.ctx, http.MethodPost, b.h.ts.URL+"/login", strings.NewReader(url.Values{"code": {code}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.h.ts.URL)
	resp, err := b.c.Do(req)
	if err != nil {
		b.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.Request.URL
}

// get returns a page's status, final URL and body.
func (b *browser) get(path string) (int, string, string) {
	b.h.t.Helper()
	req, _ := http.NewRequestWithContext(b.h.ctx, http.MethodGet, b.h.ts.URL+path, nil)
	resp, err := b.c.Do(req)
	if err != nil {
		b.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Request.URL.Path + "?" + resp.Request.URL.RawQuery, string(body)
}

// sse is an open event stream.
type sse struct {
	resp   *http.Response
	events chan sseEvent
}

type sseEvent struct {
	name string
	data string
}

func (b *browser) stream(path string) *sse {
	b.h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	b.h.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.h.ts.URL+"/stream?path="+url.QueryEscape(path), nil)
	c := *b.c
	c.Timeout = 0
	resp, err := c.Do(req) //nolint:bodyclose // closed when the test ends
	if err != nil {
		b.h.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b.h.t.Fatalf("stream %s: %s", path, resp.Status)
	}
	s := &sse{resp: resp, events: make(chan sseEvent, 1024)}
	go func() {
		defer close(s.events)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				s.events <- ev
				ev = sseEvent{}
			}
		}
	}()
	b.h.t.Cleanup(func() { _ = resp.Body.Close() })
	return s
}

// patch decodes a patch event.
type patch struct {
	ID   string `json:"id"`
	V    uint64 `json:"v"`
	HTML string `json:"html"`
}

// until reads events until ok says so; every event read is passed to seen.
func (s *sse) until(t *testing.T, what string, ok func(sseEvent) bool) sseEvent {
	t.Helper()
	deadline := time.After(20 * time.Second)
	var seen []string
	for {
		select {
		case ev, open := <-s.events:
			if !open {
				t.Fatalf("the stream ended before %s; last events:\n%s", what, strings.Join(seen, "\n"))
			}
			if ok(ev) {
				return ev
			}
			seen = append(seen, ev.name+" "+ev.data)
			if len(seen) > 6 {
				seen = seen[1:]
			}
		case <-deadline:
			t.Fatalf("no %s; last events:\n%s", what, strings.Join(seen, "\n"))
		}
	}
}

func patchHas(id, text string) func(sseEvent) bool {
	return func(ev sseEvent) bool {
		if ev.name != "patch" {
			return false
		}
		var p patch
		return json.Unmarshal([]byte(ev.data), &p) == nil && p.ID == id && strings.Contains(p.HTML, text)
	}
}
