package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	g "maragu.dev/gomponents"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/protocol"
)

// Stream timing.
const (
	// renderEvery coalesces changes: a stream sends at most this often.
	renderEvery = 150 * time.Millisecond
	// pingEvery keeps streams (and proxies) alive and lets the browser
	// tell live from stale.
	pingEvery    = 15 * time.Second
	writeTimeout = 10 * time.Second
	cookieName   = "hive_web"
	// maxStreamsPerSession bounds one browser's open pages.
	maxStreamsPerSession = 12
)

// Handler serves the web observer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/", serveAsset)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.authed(s.logout))
	mux.HandleFunc("GET /session", s.sessionCheck)
	mux.HandleFunc("GET /manifest.webmanifest", s.manifest)
	mux.HandleFunc("GET /sw.js", s.serviceWorker)
	mux.HandleFunc("GET /offline", s.offline)
	mux.HandleFunc("GET /{$}", s.authed(s.dashboard))
	mux.HandleFunc("GET /attention", s.authed(s.attention))
	mux.HandleFunc("GET /machines", s.authed(s.machines))
	mux.HandleFunc("GET /settings", s.authed(s.settings))
	mux.HandleFunc("POST /settings", s.authed(s.saveSettings))
	mux.HandleFunc("GET /push/key", s.authed(s.pushKey))
	mux.HandleFunc("POST /push/subscribe", s.authed(s.pushSubscribe))
	mux.HandleFunc("POST /push/unsubscribe", s.authed(s.pushUnsubscribe))
	mux.HandleFunc("POST /push/test", s.authed(s.pushTest))
	mux.HandleFunc("GET /access", s.authed(s.access))
	mux.HandleFunc("GET /r/{rt}", s.authed(s.runtimePage))
	mux.HandleFunc("GET /r/{rt}/e/{env}", s.authed(s.environmentPage))
	mux.HandleFunc("GET /r/{rt}/p/{pane}", s.authed(s.panePage))
	mux.HandleFunc("GET /r/{rt}/p/{pane}/output", s.authed(s.output))
	mux.HandleFunc("GET /stream", s.authed(s.stream))
	return s.headers(mux)
}

// headers sets what every response needs: nothing is cached but assets,
// nothing is framed, nothing is loaded from elsewhere.
func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), clipboard-read=()")
		h.Set("Cache-Control", "no-store")
		if s.cfg.TLSCert != "" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.cfg.Version})
}

// readyz says whether the server can serve: runtimes configured and its
// state readable. It names no runtime: it needs no sign-in.
func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	st := s.Status()
	live := 0
	for _, r := range st.Runtimes {
		if r.State == StateLive {
			live++
		}
	}
	body := map[string]any{"status": "ready", "runtimes": len(st.Runtimes), "live": live, "viewers": st.Viewers}
	if _, _, err := s.store.Grants(); err != nil {
		body["status"] = "state unreadable"
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	if len(st.Runtimes) == 0 {
		body["status"] = "no runtimes (hive web runtime add)"
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func render(w http.ResponseWriter, code int, n g.Node) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = n.Render(w)
}

// client names a request's sender for the audit log.
func (s *Server) client(r *http.Request) string {
	addr := r.RemoteAddr
	if s.cfg.BehindProxy {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			addr = strings.TrimSpace(strings.Split(f, ",")[0])
		}
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	ua := r.UserAgent()
	if len(ua) > 80 {
		ua = ua[:80]
	}
	return addr + " " + ua
}

func (s *Server) cookieName() string {
	if s.cfg.secure() {
		return "__Host-" + cookieName // Secure, Path=/, no Domain: this host only
	}
	return cookieName
}

// sameOrigin refuses cross-site form posts (with SameSite=Strict cookies,
// a second line of defence).
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	pub, err := url.Parse(s.cfg.publicURL())
	return err == nil && u.Host == pub.Host
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	render(w, http.StatusOK, loginPage(r.URL.Query().Get("reason"), safeNext(r.URL.Query().Get("next"))))
}

