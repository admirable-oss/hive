package web

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/observer"
)

func runtimesFor(names ...string) []Runtime {
	var out []Runtime
	for _, n := range names {
		out = append(out, Runtime{Name: n})
	}
	return out
}

func TestLinksAreOneTimeAndScoped(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	rts := runtimesFor("laptop")
	for _, bad := range []ShareRequest{
		{Runtimes: []string{"nowhere"}, Scope: observer.Scope{All: true}},
		{Runtimes: nil, Scope: observer.Scope{All: true}},
		{Runtimes: []string{"laptop"}, Scope: observer.Scope{}},
		{Runtimes: []string{"laptop"}, Scope: observer.Scope{All: true}, History: true},
		{Runtimes: []string{"laptop"}, Scope: observer.Scope{All: true}, TTLSeconds: 1},
		{Runtimes: []string{"laptop"}, Scope: observer.Scope{All: true}, MaxUses: MaxLinkUses + 1},
	} {
		if _, _, err := s.Share(bad, rts); err == nil {
			t.Errorf("shared %+v", bad)
		}
	}
	g, code, err := s.Share(ShareRequest{Label: "pair", Runtimes: []string{"laptop"}, Scope: observer.Scope{Environments: []string{"dev"}}, MaxUses: 2}, rts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, codePrefix) || g.MaxUses != 2 || g.CodeHash != "" {
		t.Fatalf("grant %+v code %q", g, code)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "access.json"))
	if strings.Contains(string(raw), strings.SplitN(code, "_", 3)[2]) {
		t.Fatal("the store holds the link's secret")
	}
	if goruntime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(dir, "access.json")); fi.Mode().Perm() != 0o600 {
			t.Fatalf("access.json mode %v", fi.Mode())
		}
	}
	_, _, c1, err := s.Redeem(code, "browser one")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Redeem(code, "browser two"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Redeem(code, "browser three"); !errors.Is(err, ErrLinkRefused) {
		t.Fatalf("a third use: %v", err)
	}
	for _, bad := range []string{"", code, c1 + "x", strings.Replace(c1, ".", ".A", 1), "nonsense"} {
		if _, _, err := s.Authenticate(bad); err == nil {
			t.Errorf("cookie %q works", bad)
		}
	}
	sess, got, err := s.Authenticate(c1)
	if err != nil || got.ID != g.ID || sess.Client != "browser one" {
		t.Fatalf("authenticate: %+v %+v %v", sess, got, err)
	}
	// A restarted server knows the session.
	if _, _, err := NewStore(dir).Authenticate(c1); err != nil {
		t.Fatal(err)
	}
	// Revoking one browser leaves the other; revoking the grant ends all.
	if _, ended, err := s.Revoke(sess.ID); err != nil || len(ended) != 1 {
		t.Fatal(err)
	}
	if _, _, err := s.Authenticate(c1); !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("a revoked browser: %v", err)
	}
	if _, xs, _ := s.Grants(); len(xs) != 1 {
		t.Fatalf("open sessions = %d", len(xs))
	}
	if _, ended, err := s.Revoke(g.ID); err != nil || len(ended) != 1 {
		t.Fatalf("revoke the grant: %v %v", ended, err)
	}
	// An expired link, and expired access.
	_, code2, _ := s.Share(ShareRequest{Runtimes: []string{"laptop"}, Scope: observer.Scope{All: true}, TTLSeconds: 3600, LinkTTLSeconds: 60}, rts)
	s.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, _, _, err := s.Redeem(code2, ""); !errors.Is(err, ErrLinkRefused) {
		t.Fatalf("an expired link: %v", err)
	}
	s.now = time.Now
	_, code3, _ := s.Share(ShareRequest{Runtimes: []string{"laptop"}, Scope: observer.Scope{All: true}, TTLSeconds: 120}, rts)
	_, _, c3, err := s.Redeem(code3, "")
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
	if _, _, err := s.Authenticate(c3); !errors.Is(err, ErrGrantExpired) {
		t.Fatalf("expired access: %v", err)
	}
}

func agentEvent(seq uint64, a observer.Agent) observer.Update {
	raw, _ := json.Marshal(a)
	return observer.Update{Event: &observer.Event{Seq: seq, Type: "agent.state", Time: time.Now(), Data: raw}}
}

