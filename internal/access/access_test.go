package access

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

// testKey makes a well-formed ed25519 public key.
func testKey(t *testing.T, seed byte) PublicKey {
	t.Helper()
	typ := "ssh-ed25519"
	blob := binary.BigEndian.AppendUint32(nil, uint32(len(typ)))
	blob = append(blob, typ...)
	blob = binary.BigEndian.AppendUint32(blob, 32)
	for i := range 32 {
		blob = append(blob, seed+byte(i))
	}
	k, err := ParseKey(typ + " " + base64.StdEncoding.EncodeToString(blob) + " someone@laptop")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseKey(t *testing.T) {
	k := testKey(t, 1)
	if k.Comment != "someone@laptop" || !strings.HasPrefix(k.Fingerprint(), "SHA256:") {
		t.Fatalf("key = %+v, fingerprint %s", k, k.Fingerprint())
	}
	for _, bad := range []string{
		"", "ssh-ed25519", "ssh-dss AAAA", "ssh-ed25519 !!!",
		"ssh-rsa " + strings.Fields(k.String())[1], // the blob says ed25519
		`command="x" ` + k.String(),
	} {
		if _, err := ParseKey(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseKey(%q) = %v, want invalid", bad, err)
		}
	}
}

func TestRoles(t *testing.T) {
	m := Member{Grants: []Grant{{Environment: AllEnvironments, Role: RoleObserver}, {Environment: "web", Role: RoleOperator}}}
	if m.RoleIn("web") != RoleOperator || m.RoleIn("api") != RoleObserver || m.RoleIn("") != RoleObserver {
		t.Fatalf("roles: web %s, api %s", m.RoleIn("web"), m.RoleIn("api"))
	}
	if !RoleOwner.AtLeast(RoleOperator) || RoleObserver.AtLeast(RoleOperator) || RoleNone.AtLeast(RoleNone) {
		t.Fatal("role order")
	}
	if _, err := ParseRole("admin"); err == nil {
		t.Fatal("admin is not a role")
	}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Now()
	s.now = func() time.Time { return now }
	key := testKey(t, 1)
	m, err := s.Invite(InviteRequest{Name: "alice", Key: key, Grants: []Grant{{Environment: "web", Role: RoleOperator}}, Managed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(m.ID) || m.State != StateActive || m.Fingerprint != key.Fingerprint() || !m.ExpiresAt.Equal(m.CreatedAt.Add(DefaultTTL)) {
		t.Fatalf("member = %+v", m)
	}
	if _, err := s.Invite(InviteRequest{Name: "alice", Key: testKey(t, 2), Grants: m.Grants}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same name: %v", err)
	}
	if _, err := s.Invite(InviteRequest{Name: "bob", Key: key, Grants: m.Grants}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key: %v", err)
	}
	for _, req := range []InviteRequest{
		{Name: "bad name", Key: key, Grants: m.Grants},
		{Name: "carol", Grants: m.Grants},
		{Name: "carol", Key: testKey(t, 3)},
		{Name: "carol", Key: testKey(t, 3), Grants: []Grant{{Environment: "web", Role: "admin"}}},
		{Name: "carol", Key: testKey(t, 3), Grants: m.Grants, TTL: time.Minute},
	} {
		if _, err := s.Invite(req); !errors.Is(err, ErrInvalid) {
			t.Errorf("Invite(%+v) = %v, want invalid", req, err)
		}
	}

	g, err := s.Grant("alice", AllEnvironments, RoleObserver)
	if err != nil || g.GrantsString() != "observer@all, operator@web" {
		t.Fatalf("grant: %v %s", err, g.GrantsString())
	}
	if _, err := s.Grant(m.ID, "web", RoleNone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(m.ID, AllEnvironments, RoleNone); !errors.Is(err, ErrInvalid) {
		t.Fatalf("taking the last grant: %v", err)
	}

	// A fresh store reads what was saved.
	again := NewStore(dir)
	again.now = s.now
	got, err := again.Get("alice")
	if err != nil || got.ID != m.ID || got.GrantsString() != "observer@all" {
		t.Fatalf("reloaded: %+v %v", got, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "members.json")); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("members.json: %v %v", fi, err)
	}

	if _, err := s.Check(m.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(DefaultTTL)
	if _, err := s.Check(m.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := s.Renew("alice", 0); !errors.Is(err, ErrExpired) {
		t.Fatalf("renewing an expired member: %v", err)
	}
	now = now.Add(-time.Hour)
	if r, err := s.Renew("alice", 48*time.Hour); err != nil || !r.ExpiresAt.After(now.Add(47*time.Hour)) {
		t.Fatalf("renew: %+v %v", r, err)
	}
	if _, err := s.Revoke("alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(m.ID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	// A revoked member's name and key are free again.
	if _, err := s.Invite(InviteRequest{Name: "alice", Key: key, Grants: m.Grants}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check("m_0000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestKeysFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "authorized_keys")
	own := testKey(t, 9).String() + " me@desk"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(own+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := KeysFile{Path: path}
	m := Member{ID: "m_0123456789", Name: "alice", Key: testKey(t, 1).String()}
	line := AuthorizedLine(`'/opt/hive bin/hive' proxy --member`, m)
	if want := `command="'/opt/hive bin/hive' proxy --member m_0123456789",restrict ` + m.Key + " hive:alice"; line != want {
		t.Fatalf("line =\n%s\nwant\n%s", line, want)
	}
	if err := f.Write([]string{line}); err != nil {
		t.Fatal(err)
	}
	managed, others, err := f.Read()
	if err != nil || len(managed) != 1 || managed[0] != line || len(others) != 1 || others[0].String() != testKey(t, 9).String() {
		t.Fatalf("read: %q %v %v", managed, others, err)
	}
	// Rewriting keeps the user's own lines, in place.
	if err := f.Write([]string{line, line + "2"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), own+"\n"+blockBegin+"\n") || strings.Count(string(data), blockBegin) != 1 {
		t.Fatalf("file:\n%s", data)
	}
	if err := f.Write(nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != own+"\n" {
		t.Fatalf("after removing the block:\n%s", data)
	}
	// A block whose markers were edited is left alone.
	broken := own + "\n" + blockBegin + "\n" + line + "\nanother@key\n"
	_ = os.WriteFile(path, []byte(broken), 0o600)
	if err := f.Write([]string{line}); !errors.Is(err, ErrBrokenBlock) {
		t.Fatalf("broken block: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != broken {
		t.Fatal("a broken file was changed")
	}
}

func TestCheckOriginal(t *testing.T) {
	for _, ok := range []string{
		`h=$(command -v hive 2>/dev/null) || h="$HOME/.local/bin/hive"; exec "$h" proxy`,
		`h=$(command -v hive 2>/dev/null) || h="$HOME/.local/bin/hive"; exec "$h" --session dev proxy`,
		`h=/opt/hive; exec "$h" proxy`,
		`hive proxy`,
		`/usr/local/bin/hive --session=x proxy`,
	} {
		if err := CheckOriginal(ok); err != nil {
			t.Errorf("CheckOriginal(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "bash", "hive proxy --observer", `exec "$h" daemon restart`, `exec "$h" version --json`,
		"hive proxy; rm -rf ~", "cat ~/.ssh/id_ed25519",
	} {
		if err := CheckOriginal(bad); !errors.Is(err, ErrNotProxy) {
			t.Errorf("CheckOriginal(%q) = %v, want refused", bad, err)
		}
	}
}

// echo answers every request with the caller it was made by.
type echo struct{}

func (echo) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	c, _ := CallerOf(ctx)
	raw, _ := json.Marshal(map[string]string{"member": c.Member.Name, "from": c.From})
	return protocol.Response{Version: protocol.Version, Type: protocol.MessageTypeResponse, ID: req.ID, Result: raw}
}

func TestServer(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	audit, err := OpenAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	m, err := store.Invite(InviteRequest{Name: "alice", Key: testKey(t, 1), Grants: []Grant{{Environment: AllEnvironments, Role: RoleObserver}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{Store: store, Audit: audit, Handler: echo{}})

	dial := func(b Binding) *protocol.Stream {
		t.Helper()
		client, server := net.Pipe()
		go srv.Serve(context.Background(), server, protocol.ServerInfo{Version: "test"})
		line, _ := json.Marshal(b)
		if _, err := client.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return protocol.NewStream(client, protocol.DefaultMaxMessageSize)
	}
	call := func(st *protocol.Stream, method string) (*protocol.Response, error) {
		t.Helper()
		if err := st.Send(protocol.Request{Version: protocol.Version, Type: protocol.MessageTypeRequest, ID: method, Method: method}); err != nil {
			return nil, err
		}
		var resp protocol.Response
		if err := st.Receive(&resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}

	st := dial(Binding{Member: m.ID, From: "203.0.113.7 51122"})
	resp, err := call(st, "access.whoami")
	if err != nil || resp.Error != nil || !strings.Contains(string(resp.Result), `"member":"alice"`) || !strings.Contains(string(resp.Result), "203.0.113.7") {
		t.Fatalf("whoami: %+v %v", resp, err)
	}
	if resp, _ := call(st, "agent.start"); resp == nil || resp.Error == nil || resp.Error.Code != protocol.ErrorCodePermissionDenied {
		t.Fatalf("agent.start: %+v", resp)
	}
	if n := srv.Connections()[m.ID]; n != 1 {
		t.Fatalf("connections = %d", n)
	}

	// Revoking ends the connection at once.
	if _, err := store.Revoke(m.ID); err != nil {
		t.Fatal(err)
	}
	srv.Revoked(m.ID)
	if _, err := call(st, "access.whoami"); err == nil {
		t.Fatal("a revoked member's connection still answers")
	}

	// A revoked member is told why, and gets nothing.
	st = dial(Binding{Member: m.ID})
	resp, err = call(st, "access.whoami")
	if err != nil || resp.Error == nil || !strings.Contains(resp.Error.Message, "revoked") {
		t.Fatalf("revoked member: %+v %v", resp, err)
	}

	entries, err := audit.Read(0, "alice")
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	if got := strings.Join(actions, ","); got != "connected,denied,disconnected,refused" {
		t.Fatalf("audit = %s", got)
	}
}