// login redeems a link's code for a session cookie.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if !s.limit.allow() {
		http.Error(w, "too many sign-in attempts; wait a moment", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	code := r.PostFormValue("code")
	next := safeNext(r.PostFormValue("next"))
	sess, grant, cookie, err := s.store.Redeem(code, r.UserAgent())
	if err != nil {
		s.audit.Record(observer.AuditEntry{Action: "link_refused", Credential: grant.ID, Name: grant.Label, Detail: refusal(err), Client: s.client(r)})
		to := "/login?reason=refused"
		if next != "" {
			to += "&next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	s.audit.Record(observer.AuditEntry{Action: "signed_in", Credential: grant.ID, Name: grant.Label, Detail: "session " + sess.ID, Client: s.client(r)})
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure whenever browsers reach us over HTTPS; plain HTTP is loopback only
		Name: s.cookieName(), Value: cookie, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, Secure: s.cfg.secure(), SameSite: http.SameSiteStrictMode,
	})
	if next == "" {
		next = "/attention"
	}
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // next is a local path (safeNext)
}

func refusal(err error) string {
	if errors.Is(err, ErrLinkRefused) {
		return "invalid, used or expired link"
	}
	return err.Error()
}

type authedHandler func(w http.ResponseWriter, r *http.Request, sess Session, grant Grant)

// authed lets signed-in browsers through; others go to sign in (pages) or
// get 401 (the stream, fragments).
func (s *Server) authed(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, grant, reason := s.session(r)
		if reason != "" {
			if r.URL.Path == "/stream" || strings.HasSuffix(r.URL.Path, "/output") || r.Method != http.MethodGet {
				// The cookie stays (refused everywhere) so that the page's
				// /session check, and its next load, can still say why:
				// EventSource cannot read this answer.
				writeJSON(w, http.StatusUnauthorized, map[string]string{"reason": reason})
				return
			}
			s.clearCookie(w)
			to := "/login?reason=" + reason
			if next := safeNext(r.URL.RequestURI()); next != "" && next != "/" {
				to += "&next=" + url.QueryEscape(next) // back here after signing in (a notification's link)
			}
			http.Redirect(w, r, to, http.StatusSeeOther) //nolint:gosec // a local path; reason is one of a few words, next is checked
			return
		}
		next(w, r, sess, grant)
	}
}

// session returns the request's session, or why there is none.
func (s *Server) session(r *http.Request) (Session, Grant, string) {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return Session{}, Grant{}, "out"
	}
	sess, grant, err := s.store.Authenticate(c.Value)
	switch {
	case errors.Is(err, ErrGrantRevoked):
		return Session{}, Grant{}, "revoked"
	case errors.Is(err, ErrGrantExpired):
		return Session{}, Grant{}, "expired"
	case err != nil:
		return Session{}, Grant{}, "out"
	}
	return sess, grant, ""
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.secure(), SameSite: http.SameSiteStrictMode}) //nolint:gosec // as at sign-in
}

// sessionCheck tells the page whether it is still signed in (after its
// stream failed).
func (s *Server) sessionCheck(w http.ResponseWriter, r *http.Request) {
	if _, _, reason := s.session(r); reason != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"reason": reason})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed in"})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	if !s.sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	_ = s.store.SignOut(sess.ID)
	s.endSessions([]string{sess.ID}, "out")
	s.audit.Record(observer.AuditEntry{Action: "signed_out", Credential: grant.ID, Name: grant.Label, Detail: "session " + sess.ID, Client: s.client(r)})
	s.clearCookie(w)
	// The installed app's cache and this browser's storage go too.
	w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	s.refreshPush() // this browser's devices went with its session
	http.Redirect(w, r, "/login?reason=out", http.StatusSeeOther)
}

// page builds what a page shows, connecting its runtimes if needed.
func (s *Server) page(sess Session, grant Grant, path string, q url.Values) *page {
	p := &page{grant: grant, session: sess, now: s.now(), path: path, query: q, prefs: s.store.PrefsOf(sess.ID)}
	for _, rt := range s.allowed(grant) {
		c := s.conn(rt.Name)
		if c == nil {
			continue
		}
		p.runtimes = append(p.runtimes, &shown{rt: rt, v: c.model.View(), c: c})
	}
	return p
}

// touch starts connecting a runtime a page shows; it stays a minute.
func (s *Server) touch(names ...string) {
	for _, n := range names {
		if c := s.conn(n); c != nil {
			c.Acquire()()
		}
	}
}

// loadWait is how long a page about one runtime waits for it to connect:
// a notification's link usually arrives while nobody was looking.
const loadWait = 4 * time.Second

