//go:build !windows

package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── manifests ───────────────────────────────────────────────────────────

const apiOne = `
api = 1
id = "surface"
version = "1.0.0"
isolation = "strict"
event_concurrency = 1

[permissions]
required = ["hive.read", "hive.write"]
optional = ["network"]
reasons = { network = "fetch CI results" }

[[actions]]
id = "open"
title = "Open it"
command = ["true"]

[[actions]]
id = "bg"
command = ["sleep", "30"]
async = true

[[contributes.cards]]
id = "ci"
title = "CI"

[[contributes.cards]]
id = "agent-ci"
title = "Agent CI"
scope = "agent"

[[contributes.status]]
id = "build"
title = "Build"

[[contributes.actions]]
action = "open"
contexts = ["pane", "agent"]

[[contributes.palette]]
action = "open"

[[contributes.links]]
id = "docs"
title = "Docs"
url = "https://example.com/docs"
`

func TestManifestAPI1(t *testing.T) {
	m, err := ParseManifest([]byte(apiOne))
	if err != nil {
		t.Fatal(err)
	}
	if m.Legacy() || m.EffectiveAPI() != 1 || m.Contributes.Cards[0].Scope != "environment" || m.Contributes.Actions[0].Title != "Open it" {
		t.Fatalf("manifest = %+v", m)
	}
	if got := m.Declared(); strings.Join(got, ",") != "hive.read,hive.write,network" {
		t.Fatalf("declared = %v", got)
	}
	preview := m.Preview("")
	for _, want := range []string{"plugin API: 1", "requires hive.write", "may ask for network", "fetch CI results", "sidebar card \"CI\"", "palette entry", "can run isolated"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, preview)
		}
	}

	legacy, err := ParseManifest([]byte("id = \"old\"\nversion = \"1.0.0\"\n[[actions]]\nid = \"a\"\ncommand = [\"true\"]\n"))
	if err != nil || !legacy.Legacy() || len(legacy.RequiredCapabilities()) != len(CapabilityList) {
		t.Fatalf("legacy = %+v, %v", legacy, err)
	}
}

