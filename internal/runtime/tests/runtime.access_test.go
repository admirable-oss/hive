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
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/runtime"
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