// waitLoaded connects runtime name and waits briefly until it is known.
func (s *Server) waitLoaded(r *http.Request, name string) {
	c := s.conn(name)
	if c == nil {
		return
	}
	release := c.Acquire() //nolint:contextcheck // the connection outlives this request
	defer release()
	if c.model.View().Loaded() {
		return
	}
	ch, unsub := c.Subscribe()
	defer unsub()
	t := time.NewTimer(loadWait)
	defer t.Stop()
	for !c.model.View().Loaded() {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			return
		case <-ch:
		}
	}
}

func renderString(n g.Node) string {
	var b bytes.Buffer
	_ = n.Render(&b)
	return b.String()
}

func (s *Server) notFound(w http.ResponseWriter, p *page, what string) {
	render(w, http.StatusNotFound, messagePage(p, "Not found", what+" does not exist, or this link cannot see it."))
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	s.touch(grant.Runtimes...)
	render(w, http.StatusOK, dashboardPage(s.page(sess, grant, "/", r.URL.Query())))
}

func (s *Server) access(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	render(w, http.StatusOK, accessPage(s.page(sess, grant, "/access", r.URL.Query())))
}

func (s *Server) runtimePage(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	p := s.page(sess, grant, r.URL.Path, r.URL.Query())
	rt := p.runtime(r.PathValue("rt"))
	if rt == nil {
		s.notFound(w, p, "That runtime")
		return
	}
	s.touch(rt.rt.Name)
	render(w, http.StatusOK, runtimePage(p, rt))
}

func (s *Server) environmentPage(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	if slicesContains(grant.Runtimes, r.PathValue("rt")) {
		s.waitLoaded(r, r.PathValue("rt"))
	}
	p := s.page(sess, grant, r.URL.Path, r.URL.Query())
	rt, env := p.runtime(r.PathValue("rt")), r.PathValue("env")
	if rt == nil || !grant.Scope.Environment(env) || (rt.v.Loaded() && !hasEnv(rt, env)) {
		s.notFound(w, p, "That environment")
		return
	}
	s.touch(rt.rt.Name)
	render(w, http.StatusOK, environmentPage(p, rt, env))
}

func hasEnv(r *shown, env string) bool { _, ok := r.v.Envs[env]; return ok }

// paneVisible reports whether pane is in a loaded runtime and in scope.
func paneVisible(p *page, r *shown, pane string) bool {
	pn, ok := r.v.Panes[pane]
	return ok && p.sees(observer.Target{Environment: pn.EnvironmentID, Pane: pane})
}

func (s *Server) panePage(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	if slicesContains(grant.Runtimes, r.PathValue("rt")) {
		s.waitLoaded(r, r.PathValue("rt"))
	}
	p := s.page(sess, grant, r.URL.Path, r.URL.Query())
	rt, pane := p.runtime(r.PathValue("rt")), r.PathValue("pane")
	if rt == nil || !rt.v.Loaded() {
		if rt != nil {
			s.touch(rt.rt.Name) //nolint:contextcheck // the connection outlives this request
			render(w, http.StatusOK, messagePage(p, "Connecting", "The runtime is not connected yet. Reload this page in a moment."))
			return
		}
		s.notFound(w, p, "That pane")
		return
	}
	if !paneVisible(p, rt, pane) {
		s.notFound(w, p, "That pane")
		return
	}
	s.touch(rt.rt.Name) //nolint:contextcheck // the connection outlives this request
	var scr screenState
	if p.screens(rt) {
		if cl := s.conn(rt.rt.Name).current(); cl != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			got, err := cl.Screen(ctx, observer.ScreenRequest{Pane: pane})
			cancel()
			if err == nil {
				scr = screenState{screen: got, have: true}
			} else if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodePermissionDenied {
				scr = screenState{err: pe.Message}
			}
		}
	}
	var out *outputState
	if n, err := strconv.Atoi(r.URL.Query().Get("output")); err == nil && n > 0 && p.history(rt) {
		o := s.readOutput(r, grant, rt, pane, n)
		out = &o
	}
	render(w, http.StatusOK, panePage(p, rt, pane, scr, out))
}