func TestManifestAPIErrors(t *testing.T) {
	base := "id = \"x\"\nversion = \"1.0.0\"\n[[actions]]\nid = \"open\"\ncommand = [\"true\"]\n"
	cases := map[string]string{
		"api = 9\n" + base + "future_key = 1\n":                                                                  "needs plugin API 9",
		base + "[permissions]\nrequired = [\"hive.read\"]\n":                                                     "needs api = 1",
		"api = 1\n" + base + "[permissions]\nrequired = [\"root\"]\n":                                            "unknown capability",
		"api = 1\n" + base + "[permissions]\nrequired = [\"network\"]\noptional = [\"network\"]\n":               "listed twice",
		"api = 1\n" + base + "[permissions]\nreasons = { network = \"x\" }\n":                                    "is not requested",
		"api = 1\n" + base + "[[contributes.actions]]\naction = \"nope\"\n":                                      "no action",
		"api = 1\n" + base + "[[contributes.actions]]\naction = \"open\"\ncontexts = [\"window\"]\n":             "context",
		"api = 1\n" + base + "[[contributes.cards]]\nid = \"c\"\ntitle = \"C\"\nscope = \"tab\"\n":               "scope",
		"api = 1\n" + base + "[[contributes.links]]\nid = \"l\"\ntitle = \"L\"\nurl = \"file:///etc/passwd\"\n":  "http or https",
		"api = 1\n" + base + "[[contributes.links]]\nid = \"l\"\ntitle = \"L\"\nurl = \"javascript:alert(1)\"\n": "http or https",
		"api = 1\n" + base + "[[contributes.status]]\nid = \"s\"\ntitle = \"\"\n":                                "title",
		"api = 1\n" + base + "isolation = \"loose\"\n":                                                           "isolation",
		"api = 1\n" + base + "event_concurrency = 99\n":                                                          "event_concurrency",
	}
	for src, want := range cases {
		if _, err := ParseManifest([]byte(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
	}
	if _, err := ParseManifest([]byte("api = 9\n" + base)); !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatalf("unsupported API: %v", err)
	}
}

// ─── permissions ─────────────────────────────────────────────────────────

func TestDecide(t *testing.T) {
	m, _ := ParseManifest([]byte(apiOne))
	cases := []struct {
		granted []string
		policy  Policy
		eff     string
		problem string
	}{
		{granted: []string{"hive.read", "hive.write"}, eff: "hive.read,hive.write"},
		{granted: []string{"hive.read", "hive.write", "network"}, eff: "hive.read,hive.network"[:0] + "hive.read,hive.write,network"},
		{granted: []string{"hive.read", "hive.write"}, policy: Policy{Allow: []string{"network"}}, eff: "hive.read,hive.write,network"},
		{granted: []string{"hive.read"}, eff: "hive.read", problem: "not granted"},
		{granted: []string{"hive.read", "hive.write"}, policy: Policy{Deny: []string{"hive.write"}}, eff: "hive.read", problem: "denied"},
		{granted: []string{"hive.read", "hive.write", "network"}, policy: Policy{Deny: []string{"network"}}, eff: "hive.read,hive.write"},
	}
	for i, tc := range cases {
		g := Decide(m, tc.granted, tc.policy)
		if strings.Join(g.Effective, ",") != tc.eff || !strings.Contains(g.Problem("surface"), tc.problem) || (tc.problem == "") != (g.Problem("surface") == "") {
			t.Errorf("case %d: %+v problem %q", i, g, g.Problem("surface"))
		}
	}
	legacy, _ := ParseManifest([]byte("id = \"old\"\nversion = \"1.0.0\"\n[[actions]]\nid = \"a\"\ncommand = [\"true\"]\n"))
	if g := Decide(legacy, nil, Policy{}); len(g.Effective) != len(CapabilityList) || g.Problem("old") != "" {
		t.Fatalf("legacy grant = %+v", g)
	}
	if g := Decide(legacy, nil, Policy{Deny: []string{"network"}}); !strings.Contains(g.Problem("old"), "denied") {
		t.Fatalf("legacy under deny = %+v", g)
	}
}

func TestComparePermissions(t *testing.T) {
	old, _ := ParseManifest([]byte(apiOne))
	next, _ := ParseManifest([]byte(strings.Replace(strings.Replace(apiOne, `required = ["hive.read", "hive.write"]`, `required = ["hive.read", "network", "hive.input"]`, 1), `optional = ["network"]`, `optional = ["process.exec"]`, 1)))
	c := ComparePermissions(old, next)
	if strings.Join(c.AddedRequired, ",") != "hive.input" || strings.Join(c.BecameRequired, ",") != "network" ||
		strings.Join(c.AddedOptional, ",") != "process.exec" || strings.Join(c.Removed, ",") != "hive.write" || c.Empty() {
		t.Fatalf("change = %+v", c)
	}
	if !strings.Contains(c.String(), "+ hive.input (new, required)") {
		t.Fatalf("change text:\n%s", c)
	}
	if got := Approve(next, []string{"hive.write", "process.exec"}); strings.Join(got, ",") != "hive.input,hive.read,network,process.exec" {
		t.Fatalf("approve = %v", got)
	}
	if !ComparePermissions(old, old).Empty() {
		t.Fatal("no change reported as a change")
	}
}

// TestEveryMethodIsClassified: a method plugins could call without a
// capability decision must not exist. A new daemon method fails here
// until it is classified in methodCapabilities.
func TestEveryMethodIsClassified(t *testing.T) {
	f, err := os.Open("../../api/v1/api.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	seen := map[string]bool{}
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), " params:")
		if !ok || rest == "" || strings.HasPrefix(name, "type ") || strings.Contains(name, " ") {
			continue
		}
		seen[name] = true
		if _, ok := MethodCapability(name); !ok {
			t.Errorf("method %s has no plugin capability (classify it in methodCapabilities)", name)
		}
	}
	for _, m := range ClassifiedMethods() {
		if !seen[m] {
			t.Errorf("classified method %s does not exist", m)
		}
	}
	if Allowed("process.start", []string{CapHiveRead}) || !Allowed("process.start", []string{CapHiveProcess}) || !Allowed("plugin.card", nil) || Allowed("no.such", AllCapabilities()) {
		t.Fatal("Allowed disagrees with the table")
	}
}

// ─── host ────────────────────────────────────────────────────────────────

type published struct {
	mu     sync.Mutex
	events []string
}

func (p *published) publish(typ string, _ any) {
	p.mu.Lock()
	p.events = append(p.events, typ)
	p.mu.Unlock()
}

func (p *published) count(typ string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.events {
		if e == typ {
			n++
		}
	}
	return n
}

