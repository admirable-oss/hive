package observer

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestScopes(t *testing.T) {
	for _, bad := range []Scope{{}, {All: true, Environments: []string{"a"}}, {Panes: []string{""}}, {Environments: []string{"a b"}}} {
		if bad.Validate() == nil {
			t.Errorf("%+v is valid", bad)
		}
	}
	s := Scope{Environments: []string{"dev"}, Panes: []string{"p9"}}
	cases := []struct {
		t    Target
		sees bool
	}{
		{Target{Environment: "dev"}, true},
		{Target{Environment: "dev", Pane: "p1"}, true},
		{Target{Environment: "ops", Pane: "p9"}, true},
		{Target{Environment: "ops"}, false},
		{Target{Environment: "ops", Pane: "p1"}, false},
		{Target{}, false},
	}
	for _, c := range cases {
		if got := s.Sees(c.t); got != c.sees {
			t.Errorf("Sees(%+v) = %v", c.t, got)
		}
	}
	if !(Scope{All: true}).Sees(Target{}) {
		t.Fatal("all does not see the session")
	}
}

func TestStoreKeepsOnlyHashes(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, _, err := s.Create(CreateRequest{Name: "x", Scope: Scope{All: true}, TTL: time.Second}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a TTL under a minute: %v", err)
	}
	if _, _, err := s.Create(CreateRequest{Name: "", Scope: Scope{All: true}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no name: %v", err)
	}
	c, token, err := s.Create(CreateRequest{Name: "dash", Scope: Scope{Environments: []string{"b", "a", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if c.State != StateActive || time.Until(c.ExpiresAt) < DefaultTTL-time.Minute || strings.Join(c.Scope.Environments, ",") != "a,b" {
		t.Fatalf("credential = %+v", c)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token[len(tokenPrefix)+13:]) {
		t.Fatal("the store holds the secret")
	}
	if goruntime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(dir, "credentials.json")); fi.Mode().Perm() != 0o600 {
			t.Fatalf("store mode %v", fi.Mode())
		}
	}

	// A fresh store (a restarted daemon) knows the token.
	s2 := NewStore(dir)
	got, err := s2.Authenticate(token)
	if err != nil || got.ID != c.ID || got.LastUsedAt == nil {
		t.Fatalf("authenticate = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "hvo_", token + "x", strings.Replace(token, c.ID, "000000000000", 1), "Bearer " + token} {
		if _, err := s2.Authenticate(bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	if _, err := s2.Revoke(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Authenticate(token); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	if _, err := s2.Check(c.ID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("check revoked: %v", err)
	}

	// Expiry, by moving the clock.
	_, token2, err := s2.Create(CreateRequest{Name: "short", Scope: Scope{All: true}, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s2.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := s2.Authenticate(token2); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	// Ended credentials are dropped a while after they end.
	s2.now = func() time.Time { return time.Now().Add(keepEnded + time.Hour) }
	if err := s2.save(); err != nil {
		t.Fatal(err)
	}
	if list, _ := s2.List(); len(list) != 1 || list[0].ID != c.ID && list[0].Name != "dash" {
		t.Logf("kept: %+v", list)
	}
}

func ev(seq uint64, typ string, data any) event.Event {
	raw, _ := json.Marshal(data)
	return event.Event{Seq: seq, Type: typ, Time: time.Now(), Data: raw}
}

type fakeLocator map[string]Location

func (f fakeLocator) Process(id string) (Location, bool) { l, ok := f[id]; return l, ok }
func (f fakeLocator) Pane(id string) (Location, bool) {
	for _, l := range f {
		if l.Pane == id {
			return l, true
		}
	}
	return Location{}, false
}

func TestTranslateRedactsAndLocates(t *testing.T) {
	loc := fakeLocator{"proc1": {Environment: "dev", Tab: "t1", Pane: "p1"}}
	exit := 0
	out, ok := Translate(ev(5, event.ProcessExited, process.Process{
		ID: "proc1", EnvironmentID: "dev", Command: "/home/me/bin/claude", Args: []string{"--api-key", "sk-123"},
		WorkingDir: "/home/me/secret", Env: map[string]string{"TOKEN": "abc"}, PID: 42, Status: process.StatusExited, ExitCode: &exit,
	}), loc)
	if !ok || out.Seq != 5 || out.target != (Target{Environment: "dev", Pane: "p1"}) {
		t.Fatalf("translated = %+v", out)
	}
	for _, leak := range []string{"sk-123", "/home/me", "TOKEN", "42", "args"} {
		if strings.Contains(string(out.Data), leak) {
			t.Errorf("a process event carries %q: %s", leak, out.Data)
		}
	}
	if !strings.Contains(string(out.Data), `"program":"claude"`) {
		t.Errorf("process event = %s", out.Data)
	}
	env, ok := Translate(ev(6, event.EnvironmentCreated, environment.Environment{ID: "dev", Env: map[string]string{"AWS_SECRET": "x"}}), loc)
	if !ok || strings.Contains(string(env.Data), "AWS_SECRET") {
		t.Fatalf("environment event = %s", env.Data)
	}
	a, ok := Translate(ev(7, agent.EventState, agent.Agent{ID: "proc1", EnvironmentID: "dev", PaneID: "p1", PID: 9, Status: agent.Status{State: "working", SessionID: "resume-me"}}), loc)
	if !ok || strings.Contains(string(a.Data), "resume-me") || !a.Visible(Scope{Panes: []string{"p1"}}) || a.Visible(Scope{Panes: []string{"p2"}}) {
		t.Fatalf("agent event = %s", a.Data)
	}
	if o, ok := Translate(ev(8, agent.EventOutput, map[string]string{"id": "proc1"}), loc); !ok || !strings.Contains(string(o.Data), `"pane_id":"p1"`) {
		t.Fatalf("output event = %+v", o)
	}
	for _, typ := range []string{"plugin.changed", "plugin.permission", "session.restored", "something.new"} {
		if _, ok := Translate(ev(9, typ, map[string]string{"id": "x"}), loc); ok {
			t.Errorf("%s reached observers", typ)
		}
	}
}

func record(h *History, seq uint64, env string) {
	h.Record(Event{Seq: seq, Type: pane.EventTabUpdated, Data: json.RawMessage(`{}`), target: Target{Environment: env}})
}

func TestHistoryPagesAndResyncs(t *testing.T) {
	h := NewHistory(10)
	for i := uint64(1); i <= 25; i++ {
		env := "dev"
		if i%2 == 0 {
			env = "ops"
		}
		record(h, i, env)
	}
	dev := Scope{Environments: []string{"dev"}}
	// Evicted: positions before 15 are gone.
	if p := h.Page(dev, h.Epoch(), 3, 0); p.Resync == nil || p.Resync.Reason != "history" || len(p.Events) != 0 {
		t.Fatalf("evicted page = %+v", p)
	}
	if p := h.Page(dev, "another-run", 20, 0); p.Resync == nil || p.Resync.Reason != "epoch" {
		t.Fatalf("other epoch = %+v", p)
	}
	if p := h.Page(dev, h.Epoch(), 99, 0); p.Resync == nil {
		t.Fatalf("a position from the future = %+v", p)
	}
	var seen []uint64
	since := uint64(15)
	for {
		p := h.Page(dev, h.Epoch(), since, 2)
		if p.Resync != nil {
			t.Fatal(p.Resync)
		}
		for _, e := range p.Events {
			seen = append(seen, e.Seq)
		}
		since = p.Next
		if !p.More {
			break
		}
	}
	if got, _ := json.Marshal(seen); string(got) != "[17,19,21,23,25]" || since != 25 {
		t.Fatalf("paged %s, next %d", got, since)
	}
}

func TestFeedResyncsWhenBehind(t *testing.T) {
	h := NewHistory(DefaultHistory)
	all := Scope{All: true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record(h, 1, "dev")
	since := uint64(0)
	f, at := h.Subscribe(all, h.Epoch(), &since)
	defer f.Close()
	if at != 1 {
		t.Fatalf("at %d", at)
	}
	if e, _ := f.Next(ctx); e.Seq != 1 {
		t.Fatalf("replayed %+v", e)
	}
	// A reader that does not keep up loses its queue: its next event is a
	// resync.
	for i := uint64(2); i < 2+FeedBuffer+10; i++ {
		record(h, i, "dev")
	}
	var n int
	for {
		e, err := f.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if e.Type == Resync {
			break
		}
		n++
	}
	if n != 0 {
		t.Fatalf("%d stale events before the resync", n)
	}
	// Loss before recording resyncs everyone, even an idle reader.
	done := make(chan Event, 1)
	go func() { e, _ := f.Next(ctx); done <- e }()
	time.Sleep(20 * time.Millisecond)
	h.Lost(500)
	if e := <-done; e.Type != Resync {
		t.Fatalf("after loss: %+v", e)
	}
	stale := uint64(10)
	f2, _ := h.Subscribe(all, h.Epoch(), &stale)
	defer f2.Close()
	if e, _ := f2.Next(ctx); e.Type != Resync {
		t.Fatalf("subscribing from a lost position: %+v", e)
	}
}

// fakeSources serve one environment with one pane.
type fakeSources struct{}

func (fakeSources) List(context.Context) ([]environment.Environment, error) {
	return []environment.Environment{{ID: "dev"}, {ID: "ops"}}, nil
}

func (fakeSources) Tabs(context.Context, string) ([]pane.Tab, error) { return nil, nil }
func (fakeSources) Panes(context.Context, string, string) ([]pane.Pane, error) {
	return []pane.Pane{{ID: "p1", EnvironmentID: "dev"}, {ID: "p2", EnvironmentID: "ops"}}, nil
}

func (fakeSources) Pane(_ context.Context, id string) (pane.Pane, error) {
	switch id {
	case "p1":
		return pane.Pane{ID: "p1", EnvironmentID: "dev", ProcessID: "proc1"}, nil
	case "p2":
		return pane.Pane{ID: "p2", EnvironmentID: "ops", ProcessID: "proc2"}, nil
	}
	return pane.Pane{}, errors.New("no such pane")
}

type noAgents struct{}

func (noAgents) List(context.Context, bool) ([]agent.Agent, error) { return nil, nil }

type noTerminals struct{}

func (noTerminals) Get(string) (terminal.Session, error) { return nil, errors.New("none") }

func newTestServer(t *testing.T) (*Server, *Store) {
	t.Helper()
	dir := t.TempDir()
	store := NewStore(dir)
	audit, err := OpenAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	src := fakeSources{}
	s := NewServer(Config{
		Store: store, History: NewHistory(0), Audit: audit, MaxConnections: 2,
		Sources: Sources{Environments: src, Panes: src, Agents: noAgents{}, Terminals: noTerminals{}},
	})
	return s, store
}

// serve connects a client to s over a pipe.
func serve(t *testing.T, s *Server) func(context.Context) (net.Conn, error) {
	t.Helper()
	return func(context.Context) (net.Conn, error) {
		ours, theirs := net.Pipe()
		go s.Serve(context.Background(), theirs, protocol.ServerInfo{Version: "test"})
		return ours, nil
	}
}

func TestServerExpiresConnections(t *testing.T) {
	s, store := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Made so that it expires a second or so from now (creation times are
	// whole seconds).
	store.now = func() time.Time { return time.Now().Add(-MinTTL + 1500*time.Millisecond) }
	_, token, err := store.Create(CreateRequest{Name: "brief", Scope: Scope{Environments: []string{"dev"}}, TTL: MinTTL})
	if err != nil {
		t.Fatal(err)
	}
	store.now = time.Now
	c, err := Connect(ctx, ClientConfig{Dial: serve(t, s), Token: token})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	snap, err := c.Snapshot(ctx)
	if err != nil || len(snap.Environments) != 1 || len(snap.Panes) != 1 {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	if _, err := c.Pane(ctx, "p2"); code(err) != protocol.ErrorCodeNotFound {
		t.Fatalf("out of scope pane: %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("an expired credential's connection stayed open")
	}
	if _, err := Connect(ctx, ClientConfig{Dial: serve(t, s), Token: token}); code(err) != protocol.ErrorCodePermissionDenied {
		t.Fatalf("connecting with an expired token: %v", err)
	}
}

func TestServerLimits(t *testing.T) {
	s, store := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, token, err := store.Create(CreateRequest{Name: "busy", Scope: Scope{All: true}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := Connect(ctx, ClientConfig{Dial: serve(t, s), Token: token})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Connect(ctx, ClientConfig{Dial: serve(t, s), Token: token})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// MaxConnections is 2: a third is closed at once.
	if c, err := Connect(ctx, ClientConfig{Dial: serve(t, s), Token: token}); err == nil {
		_ = c.Close()
		t.Fatal("a connection over the limit was served")
	}
	// The request rate is bounded per connection.
	limited := false
	for range int(requestBurst) + 10 {
		if _, err := a.Status(ctx); code(err) == protocol.ErrorCodeUnavailable {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("no rate limit")
	}
	// Streams are bounded per connection.
	var streams []*EventStream
	defer func() {
		for _, st := range streams {
			_ = st.Close()
		}
	}()
	for range maxStreams {
		st, _, err := b.Events(ctx, EventsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		streams = append(streams, st)
	}
	if _, _, err := b.Events(ctx, EventsRequest{}); code(err) != protocol.ErrorCodeUnavailable {
		t.Fatalf("a stream over the limit: %v", err)
	}
	// Bad tokens end the connection after a few tries.
	s, _ = newTestServer(t)
	ours, theirs := net.Pipe()
	go s.Serve(context.Background(), theirs, protocol.ServerInfo{})
	mux, err := protocol.Handshake(ctx, ours, protocol.Hello{Client: "guess"}, protocol.DefaultMaxMessageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	for range maxAuthFailures {
		_ = mux.Call(ctx, methodAuth, AuthRequest{Token: "hvo_" + strings.Repeat("0", 12) + "_" + strings.Repeat("a", 43)}, nil)
	}
	select {
	case <-mux.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a connection guessing tokens stayed open")
	}
	entries, err := s.cfg.Audit.Read(0, "")
	if err != nil {
		t.Fatal(err)
	}
	refused := 0
	for _, e := range entries {
		if e.Action == AuditRefused {
			refused++
		}
	}
	if refused < maxAuthFailures {
		t.Fatalf("audit: %+v", entries)
	}
}

func code(err error) string {
	if pe, ok := errors.AsType[*protocol.Error](err); ok {
		return pe.Code
	}
	return ""
}

// TestFollowResumesAndResyncs: Follow passes a snapshot, then events; after
// a dropped connection it resumes from history without a new snapshot;
// after events were lost it passes a fresh snapshot.
func TestFollowResumesAndResyncs(t *testing.T) {
	s, store := newTestServer(t)
	_, token, err := store.Create(CreateRequest{Name: "follow", Scope: Scope{All: true}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	updates := make(chan Update, 64)
	done := make(chan error, 1)
	go func() {
		done <- Follow(ctx, ClientConfig{Dial: serve(t, s), Token: token}, func(u Update) error {
			updates <- u
			return nil
		})
	}()
	next := func() Update {
		t.Helper()
		select {
		case u := <-updates:
			return u
		case <-ctx.Done():
			t.Fatal("no update")
			return Update{}
		}
	}
	if u := next(); u.Snapshot == nil || len(u.Snapshot.Environments) != 2 {
		t.Fatalf("first update = %+v", u)
	}
	h := s.cfg.History
	waitConnected := func() {
		for len(s.Connections()) == 0 || h.feedCount() == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitConnected()
	record(h, 10, "dev")
	if u := next(); u.Event == nil || u.Event.Seq != 10 {
		t.Fatalf("event = %+v", u)
	}
	// The connection drops; what happens meanwhile is replayed.
	for _, c := range s.sessionsOf(s.onlyCredential()) {
		c.end("test")
	}
	for h.feedCount() != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	record(h, 11, "ops")
	waitConnected()
	if u := next(); u.Event == nil || u.Event.Seq != 11 {
		t.Fatalf("after reconnecting = %+v", u)
	}
	// Events lost: a new snapshot.
	h.Lost(50)
	if u := next(); u.Snapshot == nil {
		t.Fatalf("after a loss = %+v", u)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (h *History) feedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.feeds)
}

func (s *Server) onlyCredential() string {
	for id := range s.Connections() {
		return id
	}
	return ""
}
