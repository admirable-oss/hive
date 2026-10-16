//go:build !windows

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/runtime"
)

// observed is a daemon with two environments, each with a shell pane.
type observed struct {
	mod          *runtime.Module
	control      client.Client
	root         string
	dev, other   pane.Pane
	devTab, oTab pane.Tab
}

func newObserved(ctx context.Context, t *testing.T) *observed {
	t.Helper()
	sock := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: sock, Shell: []string{"sh"}})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.StopWithAgents(context.Background()) })
	o := &observed{mod: mod, root: filepath.Dir(sock), control: client.NewService(client.Config{SocketPath: sock})}
	t.Cleanup(func() { _ = o.control.Close() })
	for _, id := range []string{"dev", "other"} {
		if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: id, Env: map[string]string{"SECRET_TOKEN": "s3cr3t-" + id}}); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if o.devTab, o.dev, err = mod.Panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Name: "work"}); err != nil {
		t.Fatal(err)
	}
	if o.oTab, o.other, err = mod.Panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "other", Name: "private"}); err != nil {
		t.Fatal(err)
	}
	return o
}

func (o *observed) create(ctx context.Context, t *testing.T, p runtime.ObserverCreateParams) runtime.ObserverCreated {
	t.Helper()
	var res runtime.ObserverCreated
	if err := o.control.Call(ctx, "observer.create", p, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func (o *observed) connect(ctx context.Context, t *testing.T, token string) *observer.Client {
	t.Helper()
	c, err := observer.Connect(ctx, observer.ClientConfig{Socket: runtime.ObserverSocketPath(o.root), Token: token, Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func code(err error) string {
	if pe, ok := errors.AsType[*protocol.Error](err); ok {
		return pe.Code
	}
	return ""
}

// TestObserverSeesOnlyItsScope: a credential scoped to one environment
// sees that environment's workspace, screens and events, nothing of the
// other, and no secret-bearing fields.
func TestObserverSeesOnlyItsScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o := newObserved(ctx, t)
	created := o.create(ctx, t, runtime.ObserverCreateParams{Name: "dashboard", Scope: observer.Scope{Environments: []string{"dev"}}, Screens: true})
	if !strings.HasPrefix(created.Token, "hvo_") || created.Socket != runtime.ObserverSocketPath(o.root) {
		t.Fatalf("created = %+v", created)
	}
	if fi, err := os.Stat(created.Socket); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("observer socket: %v %v", fi, err)
	}
	c := o.connect(ctx, t, created.Token)

	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Environments) != 1 || snap.Environments[0].ID != "dev" || len(snap.Tabs) != 1 || len(snap.Panes) != 1 || snap.Panes[0].ID != o.dev.ID {
		t.Fatalf("snapshot = %+v", snap)
	}
	raw, _ := json.Marshal(snap)
	for _, leak := range []string{"s3cr3t", "SECRET_TOKEN", `"args"`, `"env"`, `"pid"`, "other"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the snapshot carries %q: %s", leak, raw)
		}
	}

	// Screens of panes in scope only; one out of scope is "not found".
	if err := o.mod.Panes.Run(ctx, o.dev.ID, "echo dev-visible"); err != nil {
		t.Fatal(err)
	}
	if err := o.mod.Panes.Run(ctx, o.other.ID, "echo other-hidden"); err != nil {
		t.Fatal(err)
	}
	waitFor(ctx, t, func() bool {
		scr, err := c.Screen(ctx, observer.ScreenRequest{Pane: o.dev.ID})
		return err == nil && strings.Contains(strings.Join(scr.Lines, "\n"), "dev-visible")
	})
	for _, call := range []func() error{
		func() error { _, err := c.Screen(ctx, observer.ScreenRequest{Pane: o.other.ID}); return err },
		func() error { _, err := c.Output(ctx, observer.OutputRequest{Pane: o.other.ID}); return err },
		func() error { _, err := c.Pane(ctx, o.other.ID); return err },
		func() error { _, _, err := c.Watch(ctx, observer.WatchRequest{Pane: o.other.ID}); return err },
		func() error { _, err := c.Screen(ctx, observer.ScreenRequest{Pane: "p-nope"}); return err },
	} {
		if err := call(); code(err) != protocol.ErrorCodeNotFound {
			t.Errorf("out of scope: %v", err)
		}
	}

	// Events: what happens in dev arrives; what happens in other does not.
	stream, at, err := c.Events(ctx, observer.EventsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, _, err := o.mod.Panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "other", Name: "hidden-tab"}); err != nil {
		t.Fatal(err)
	}
	devTab, _, err := o.mod.Panes.CreateTab(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Name: "seen-tab"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		ev, err := stream.Next()
		if err != nil {
			t.Fatal(err)
		}
		if ev.Seq <= at.Seq || strings.Contains(string(ev.Data), "other") || strings.Contains(string(ev.Data), "hidden-tab") {
			t.Fatalf("event out of scope or order: %+v (start %d)", ev, at.Seq)
		}
		if ev.Type == pane.EventTabCreated && strings.Contains(string(ev.Data), devTab.ID) {
			break
		}
	}

	// Watching a screen: a keyframe, then what changes.
	watch, _, err := c.Watch(ctx, observer.WatchRequest{Pane: o.dev.ID, IntervalMS: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	first, err := watch.Next()
	if err != nil || !first.Keyframe {
		t.Fatalf("first update = %+v, %v", first, err)
	}
	lines := first.Apply(nil)
	if err := o.mod.Panes.Run(ctx, o.dev.ID, "echo watched-$((40+2))"); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(strings.Join(lines, "\n"), "watched-42") {
		u, err := watch.Next()
		if err != nil {
			t.Fatal(err)
		}
		lines = u.Apply(lines)
	}
}

// TestObserverCannotControl: the observer socket has no method that types,
// changes layouts or starts anything, even for a credential that sees
// everything, and nothing works before authenticating.
func TestObserverCannotControl(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o := newObserved(ctx, t)
	created := o.create(ctx, t, runtime.ObserverCreateParams{Name: "all", Scope: observer.Scope{All: true}, Screens: true})

	// Before authenticating: only observe.auth and observe.schema answer.
	raw, err := net.Dial("unix", created.Socket)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := protocol.Handshake(ctx, raw, protocol.Hello{Client: "probe"}, protocol.DefaultMaxMessageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	for _, m := range mux.Welcome().Methods {
		if !strings.HasPrefix(m, "observe.") {
			t.Errorf("the observer socket offers %s", m)
		}
	}
	if err := mux.Call(ctx, "observe.snapshot", nil, nil); code(err) != protocol.ErrorCodePermissionDenied {
		t.Fatalf("unauthenticated snapshot: %v", err)
	}
	var schema protocol.Schema
	if err := mux.Call(ctx, "observe.schema", nil, &schema); err != nil || len(schema.Methods) == 0 {
		t.Fatalf("schema: %v", err)
	}
	if err := mux.Call(ctx, "observe.auth", observer.AuthRequest{Token: "hvo_000000000000_" + strings.Repeat("x", 43)}, nil); code(err) != protocol.ErrorCodePermissionDenied {
		t.Fatalf("a made-up token: %v", err)
	}

	c := o.connect(ctx, t, created.Token)
	before, err := o.mod.Panes.Panes(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		method string
		params any
	}{
		{"pane.input", map[string]any{"id": o.dev.ID, "data": "aGk="}},
		{"pane.send_text", map[string]any{"id": o.dev.ID, "text": "rm -rf /\n"}},
		{"pane.send_keys", map[string]any{"id": o.dev.ID, "keys": []string{"enter"}}},
		{"pane.run", map[string]any{"id": o.dev.ID, "command": "true"}},
		{"terminal.input", map[string]any{"process_id": o.dev.ProcessID, "data": "aGk="}},
		{"terminal.resize", map[string]any{"process_id": o.dev.ProcessID, "width": 10, "height": 10}},
		{"terminal.attach", map[string]any{"process_id": o.dev.ProcessID}},
		{"terminal.frames", map[string]any{"process_id": o.dev.ProcessID}},
		{"pane.split", map[string]any{"pane": o.dev.ID, "direction": "right"}},
		{"pane.close", map[string]any{"id": o.dev.ID}},
		{"tab.create", map[string]any{"environment_id": "dev"}},
		{"layout.apply", map[string]any{}},
		{"process.start", map[string]any{"environment_id": "dev", "command": "sh"}},
		{"agent.start", map[string]any{"environment_id": "dev", "kind": "shell"}},
		{"agent.prompt", map[string]any{"id": o.dev.ProcessID, "text": "hi"}},
		{"environment.create", map[string]any{"id": "x"}},
		{"observer.create", runtime.ObserverCreateParams{Name: "wider", Scope: observer.Scope{All: true}}},
		{"observer.revoke", map[string]any{"id": c.Credential().ID}},
		{"runtime.shutdown", nil},
		{"events.subscribe", nil},
		{"api.snapshot", nil},
	} {
		if err := c.Call(ctx, m.method, m.params, nil); code(err) != protocol.ErrorCodeUnknownMethod {
			t.Errorf("%s on the observer socket: %v", m.method, err)
		}
	}
	// A second observe.auth cannot switch or widen the connection.
	if err := c.Call(ctx, "observe.auth", observer.AuthRequest{Token: created.Token}, nil); code(err) != protocol.ErrorCodeConflict {
		t.Errorf("second auth: %v", err)
	}
	after, err := o.mod.Panes.Panes(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("panes changed under an observer: %d → %d", len(before), len(after))
	}
	if err := o.control.Ping(ctx); err != nil {
		t.Fatalf("the daemon is down after the observer's attempts: %v", err)
	}
}

// TestObserverRevocation: revoking a credential ends its open connections
// at once and refuses new ones; credentials without screen access cannot
// read screens; the audit log tells the story.
func TestObserverRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o := newObserved(ctx, t)
	created := o.create(ctx, t, runtime.ObserverCreateParams{Name: "metadata-only", Scope: observer.Scope{Panes: []string{o.dev.ID}}, TTLSeconds: 3600})
	if time.Until(created.Credential.ExpiresAt) > time.Hour || created.Credential.Screens {
		t.Fatalf("credential = %+v", created.Credential)
	}
	c := o.connect(ctx, t, created.Token)
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A pane alone: the pane, not its environment or tab.
	if len(snap.Panes) != 1 || len(snap.Environments) != 0 || len(snap.Tabs) != 0 {
		t.Fatalf("pane-scoped snapshot = %+v", snap)
	}
	if _, err := c.Screen(ctx, observer.ScreenRequest{Pane: o.dev.ID}); code(err) != protocol.ErrorCodePermissionDenied {
		t.Fatalf("screen without screens access: %v", err)
	}
	stream, _, err := c.Events(ctx, observer.EventsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var list runtime.ObserverList
	if err := o.control.Call(ctx, "observer.list", nil, &list); err != nil || len(list.Credentials) != 1 || list.Credentials[0].Connections != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	var v runtime.ObserverValidation
	if err := o.control.Call(ctx, "observer.validate", map[string]string{"token": created.Token}, &v); err != nil || !v.Valid {
		t.Fatalf("validate = %+v, %v", v, err)
	}

	if err := o.control.Call(ctx, "observer.revoke", map[string]string{"id": created.Credential.ID}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the revoked observer's connection stayed open")
	}
	if _, err := stream.Next(); err == nil {
		t.Fatalf("the revoked observer's stream kept going: %v", err)
	}
	if _, err := observer.Connect(ctx, observer.ClientConfig{Socket: created.Socket, Token: created.Token}); code(err) != protocol.ErrorCodePermissionDenied {
		t.Fatalf("connecting with a revoked token: %v", err)
	}
	if err := o.control.Call(ctx, "observer.validate", map[string]string{"token": created.Token}, &v); err != nil || v.Valid || v.State != observer.StateRevoked {
		t.Fatalf("validate after revoke = %+v, %v", v, err)
	}

	var audit []observer.AuditEntry
	if err := o.control.Call(ctx, "observer.audit", map[string]any{"credential": created.Credential.ID}, &audit); err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range audit {
		actions = append(actions, e.Action)
	}
	want := []string{observer.AuditCreated, observer.AuditConnected, observer.AuditDenied, observer.AuditRevoked, observer.AuditRefused}
	got := strings.Join(actions, ",")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("audit lacks %s: %s", w, got)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(o.root, "observers", "audit.log"))
	if strings.Contains(string(raw), created.Token) {
		t.Fatal("the audit log holds the token")
	}
	creds, _ := os.ReadFile(filepath.Join(o.root, "observers", "credentials.json"))
	if strings.Contains(string(creds), strings.SplitN(created.Token, "_", 3)[2]) {
		t.Fatal("the credential store holds the token's secret")
	}
}

func waitFor(ctx context.Context, t *testing.T, ok func() bool) {
	t.Helper()
	for !ok() {
		if ctx.Err() != nil {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