// startHost links manifest (with files) and runs a host over it.
func startHost(t *testing.T, manifest string, files map[string]string, cfg HostConfig) (*Host, *Store, *published) {
	t.Helper()
	dir := writePlugin(t, manifest, files, false)
	s := NewStore(filepath.Join(t.TempDir(), "plugins"))
	if _, _, err := s.Link(context.Background(), dir, "dev"); err != nil {
		t.Fatal(err)
	}
	pub := &published{}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	cfg.Store, cfg.Publish = s, pub.publish
	h := NewHost(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the host to load", func() bool { return len(h.Status()) > 0 })
	return h, s, pub
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func statusOf(h *Host, id string) Status {
	for _, s := range h.Status() {
		if s.ID == id {
			return s
		}
	}
	return Status{}
}

func TestHostHealthReadinessAndCrashLoop(t *testing.T) {
	m := "api = 1\nid = \"crashy\"\nversion = \"1.0.0\"\n[[startup]]\ncommand = [\"sh\", \"-c\", \"exit 4\"]\nrestart = true\n"
	h, _, pub := startHost(t, m, map[string]string{}, HostConfig{RestartLimit: 1})
	waitFor(t, "the crash loop to fail the plugin", func() bool { return statusOf(h, "crashy").Health.State == StateFailed })
	st := statusOf(h, "crashy")
	if st.Health.Crashes != 2 || !strings.Contains(st.LastError, "no longer restarted") || st.Running != 0 {
		t.Fatalf("status = %+v", st.Health)
	}
	if pub.count(EventChanged) == 0 {
		t.Fatal("no plugin.changed published")
	}
	if _, err := h.Action(context.Background(), "crashy", "x", Context{}); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("action on a failed plugin: %v", err)
	}
	// A reload starts it afresh.
	if st, err := h.ReloadPlugin("crashy"); err != nil || st.Health.State == StateFailed || st.Health.Crashes != 0 {
		t.Fatalf("reload = %+v, %v", st.Health, err)
	}
}

func TestHostReadyBySignalAndUptime(t *testing.T) {
	m := "api = 1\nid = \"svc\"\nversion = \"1.0.0\"\n[[startup]]\ncommand = [\"sleep\", \"30\"]\n[[actions]]\nid = \"a\"\ncommand = [\"true\"]\n"
	h, _, _ := startHost(t, m, map[string]string{}, HostConfig{})
	if st := statusOf(h, "svc"); st.Health.State != StateStarting {
		t.Fatalf("state = %s, want starting", st.Health.State)
	}
	if err := h.Ready("svc"); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(h, "svc"); st.Health.State != StateReady || st.Health.ReadyAt.IsZero() {
		t.Fatalf("after ready: %+v", st.Health)
	}

	m2 := "api = 1\nid = \"svc2\"\nversion = \"1.0.0\"\n[[startup]]\ncommand = [\"sleep\", \"30\"]\n"
	h2, _, _ := startHost(t, m2, map[string]string{}, HostConfig{})
	waitFor(t, "uptime readiness", func() bool { return statusOf(h2, "svc2").Health.State == StateReady })
}

func TestHostEventsInOrderAndDroppedWhenSlow(t *testing.T) {
	m := `api = 1
id = "ordered"
version = "1.0.0"
event_concurrency = 1
[[events]]
on = "tab.created"
command = ["./ev.sh"]
`
	h, s, _ := startHost(t, m, map[string]string{
		"ev.sh": "#!/bin/sh\ngrep -o '\"seq\":[0-9]*' \"$HIVE_PLUGIN_CONTEXT\" | cut -d: -f2 >> \"$HIVE_PLUGIN_DATA/seen\"\n",
	}, HostConfig{})
	for i := 1; i <= 20; i++ {
		h.EventAt("tab.created", uint64(i), time.Now(), json.RawMessage(`{}`))
	}
	seen := filepath.Join(s.DataDir("ordered"), "seen")
	waitFor(t, "20 events", func() bool { b, _ := os.ReadFile(seen); return strings.Count(string(b), "\n") >= 20 })
	b, _ := os.ReadFile(seen)
	lines := strings.Fields(string(b))
	for i, l := range lines {
		if l != strconv.Itoa(i+1) {
			t.Fatalf("events out of order: %v", lines)
		}
	}
	// The 20th line lands just before its delivery is counted.
	waitFor(t, "20 deliveries counted", func() bool { return statusOf(h, "ordered").Health.Events.Delivered == 20 })
	if st := statusOf(h, "ordered"); st.Health.Events.Delivered != 20 || st.Health.Events.Dropped != 0 {
		t.Fatalf("events = %+v", st.Health.Events)
	}

	// A slow plugin's queue fills: events are dropped, never waited for,
	// and the next one it gets says it missed some.
	slow := `api = 1
id = "slow"
version = "1.0.0"
event_concurrency = 1
[[events]]
on = "tab.created"
command = ["./ev.sh"]
`
	h2, s2, _ := startHost(t, slow, map[string]string{
		"ev.sh": "#!/bin/sh\ncat \"$HIVE_PLUGIN_CONTEXT\" >> \"$HIVE_PLUGIN_DATA/ctx\"; echo >> \"$HIVE_PLUGIN_DATA/ctx\"; [ -f \"$HIVE_PLUGIN_DATA/go\" ] || sleep 0.5\n",
	}, HostConfig{})
	start := time.Now()
	for i := 1; i <= eventQueue+50; i++ {
		h2.EventAt("tab.created", uint64(i), time.Now(), json.RawMessage(`{}`))
	}
	if time.Since(start) > time.Second {
		t.Fatalf("queueing %d events took %v: the host waited for a slow plugin", eventQueue+50, time.Since(start))
	}
	st := statusOf(h2, "slow")
	if st.Health.Events.Dropped == 0 || st.Health.State != StateDegraded {
		t.Fatalf("slow plugin: %+v", st.Health)
	}
	if err := os.MkdirAll(s2.DataDir("slow"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s2.DataDir("slow"), "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for statusOf(h2, "slow").Health.Events.Queued > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the queue never drained")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h2.EventAt("tab.created", 9999, time.Now(), json.RawMessage(`{}`))
	waitFor(t, "the event after the drop", func() bool {
		b, _ := os.ReadFile(filepath.Join(s2.DataDir("slow"), "ctx"))
		return strings.Contains(string(b), `"seq":9999`)
	})
	b, _ = os.ReadFile(filepath.Join(s2.DataDir("slow"), "ctx"))
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"seq":9999`) && !strings.Contains(line, `"missed":true`) {
			t.Fatalf("event after drops does not say missed: %s", line)
		}
	}
}

func TestHostAsyncActionsAndCancel(t *testing.T) {
	h, _, _ := startHost(t, apiOne, map[string]string{}, HostConfig{Isolation: IsolationStandard})
	res, err := h.Action(context.Background(), "surface", "bg", Context{})
	if err != nil || !res.Async || res.Run == "" {
		t.Fatalf("async = %+v, %v", res, err)
	}
	if st := statusOf(h, "surface"); len(st.Runs) != 1 || st.Runs[0].ID != res.Run {
		t.Fatalf("runs = %+v", st.Runs)
	}
	if err := h.Cancel("surface", res.Run); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to end", func() bool { return len(statusOf(h, "surface").Runs) == 0 })
	if st := statusOf(h, "surface"); st.Health.Actions.Canceled != 1 {
		t.Fatalf("actions = %+v", st.Health.Actions)
	}
	if err := h.Cancel("surface", "nope"); !errors.Is(err, ErrNoRun) {
		t.Fatalf("cancel unknown: %v", err)
	}
	// A synchronous action that runs out of time says so.
	slow := "api = 1\nid = \"slowact\"\nversion = \"1.0.0\"\n[[actions]]\nid = \"wait\"\ncommand = [\"sleep\", \"5\"]\ntimeout = \"200ms\"\n"
	h2, _, _ := startHost(t, slow, map[string]string{}, HostConfig{})
	if _, err := h2.Action(context.Background(), "slowact", "wait", Context{}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout: %v", err)
	}
	if st := statusOf(h2, "slowact"); st.Health.Actions.TimedOut != 1 || st.Health.State != StateDegraded {
		t.Fatalf("after timeout: %+v", st.Health)
	}
}

func TestHostPermissionsGateLoading(t *testing.T) {
	h, s, _ := startHost(t, apiOne, map[string]string{}, HostConfig{Isolation: IsolationStandard, Policy: Policy{Deny: []string{CapHiveWrite}}})
	st := statusOf(h, "surface")
	if st.Health.State != StateFailed || !strings.Contains(st.LastError, "denied by [plugins] deny") {
		t.Fatalf("denied plugin: %+v", st)
	}
	// Linking granted its required capabilities; an optional one asked
	// for at run time is recorded, never granted by asking.
	in, _ := s.Get(context.Background(), "surface", "v1.0.0")
	if strings.Join(in.Granted, ",") != "hive.read,hive.write" {
		t.Fatalf("granted = %v", in.Granted)
	}
	h2, _, pub := startHost(t, apiOne, map[string]string{}, HostConfig{Isolation: IsolationStandard})
	if _, err := h2.Request("surface", CapNetwork, "to fetch"); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.Request("surface", CapHiveAdmin, ""); err == nil {
		t.Fatal("a plugin asked for a capability it does not list")
	}
	st = statusOf(h2, "surface")
	if len(st.Health.Requests) != 1 || slices.Contains(st.Grant.Effective, CapNetwork) || pub.count(EventPermission) != 1 {
		t.Fatalf("request = %+v %+v", st.Health.Requests, st.Grant)
	}
	d := h2.Discovery("surface")
	if !slices.Contains(d.Methods, "tab.rename") || slices.Contains(d.Methods, "process.start") || !slices.Contains(d.Methods, "plugin.card") {
		t.Fatalf("discovery methods = %v", d.Methods)
	}
}

func TestHostContributions(t *testing.T) {
	h, _, pub := startHost(t, apiOne, map[string]string{}, HostConfig{Isolation: IsolationStandard, Hidden: []string{"surface:link"}})
	bad := []CardUpdate{
		{Plugin: "surface", Card: "nope", Environment: "e"},
		{Plugin: "surface", Card: "ci", Pane: "p"},
		{Plugin: "surface", Card: "agent-ci", Environment: "e"},
		{Plugin: "surface", Card: "ci", Environment: "e", Rows: make([]CardRow, MaxCardRows+1)},
		{Plugin: "surface", Card: "ci", Environment: "e", Rows: []CardRow{{Text: "x", Tone: "purple"}}},
		{Plugin: "surface", Card: "ci", Environment: "e", Links: []Link{{Title: "x", URL: "file:///x"}}},
		{Plugin: "surface", Card: "ci", Environment: "e", TTL: -time.Second},
		{Plugin: "ghost", Card: "ci", Environment: "e"},
	}
	for i, u := range bad {
		if _, err := h.SetCard(u); err == nil {
			t.Errorf("bad card %d accepted", i)
		}
	}
	c, err := h.SetCard(CardUpdate{Plugin: "surface", Card: "ci", Environment: "api", Rows: []CardRow{
		{Label: "main", Text: "passing \x1b[31m" + strings.Repeat("x", 80), Tone: "success"},
	}, Links: []Link{{Title: "Run", URL: "https://ci.example/run/1"}}})
	if err != nil || c.Title != "CI" || strings.ContainsRune(c.Rows[0].Text, 0x1b) || len([]rune(c.Rows[0].Text)) != MaxText {
		t.Fatalf("card = %+v, %v", c, err)
	}
	if _, err := h.SetStatus(StatusUpdate{Plugin: "surface", Indicator: "build", Text: "green", Tone: "success"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SetStatus(StatusUpdate{Plugin: "surface", Indicator: "nope", Text: "x"}); err == nil {
		t.Fatal("undeclared indicator accepted")
	}
	all := h.Contributions()
	if len(all.Cards) != 1 || len(all.Status) != 1 || pub.count(EventContributions) < 2 {
		t.Fatalf("contributions = %+v", all)
	}
	kinds := map[string]int{}
	for _, d := range all.Declared {
		kinds[d.Kind]++
		if (d.Kind == ContribLink) != d.Hidden {
			t.Errorf("%s hidden = %v", d.Key, d.Hidden)
		}
	}
	if kinds[ContribCard] != 2 || kinds[ContribAction] != 1 || kinds[ContribPalette] != 1 || kinds[ContribLink] != 1 {
		t.Fatalf("declared kinds = %v", kinds)
	}
	// Clearing, and expiry.
	if _, err := h.SetCard(CardUpdate{Plugin: "surface", Card: "ci", Environment: "api"}); err != nil || len(h.Contributions().Cards) != 0 {
		t.Fatalf("clear: %v", err)
	}
	if _, err := h.SetStatus(StatusUpdate{Plugin: "surface", Indicator: "build", Text: "x", TTL: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if got := h.content.expire(time.Now()); len(got) != 1 || len(h.Contributions().Status) != 0 {
		t.Fatalf("expire = %v", got)
	}
	// Unloading a plugin removes what it showed.
	_, _ = h.SetStatus(StatusUpdate{Plugin: "surface", Indicator: "build", Text: "y"})
	h.unload("surface")
	if len(h.Contributions().Status) != 0 {
		t.Fatal("content outlived its plugin")
	}
}

func TestConfigViewAndSet(t *testing.T) {
	manifest := `api = 1
id = "conf"
version = "1.0.0"
[[actions]]
id = "a"
command = ["true"]
[configuration]
version = 1
[[configuration.settings]]
name = "interval"
type = "integer"
default = 30
[[configuration.settings]]
name = "label"
type = "string"
required = true
default = "x"
`
	dir := writePlugin(t, manifest, map[string]string{}, false)
	s := NewStore(filepath.Join(t.TempDir(), "plugins"))
	if _, _, err := s.Link(context.Background(), dir, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, err := s.Config(ctx, "conf", "v1.0.0")
	if err != nil || v.Values["interval"] == nil || v.Error != "" || len(v.Settings) != 2 {
		t.Fatalf("view = %+v, %v", v, err)
	}
	n, _ := ParseSetting("integer", "45")
	if v, err = s.SetConfig(ctx, "conf", "v1.0.0", map[string]any{"interval": n}, nil); err != nil || v.Values["interval"] != float64(45) {
		t.Fatalf("set = %+v, %v", v.Values, err)
	}
	if _, err := s.SetConfig(ctx, "conf", "v1.0.0", map[string]any{"interval": "soon"}, nil); err == nil {
		t.Fatal("a string set as an integer")
	}
	if _, err := s.SetConfig(ctx, "conf", "v1.0.0", map[string]any{"nope": 1}, nil); err == nil {
		t.Fatal("an undeclared setting set")
	}
	if v, err = s.SetConfig(ctx, "conf", "v1.0.0", nil, []string{"interval"}); err != nil || v.Values["interval"] != float64(30) {
		t.Fatalf("unset = %+v, %v", v.Values, err)
	}
	for typ, raw := range map[string]string{"boolean": "maybe", "integer": "1.5", "number": "x"} {
		if _, err := ParseSetting(typ, raw); err == nil {
			t.Errorf("%s %q parsed", typ, raw)
		}
	}
	// A setting a release dropped is kept, as a warning, not an error.
	path := s.SettingsPath(&Manifest{ID: "conf"})
	_ = os.WriteFile(path, []byte(`{"schema_version":1,"values":{"label":"y","old_flag":true}}`), 0o600)
	if v, err = s.Config(ctx, "conf", "v1.0.0"); err != nil || v.Error != "" || len(v.Warnings) != 1 || v.Values["old_flag"] != true {
		t.Fatalf("obsolete = %+v, %v", v, err)
	}
}

func TestStrictIsolation(t *testing.T) {
	if !SandboxAvailable() {
		t.Skip("no sandbox tool here")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("exercised on macOS; Linux's bwrap path is built but not run here")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	manifest := `api = 1
id = "boxed"
version = "1.0.0"
isolation = "strict"
[[actions]]
id = "probe"
command = ["./probe.sh"]
`
	h, s, _ := startHost(t, manifest, map[string]string{
		"probe.sh": "#!/bin/sh\n" +
			"touch \"$HIVE_PLUGIN_DATA/inside\" && echo inside-ok\n" +
			"touch \"$OUTSIDE\" 2>/dev/null && echo outside-written\n" +
			"echo home=$HOME\n" +
			"[ -n \"$SECRET_TOKEN\" ] && echo leaked\n" +
			"/usr/bin/nc -z -G 1 1.1.1.1 443 2>/dev/null && echo network-ok\n" +
			"exit 0\n",
	}, HostConfig{Env: func(vars map[string]string) []string {
		env := append(os.Environ(), "SECRET_TOKEN=hunter2", "OUTSIDE="+outside)
		for k, v := range vars {
			env = append(env, k+"="+v)
		}
		return env
	}})
	if st := statusOf(h, "boxed"); st.Isolation != IsolationStrict {
		t.Fatalf("isolation = %s", st.Isolation)
	}
	res, err := h.Action(context.Background(), "boxed", "probe", Context{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "inside-ok") || strings.Contains(res.Output, "outside-written") || strings.Contains(res.Output, "leaked") || strings.Contains(res.Output, "network-ok") {
		t.Fatalf("sandboxed output:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "home="+s.CacheDir("boxed")) {
		t.Fatalf("HOME is not the plugin's cache:\n%s", res.Output)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a strict plugin wrote outside its directories")
	}
}
