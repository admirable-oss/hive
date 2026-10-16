package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/admirable-oss/hive/internal/observer"
)

// Handlers for what a browser sets for itself: notifications, mutes and
// follows. None of them reaches Hive; they change only this browser's
// settings on the web observer.

func (s *Server) attention(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	s.touch(grant.Runtimes...)
	p := s.page(sess, grant, "/attention", r.URL.Query())
	render(w, http.StatusOK, attentionPage(p, p.prefs))
}

func (s *Server) machines(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	s.touch(grant.Runtimes...)
	render(w, http.StatusOK, machinesPage(s.page(sess, grant, "/machines", r.URL.Query())))
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	p := s.page(sess, grant, "/settings", r.URL.Query())
	devices, _ := s.store.Subscriptions(sess.ID)
	notice := map[string]string{"saved": "Saved.", "muted": "Muted.", "unmuted": "Unmuted."}[r.URL.Query().Get("done")]
	render(w, http.StatusOK, settingsPage(p, p.prefs, devices, s.notifier != nil, notice))
}

// saveSettings handles the settings forms: preferences, mutes, follows.
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	if !s.sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	prefs := s.store.PrefsOf(sess.ID)
	f := r.PostForm
	back := safeNext(f.Get("back"))
	done := "saved"
	rt, agentID := f.Get("runtime"), f.Get("agent")
	known := func() bool {
		if !slices.Contains(grant.Runtimes, rt) {
			return false
		}
		c := s.conn(rt)
		if c == nil {
			return false
		}
		a, ok := c.model.View().Agents[agentID]
		return ok && grant.Scope.Sees(observer.Target{Environment: a.EnvironmentID, Pane: a.PaneID})
	}
	switch f.Get("action") {
	case "prefs":
		prefs.Blocked, prefs.Done, prefs.Failed = f.Get("blocked") != "", f.Get("done") != "", f.Get("failed") != ""
		prefs.Detail = f.Get("detail")
		prefs.Off = nil
		for _, name := range grant.Runtimes {
			if f.Get("on_"+name) == "" {
				prefs.Off = append(prefs.Off, name)
			}
		}
	case "mute":
		until, ok := muteUntil(f.Get("for"), s.now())
		if !ok || !known() {
			http.Error(w, "nothing to mute", http.StatusBadRequest)
			return
		}
		prefs.Mutes = append(prefs.Mutes, Mute{Runtime: rt, Agent: agentID, Until: until})
		done = "muted"
	case "unmute":
		i, err := strconv.Atoi(f.Get("index"))
		if err != nil || i < 0 || i >= len(prefs.Mutes) {
			http.Error(w, "no such mute", http.StatusBadRequest)
			return
		}
		prefs.Mutes = slices.Delete(prefs.Mutes, i, i+1)
		done = "unmuted"
	case "follow":
		if !known() {
			http.Error(w, "nothing to follow", http.StatusBadRequest)
			return
		}
		if !prefs.Following(rt, agentID) {
			prefs.Follows = append(prefs.Follows, Follow{Runtime: rt, Agent: agentID})
		}
	case "unfollow":
		prefs.Follows = slices.DeleteFunc(prefs.Follows, func(x Follow) bool { return x == Follow{Runtime: rt, Agent: agentID} })
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err := s.store.SetPrefs(sess.ID, prefs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if back == "" {
		back = "/settings?done=" + done
	}
	http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // back is a local path (safeNext)
}

// pushKey tells the page whether notifications are on, and the key to
// subscribe with.
func (s *Server) pushKey(w http.ResponseWriter, _ *http.Request, sess Session, _ Grant) {
	if s.vapid == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	devices, _ := s.store.Subscriptions(sess.ID)
	endpoints := []string{}
	for _, d := range devices {
		endpoints = append(endpoints, d.Endpoint)
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "key": s.vapid.PublicKey(), "endpoints": endpoints})
}

// subscriptionJSON is what PushSubscription.toJSON() gives.
type subscriptionJSON struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	if !s.sameOrigin(r) || s.vapid == nil {
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	var sub subscriptionJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&sub); err != nil {
		http.Error(w, "bad subscription", http.StatusBadRequest)
		return
	}
	x, err := s.store.Subscribe(sess.ID, Subscription{Endpoint: sub.Endpoint, P256dh: sub.Keys.P256dh, Auth: sub.Keys.Auth, Client: r.UserAgent()}, s.cfg.Push.AllowAnyEndpoint)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.audit.Record(observer.AuditEntry{Action: "push_subscribed", Credential: grant.ID, Name: grant.Label, Detail: "device " + x.String(), Client: s.client(r)})
	s.refreshPush()
	writeJSON(w, http.StatusOK, map[string]string{"id": x.ID})
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request, sess Session, grant Grant) {
	if !s.sameOrigin(r) {
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)
	n, err := s.store.Unsubscribe(sess.ID, body.Endpoint)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.audit.Record(observer.AuditEntry{Action: "push_unsubscribed", Credential: grant.ID, Name: grant.Label, Detail: fmt.Sprintf("%d device(s)", n), Client: s.client(r)})
	s.refreshPush()
	writeJSON(w, http.StatusOK, map[string]int{"removed": n})
}

func (s *Server) pushTest(w http.ResponseWriter, r *http.Request, sess Session, _ Grant) {
	if !s.sameOrigin(r) || s.notifier == nil {
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	n, err := s.notifier.sendTest(sess.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"devices": n})
}

// safeNext keeps a local path to return to after signing in or saving; it
// never leads elsewhere.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") || len(next) > 512 {
		return ""
	}
	if strings.HasPrefix(next, "/login") || strings.HasPrefix(next, "/stream") {
		return ""
	}
	return next
}

// manifest is the installed app's description.
func (s *Server) manifest(w http.ResponseWriter, _ *http.Request) {
	icon := func(name, size, purpose string) map[string]string {
		return map[string]string{"src": assetPath(name), "sizes": size, "type": "image/png", "purpose": purpose}
	}
	m := map[string]any{
		"name": "Hive", "short_name": "Hive", "id": "/attention",
		"description":      "Your agents, read-only: what needs you, and what they are doing.",
		"start_url":        "/attention?source=app",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#0d1117",
		"theme_color":      "#161b22",
		"icons": []any{
			icon("icon-192.png", "192x192", "any"),
			icon("icon-512.png", "512x512", "any"),
			icon("icon-maskable-512.png", "512x512", "maskable"),
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(m)
}

// serviceWorker serves the worker with the current asset URLs, so a new
// build replaces the cached shell.
func (s *Server) serviceWorker(w http.ResponseWriter, _ *http.Request) {
	src, err := assetFS.ReadFile("assets/sw.js")
	if err != nil {
		http.Error(w, "missing", http.StatusInternalServerError)
		return
	}
	shell := []string{"/offline"}
	for _, name := range []string{"app.css", "app.js", "theme.js", "icon.svg", "icon-192.png"} {
		shell = append(shell, assetPath(name))
	}
	list, _ := json.Marshal(shell)
	var version strings.Builder
	for _, v := range shell {
		version.WriteString(v)
	}
	body := strings.Replace(string(src), "/*SHELL*/[]", string(list), 1)
	body = strings.Replace(body, "/*VERSION*/\"\"", strconv.Quote(shortHash(version.String())), 1)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write([]byte(body))
}

func (s *Server) offline(w http.ResponseWriter, _ *http.Request) {
	render(w, http.StatusOK, offlinePage())
}