func TestModelKeepsTheNewest(t *testing.T) {
	m := NewModel()
	m.Apply(observer.Update{Snapshot: &observer.Snapshot{Seq: 10, Agents: []observer.Agent{{ID: "a", Name: "claude", EnvironmentID: "dev", PaneID: "p1", State: agent.StateWorking, StateSeq: 4}}}}, time.Now())
	// Already in the snapshot: ignored.
	m.Apply(agentEvent(9, observer.Agent{ID: "a", State: agent.StateIdle, StateSeq: 3}), time.Now())
	if v := m.View(); v.Agents["a"].State != agent.StateWorking {
		t.Fatalf("an event before the snapshot applied: %+v", v.Agents["a"])
	}
	// Newer position but an older state: ignored.
	m.Apply(agentEvent(11, observer.Agent{ID: "a", State: agent.StateIdle, StateSeq: 2}), time.Now())
	if v := m.View(); v.Agents["a"].State != agent.StateWorking {
		t.Fatalf("an older state applied: %+v", v.Agents["a"])
	}
	// A newer state applies, keeping where the agent is.
	m.Apply(agentEvent(12, observer.Agent{ID: "a", Name: "claude", State: agent.StateBlocked, StateSeq: 5, Reason: "a permission prompt"}), time.Now())
	v := m.View()
	if a := v.Agents["a"]; a.State != agent.StateBlocked || a.EnvironmentID != "dev" || a.PaneID != "p1" {
		t.Fatalf("agent = %+v", a)
	}
	if len(v.Activity) != 1 || !strings.Contains(v.Activity[0].Text, "blocked") {
		t.Fatalf("activity = %+v", v.Activity)
	}
	for i := range activityLimit + 50 {
		state := agent.StateIdle
		if i%2 == 0 {
			state = agent.StateWorking
		}
		m.Apply(agentEvent(uint64(13+i), observer.Agent{ID: "a", State: state, StateSeq: uint64(6 + i)}), time.Now())
	}
	if n := len(m.View().Activity); n != activityLimit {
		t.Fatalf("activity grew to %d", n)
	}
}

func TestServingNeedsTLSBeyondThisMachine(t *testing.T) {
	ok := []Config{
		{Dir: "x", Listen: "127.0.0.1:8484"},
		{Dir: "x", Listen: "localhost:0"},
		{Dir: "x", Listen: "[::1]:8484"},
		{Dir: "x", Listen: "0.0.0.0:443", TLSCert: "c", TLSKey: "k"},
		{Dir: "x", Listen: "0.0.0.0:8080", BehindProxy: true, PublicURL: "https://hive.example.com"},
	}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	for _, c := range []Config{
		{Dir: "x", Listen: "0.0.0.0:8484"},
		{Dir: "x", Listen: "192.168.1.5:80"},
		{Dir: "x", Listen: "127.0.0.1:1", TLSCert: "only-cert"},
		{Dir: "x", Listen: "127.0.0.1:1", PublicURL: "ftp://x"},
		{Listen: "127.0.0.1:1"},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v is accepted", c)
		}
	}
	if u := (Config{Listen: "0.0.0.0:8484", TLSCert: "c"}).publicURL(); u != "https://localhost:8484" {
		t.Fatalf("public URL %s", u)
	}
}

func TestStreamViews(t *testing.T) {
	cases := map[string]streamView{
		"/":                     {kind: "dashboard"},
		"/?q=claude&state=idle": {kind: "dashboard"},
		"/r/laptop":             {kind: "runtime", runtime: "laptop"},
		"/r/laptop/e/my%20env":  {kind: "environment", runtime: "laptop", env: "my env"},
		"/r/laptop/p/p123":      {kind: "pane", runtime: "laptop", pane: "p123"},
	}
	for raw, want := range cases {
		got, ok := parseView(raw)
		if !ok || got.kind != want.kind || got.runtime != want.runtime || got.env != want.env || got.pane != want.pane {
			t.Errorf("%s → %+v", raw, got)
		}
	}
	for _, bad := range []string{"", "relative", "/r", "/r/x/q/y", "/r/x/p/y/z"} {
		if _, ok := parseView(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if id, v := fragmentMeta(`<div id="agents" data-v="42"><p>x</p></div>`); id != "agents" || v != 42 {
		t.Fatalf("meta = %s %d", id, v)
	}
}
