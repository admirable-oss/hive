//go:build !windows

package runtime_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/runtime"
	"github.com/admirable-oss/hive/internal/task"
)

func memberKey(seed byte) string {
	typ := "ssh-ed25519"
	blob := binary.BigEndian.AppendUint32(nil, uint32(len(typ)))
	blob = append(blob, typ...)
	blob = binary.BigEndian.AppendUint32(blob, 32)
	for i := range 32 {
		blob = append(blob, seed+byte(i))
	}
	return typ + " " + base64.StdEncoding.EncodeToString(blob)
}

// member invites name with grants (its line printed, not installed) and
// returns a client of the member socket bound to it, as `hive proxy
// --member` binds one.
func (o *observed) member(ctx context.Context, t *testing.T, name string, seed byte, grants ...access.Grant) client.Client {
	t.Helper()
	var res runtime.AccessInvited
	if err := o.control.Call(ctx, "access.invite", runtime.AccessInviteParams{Name: name, Key: memberKey(seed), Grants: grants, Print: true}, &res); err != nil {
		t.Fatal(err)
	}
	c := client.NewService(client.Config{Name: name, Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "unix", runtime.MemberSocketPath(o.root))
		if err != nil {
			return nil, err
		}
		line, _ := json.Marshal(access.Binding{Member: res.Member.ID, From: "192.0.2.1 4000"})
		if _, err := conn.Write(append(line, '\n')); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestMemberViewsTypeOnlyWhenAllowed: a frames view opened by an observer
// drops what it types; one opened by an owner of the environment takes
// it. What the observer reads carries no secrets.
func TestMemberViewsTypeOnlyWhenAllowed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o := newObserved(ctx, t)
	bob := o.member(ctx, t, "bob", 1, access.Grant{Environment: access.AllEnvironments, Role: access.RoleObserver})
	carol := o.member(ctx, t, "carol", 2, access.Grant{Environment: "dev", Role: access.RoleOwner})

	watch, err := bob.TerminalFrames(ctx, client.ViewRequest{ProcessID: o.dev.ProcessID})
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if _, err := watch.Write([]byte("echo leaked-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	drive, err := carol.TerminalFrames(ctx, client.ViewRequest{ProcessID: o.dev.ProcessID})
	if err != nil {
		t.Fatal(err)
	}
	defer drive.Close()
	if _, err := drive.Write([]byte("echo typed-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	var screen string
	for deadline := time.Now().Add(10 * time.Second); ; {
		snap, err := o.control.TerminalSnapshot(ctx, client.SnapshotRequest{ProcessID: o.dev.ProcessID})
		if err != nil {
			t.Fatal(err)
		}
		screen = strings.Join(snap.Lines, "\n")
		if strings.Contains(screen, "typed-42") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the owner's input never arrived:\n%s", screen)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(screen, "leaked") {
		t.Fatalf("an observer's input reached the shell:\n%s", screen)
	}

	// Carol owns dev alone: the other environment does not exist for her.
	var envs []map[string]any
	if err := carol.Call(ctx, "environment.list", nil, &envs); err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0]["id"] != "dev" || !strings.Contains(mustJSON(t, envs), "s3cr3t-dev") {
		t.Fatalf("carol's environments: %v", envs)
	}
	if err := carol.Call(ctx, "pane.get", map[string]string{"id": o.other.ID}, nil); code(err) != "not_found" {
		t.Fatalf("carol reading the other environment's pane: %v", err)
	}

	// Bob reads everything, and no secret.
	var snap map[string]any
	if err := bob.Call(ctx, "api.snapshot", nil, &snap); err != nil {
		t.Fatal(err)
	}
	if raw := mustJSON(t, snap); strings.Contains(raw, "s3cr3t") || !strings.Contains(raw, o.other.ID) {
		t.Fatalf("bob's snapshot:\n%s", raw)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// screenOf is what process shows now.
func (o *observed) screenOf(ctx context.Context, t *testing.T, process string) string {
	t.Helper()
	snap, err := o.control.TerminalSnapshot(ctx, client.SnapshotRequest{ProcessID: process})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(snap.Lines, "\n")
}

func (o *observed) waitScreen(ctx context.Context, t *testing.T, process, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		screen := o.screenOf(ctx, t, process)
		if strings.Contains(screen, want) {
			return screen
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q never showed:\n%s", want, screen)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestOneTypistPerTerminal: while a member holds a pane's input, the
// runtime's own user's input there is refused, whether typed through a
// call or a frames view; released, or once the member is revoked, it goes
// through.
func TestOneTypistPerTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o := newObserved(ctx, t)
	carol := o.member(ctx, t, "carol", 3, access.Grant{Environment: "dev", Role: access.RoleOwner})

	var claimed runtime.PaneClaimed
	if err := carol.Call(ctx, "pane.claim", map[string]string{"pane": o.dev.ID}, &claimed); err != nil || claimed.Holder.Name != "carol" {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if err := carol.TerminalInput(ctx, o.dev.ProcessID, []byte("echo carol-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	o.waitScreen(ctx, t, o.dev.ProcessID, "carol-42")

	// The owner's input is refused while carol holds it, and says who does.
	err := o.control.TerminalInput(ctx, o.dev.ProcessID, []byte("echo owner-$((6*7))\n"))
	if code(err) != "conflict" || !strings.Contains(err.Error(), "carol is typing") {
		t.Fatalf("the owner typing over carol: %v", err)
	}
	view, err := o.control.TerminalFrames(ctx, client.ViewRequest{ProcessID: o.dev.ProcessID})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if _, err := view.Write([]byte("echo frames-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	var list runtime.PresenceList
	if err := o.control.Call(ctx, "presence.list", nil, &list); err != nil || len(list.Holds) != 1 || list.Holds[0].Holder.Name != "carol" || list.Holds[0].Pane != o.dev.ID {
		t.Fatalf("holds: %+v %v", list.Holds, err)
	}

	// Released, the owner types again.
	var rel map[string]bool
	if err := carol.Call(ctx, "pane.release", map[string]string{"pane": o.dev.ID}, &rel); err != nil || !rel["released"] {
		t.Fatalf("release: %v %v", rel, err)
	}
	if err := o.control.TerminalInput(ctx, o.dev.ProcessID, []byte("echo owner-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	screen := o.waitScreen(ctx, t, o.dev.ProcessID, "owner-42")
	if strings.Contains(screen, "frames-42") {
		t.Fatalf("frames input went through while carol held the pane:\n%s", screen)
	}

	// The owner holds it now; carol may take it back with --force, as an
	// owner of dev.
	if err := carol.TerminalInput(ctx, o.dev.ProcessID, []byte("x")); code(err) != "conflict" {
		t.Fatalf("carol typing over the owner: %v", err)
	}
	if err := carol.Call(ctx, "pane.claim", map[string]any{"pane": o.dev.ID, "force": true}, &claimed); err != nil || claimed.Previous == nil || claimed.Previous.ID != "owner" {
		t.Fatalf("force: %+v %v", claimed, err)
	}

	// Revoking carol lets go of what she held.
	var revoked access.Member
	if err := o.control.Call(ctx, "access.revoke", map[string]string{"member": "carol"}, &revoked); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		if err := o.control.Call(ctx, "presence.list", nil, &list); err == nil && len(list.Holds) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a revoked member still holds: %+v", list.Holds)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := o.control.TerminalInput(ctx, o.dev.ProcessID, []byte("echo after-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	o.waitScreen(ctx, t, o.dev.ProcessID, "after-42")
}

// TestMembersFocusIsTheirOwn: a member moving focus moves their presence,
// not the session's focus; presence ends with the connection.
func TestMembersFocusIsTheirOwn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o := newObserved(ctx, t)
	second, err := o.mod.Panes.Split(ctx, pane.SplitRequest{Pane: o.dev.ID, Direction: "right"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.mod.Panes.Focus(ctx, o.dev.ID); err != nil {
		t.Fatal(err)
	}
	bob := o.member(ctx, t, "bob", 4, access.Grant{Environment: "dev", Role: access.RoleObserver})
	var got map[string]any
	if err := bob.Call(ctx, "pane.focus", map[string]string{"id": second.ID}, &got); err != nil || got["id"] != second.ID {
		t.Fatalf("bob focusing: %v %v", got, err)
	}
	tab, err := o.mod.Panes.Tab(ctx, o.devTab.ID)
	if err != nil || tab.Focused != o.dev.ID {
		t.Fatalf("bob moved the session's focus: %+v %v", tab, err)
	}
	// Back by direction, still his own.
	if err := bob.Call(ctx, "pane.focus", map[string]string{"id": second.ID, "direction": "left"}, &got); err != nil || got["id"] != o.dev.ID {
		t.Fatalf("bob focusing left: %v %v", got, err)
	}
	var list runtime.PresenceList
	if err := o.control.Call(ctx, "presence.list", nil, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Presence) != 1 || list.Presence[0].Person.Name != "bob" || list.Presence[0].Pane != o.dev.ID || list.Presence[0].Environment != "dev" {
		t.Fatalf("presence: %+v", list.Presence)
	}
	// The other environment does not exist for bob, there either.
	if err := bob.Call(ctx, "pane.focus", map[string]string{"id": o.other.ID}, nil); code(err) != "not_found" {
		t.Fatalf("bob focusing elsewhere: %v", err)
	}
	_ = bob.Close()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if err := o.control.Call(ctx, "presence.list", nil, &list); err == nil && len(list.Presence) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bob left but is still present: %+v", list.Presence)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestHandingOver: what a member starts is theirs, events name who caused
// them, and agents and tasks change hands only to operators and owners of
// their environment, given by an owner or by whoever owns them.
func TestHandingOver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o := newObserved(ctx, t)
	carol := o.member(ctx, t, "carol", 3, access.Grant{Environment: access.AllEnvironments, Role: access.RoleOwner})
	alice := o.member(ctx, t, "alice", 4, access.Grant{Environment: "dev", Role: access.RoleOperator})
	o.member(ctx, t, "bob", 5, access.Grant{Environment: "dev", Role: access.RoleObserver})
	o.member(ctx, t, "dave", 6, access.Grant{Environment: "other", Role: access.RoleOperator})
	events, err := o.control.Events(ctx, "tab.created", runtime.EventAgentHandover)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	next := func(typ string) event.Event {
		t.Helper()
		for {
			ev, err := events.Next()
			if err != nil {
				t.Fatal(err)
			}
			if ev.Type == typ {
				return ev
			}
		}
	}

	// Carol's tab, and its process, are hers; the event says she made it.
	var made struct {
		Pane pane.Pane `json:"pane"`
	}
	if err := carol.Call(ctx, "tab.create", map[string]string{"environment_id": "dev", "name": "carols"}, &made); err != nil {
		t.Fatal(err)
	}
	if ev := next("tab.created"); ev.Actor == nil || ev.Actor.Name != "carol" {
		t.Fatalf("tab.created actor: %+v", ev.Actor)
	}
	var a agent.Agent
	if err := o.control.Call(ctx, "agent.get", map[string]string{"id": made.Pane.ProcessID}, &a); err != nil || a.Owner == nil || a.Owner.Name != "carol" {
		t.Fatalf("carol's agent: %+v %v", a.Owner, err)
	}
	var mine agent.Agent
	if err := o.control.Call(ctx, "agent.get", map[string]string{"id": o.dev.ProcessID}, &mine); err != nil || mine.Owner != nil {
		t.Fatalf("the owner's agent: %+v %v", mine.Owner, err)
	}

	// An operator gives away only what is hers; work goes only to
	// operators and owners of its environment.
	handover := func(c client.Client, id, to string) (runtime.AgentHandedOver, error) {
		var res runtime.AgentHandedOver
		err := c.Call(ctx, "agent.handover", map[string]string{"id": id, "to": to}, &res)
		return res, err
	}
	if _, err := handover(alice, made.Pane.ProcessID, "alice"); code(err) != "permission_denied" {
		t.Fatalf("alice taking carol's agent: %v", err)
	}
	for _, to := range []string{"bob", "dave"} {
		if _, err := handover(carol, made.Pane.ProcessID, to); code(err) != "invalid_params" {
			t.Fatalf("handing over to %s: %v", to, err)
		}
	}
	if _, err := handover(carol, made.Pane.ProcessID, "nobody"); code(err) != "not_found" {
		t.Fatalf("handing over to nobody: %v", err)
	}
	if err := carol.Call(ctx, "pane.claim", map[string]string{"pane": made.Pane.ID}, nil); err != nil {
		t.Fatal(err)
	}
	res, err := handover(carol, made.Pane.ProcessID, "alice")
	if err != nil || res.Agent.Owner == nil || res.Agent.Owner.Name != "alice" || res.From == nil || res.From.Name != "carol" {
		t.Fatalf("carol to alice: %+v %v", res, err)
	}
	var list runtime.PresenceList
	if err := o.control.Call(ctx, "presence.list", nil, &list); err != nil || len(list.Holds) != 1 || list.Holds[0].Holder.Name != "alice" {
		t.Fatalf("the pane's input after the handover: %+v %v", list.Holds, err)
	}
	if ev := next(runtime.EventAgentHandover); ev.Actor == nil || ev.Actor.Name != "carol" {
		t.Fatalf("agent.handover actor: %+v", ev.Actor)
	}
	// Hers now, alice gives it back to the runtime's owner.
	if res, err := handover(alice, made.Pane.ProcessID, "owner"); err != nil || res.Agent.Owner != nil {
		t.Fatalf("alice to owner: %+v %v", res, err)
	}

	// Plans: carol's plan and tasks are hers; she assigns one to alice.
	var plan task.Plan
	run := map[string]any{"plan": map[string]any{"tasks": []map[string]any{
		{"id": "fix", "prompt": "fix it", "kind": "claude", "environment_id": "dev", "approve": true},
	}}}
	if err := carol.Call(ctx, "task.run", run, &plan); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.control.Call(context.Background(), "task.cancel", map[string]string{"id": plan.ID}, nil) }()
	if plan.Owner == nil || plan.Owner.Name != "carol" || plan.Tasks[0].Owner == nil || plan.Tasks[0].Owner.Name != "carol" {
		t.Fatalf("carol's plan: %+v / %+v", plan.Owner, plan.Tasks[0].Owner)
	}
	assign := map[string]string{"id": plan.ID, "task": "fix", "to": "alice"}
	if err := alice.Call(ctx, "task.assign", assign, nil); code(err) != "permission_denied" {
		t.Fatalf("alice taking carol's task: %v", err)
	}
	if err := carol.Call(ctx, "task.assign", assign, &plan); err != nil {
		t.Fatal(err)
	}
	fix := plan.Tasks[0]
	if fix.Owner == nil || fix.Owner.Name != "alice" || plan.Owner.Name != "carol" || !strings.Contains(fix.Log[len(fix.Log)-1].Message, "assigned to alice by carol") {
		t.Fatalf("assigned: %+v %+v", fix.Owner, fix.Log)
	}
	// An operator in dev decides on the task waiting there, which runs in
	// dev though it has not started.
	if err := alice.Call(ctx, "task.approve", map[string]any{"id": plan.ID, "task": "fix", "deny": true}, &plan); err != nil || plan.Tasks[0].State != task.StateCancelled {
		t.Fatalf("alice deciding on her task: %v", err)
	}
}