// output is a pane's output history, read only when a viewer asks, as a
// fragment.
func (s *Server) output(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	p := s.page(sess, grant, r.URL.Path, r.URL.Query())
	rt, pane := p.runtime(r.PathValue("rt")), r.PathValue("pane")
	if rt == nil || !paneVisible(p, rt, pane) || !p.history(rt) {
		http.Error(w, "not available", http.StatusNotFound)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	out := s.readOutput(r, grant, rt, pane, n)
	render(w, http.StatusOK, outputFragment(&out))
}

func (s *Server) readOutput(r *http.Request, grant Grant, rt *shown, pane string, n int) outputState {
	if n <= 0 || n > observer.MaxOutputLines {
		n = historyLines
	}
	cl := s.conn(rt.rt.Name).current()
	if cl == nil {
		return outputState{err: errOffline.Error()}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	res, err := cl.Output(ctx, observer.OutputRequest{Pane: pane, Lines: n})
	if err != nil {
		return outputState{err: err.Error()}
	}
	s.audit.Record(observer.AuditEntry{Action: "history_read", Credential: grant.ID, Name: grant.Label, Detail: fmt.Sprintf("%s pane %s, %d lines", rt.rt.Name, pane, n), Client: s.client(r)})
	return outputState{lines: res.Lines}
}

// --- the live stream ---

// stream is one open page's event stream.
type stream struct {
	session string
	ended   chan string
	once    sync.Once
}

func (st *stream) end(reason string) {
	st.once.Do(func() { st.ended <- reason })
}

// view is what a stream renders: a page's path, parsed.
type streamView struct {
	kind    string // dashboard, access, runtime, environment, pane
	runtime string
	env     string
	pane    string
	query   url.Values
	path    string
}

func parseView(raw string) (streamView, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Path, "/") {
		return streamView{}, false
	}
	v := streamView{query: u.Query(), path: u.Path}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case u.Path == "/":
		v.kind = "dashboard"
	case u.Path == "/access":
		v.kind = "access"
	case u.Path == "/attention":
		v.kind = "attention"
	case u.Path == "/machines":
		v.kind = "machines"
	case u.Path == "/settings":
		v.kind = "settings"
	case len(parts) == 2 && parts[0] == "r":
		v.kind, v.runtime = "runtime", parts[1]
	case len(parts) == 4 && parts[0] == "r" && parts[2] == "e":
		v.kind, v.runtime, v.env = "environment", parts[1], parts[3]
	case len(parts) == 4 && parts[0] == "r" && parts[2] == "p":
		v.kind, v.runtime, v.pane = "pane", parts[1], parts[3]
	default:
		return streamView{}, false
	}
	for _, p := range []*string{&v.env, &v.pane} {
		if s, err := url.PathUnescape(*p); err == nil {
			*p = s
		}
	}
	return v, true
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	view, ok := parseView(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "unknown page", http.StatusBadRequest)
		return
	}
	names := grant.Runtimes
	if view.runtime != "" {
		if !slicesContains(grant.Runtimes, view.runtime) || s.conn(view.runtime) == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		names = []string{view.runtime}
	}
	st := &stream{session: sess.ID, ended: make(chan string, 1)}
	s.mu.Lock()
	n := 0
	for o := range s.streams {
		if o.session == sess.ID {
			n++
		}
	}
	if n >= maxStreamsPerSession {
		s.mu.Unlock()
		http.Error(w, "too many open pages", http.StatusTooManyRequests)
		return
	}
	s.streams[st] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.streams, st)
		s.mu.Unlock()
	}()

	// Follow the runtimes shown, and the screen when it is a pane page.
	changes := make(chan struct{}, 1)
	forward := func(ch <-chan struct{}, stop <-chan struct{}) {
		for {
			select {
			case <-stop:
				return
			case <-ch:
				select {
				case changes <- struct{}{}:
				default:
				}
			}
		}
	}
	stop := make(chan struct{})
	defer close(stop)
	var cleanups []func()
	defer func() {
		for _, f := range cleanups {
			f()
		}
	}()
	for _, name := range names {
		c := s.conn(name)
		if c == nil {
			continue
		}
		release := c.Acquire() //nolint:contextcheck // the connection outlives this request
		ch, unsub := c.Subscribe()
		cleanups = append(cleanups, release, unsub)
		go forward(ch, stop)
	}
	var watch *paneWatch
	if view.kind == "pane" {
		p := s.page(sess, grant, view.path, view.query)
		if rt := p.runtime(view.runtime); rt != nil && p.screens(rt) {
			w, ch, unwatch := s.conn(view.runtime).WatchScreen(view.pane) //nolint:contextcheck // shared by every viewer of the pane
			cleanups = append(cleanups, unwatch)
			watch = w
			go forward(ch, stop)
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	send := func(event string, data any) error {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
			return err
		}
		return rc.Flush()
	}
	sent := map[string]string{}
	renderAll := func() error { //nolint:contextcheck // insights are read for every viewer of a scope, and kept
		p := s.page(sess, grant, view.path, view.query)
		for _, n := range s.fragments(view, p, watch) {
			var b bytes.Buffer
			if err := n.Render(&b); err != nil {
				return err
			}
			html := b.String()
			id, v := fragmentMeta(html)
			if id == "" || sent[id] == html {
				continue
			}
			sent[id] = html
			if err := send("patch", map[string]any{"id": id, "v": v, "html": html}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := renderAll(); err != nil {
		return
	}
	expires := time.NewTimer(time.Until(grant.ExpiresAt))
	defer expires.Stop()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	var (
		pending  bool
		last     = time.Now()
		coalesce = time.NewTimer(time.Hour)
	)
	coalesce.Stop()
	defer coalesce.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case reason := <-st.ended:
			_ = send("end", map[string]string{"reason": reason})
			return
		case <-expires.C:
			_ = send("end", map[string]string{"reason": "expired"})
			return
		case <-changes:
			if !pending {
				pending = true
				coalesce.Reset(max(0, renderEvery-time.Since(last)))
			}
		case <-coalesce.C:
			pending, last = false, time.Now()
			if renderAll() != nil {
				return
			}
		case <-ping.C:
			if _, _, reason := s.session(r); reason != "" {
				_ = send("end", map[string]string{"reason": reason})
				return
			}
			if send("ping", map[string]int64{"t": time.Now().UnixMilli()}) != nil {
				return
			}
			if renderAll() != nil { // times and staleness move on
				return
			}
		}
	}
}

func slicesContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// fragments are the parts of a view that change live.
func (s *Server) fragments(v streamView, p *page, watch *paneWatch) []g.Node {
	out := []g.Node{connFragment(p), bannerFragment(p)}
	switch v.kind {
	case "dashboard":
		out = append(out, dashboardFragments(p)...)
	case "attention":
		out = append(out, attentionFragments(p, p.prefs)...)
	case "machines":
		out = append(out, machinesFragments(p)...)
	case "runtime":
		if rt := p.runtime(v.runtime); rt != nil {
			out = append(out, runtimeFragments(p, rt)...)
		}
	case "environment":
		if rt := p.runtime(v.runtime); rt != nil && p.grant.Scope.Environment(v.env) {
			out = append(out, environmentFragments(p, rt, v.env)...)
		}
	case "pane":
		rt := p.runtime(v.runtime)
		if rt == nil || (rt.v.Loaded() && !paneVisible(p, rt, v.pane)) {
			break
		}
		var scr screenState
		if watch != nil {
			screen, version, err, have := watch.Screen()
			scr = screenState{screen: screen, have: have, err: err, version: version}
		}
		frags := paneFragments(p, rt, v.pane, scr)
		if watch != nil && !scr.have && (scr.err == "" || scr.err == errOffline.Error()) {
			// Still loading: keep the screen the page came with.
			frags = slices.DeleteFunc(frags, func(n g.Node) bool { id, _ := fragmentMeta(renderString(n)); return id == "screen" })
		}
		out = append(out, frags...)
	}
	return out
}

var (
	fragmentID      = regexp.MustCompile(`^<div id="([^"]+)"`)
	fragmentVersion = regexp.MustCompile(`^<div id="[^"]+"[^>]*? data-v="(\d+)"`)
)

// fragmentMeta reads a rendered fragment's ID and version.
func fragmentMeta(html string) (string, uint64) {
	m := fragmentID.FindStringSubmatch(html)
	if m == nil {
		return "", 0
	}
	var v uint64
	if vm := fragmentVersion.FindStringSubmatch(html); vm != nil {
		v, _ = strconv.ParseUint(vm[1], 10, 64)
	}
	return m[1], v
}
