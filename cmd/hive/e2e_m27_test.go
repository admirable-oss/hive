//go:build !windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/runtime"
)

// memberSSH stands in for ssh on a teammate's machine, and for sshd on
// the runtime's: it finds the line holding the teammate's key in the
// runtime's authorized_keys and runs it as sshd would, its forced command
// with SSH_ORIGINAL_COMMAND set to what the client asked for.
const memberSSH = `#!/bin/sh
while [ $# -gt 0 ]; do
	case "$1" in --) shift; break ;; *) shift ;; esac
done
line=$(grep -F "$HIVE_TEST_MEMBER_KEY" "$HIVE_TEST_AUTHORIZED_KEYS" | head -n 1)
if [ -z "$line" ]; then
	echo "Permission denied (publickey)." >&2
	exit 255
fi
case "$line" in
command=*) ;;
*) exec sh -c "$1" ;;
esac
cmd=${line#command=\"}
cmd=${cmd%%\",restrict*}
SSH_ORIGINAL_COMMAND="$1" SSH_CONNECTION="198.51.100.4 50000 10.0.0.1 22" exec sh -c "$cmd"
`

// e2eKey writes a well-formed ed25519 public key file and returns its
// path and its base64 part.
func e2eKey(t *testing.T, dir, name string, seed byte) (string, string) {
	t.Helper()
	typ := "ssh-ed25519"
	blob := binary.BigEndian.AppendUint32(nil, uint32(len(typ)))
	blob = append(blob, typ...)
	blob = binary.BigEndian.AppendUint32(blob, 32)
	for i := range 32 {
		blob = append(blob, seed+byte(i))
	}
	b64 := base64.StdEncoding.EncodeToString(blob)
	path := filepath.Join(dir, name+".pub")
	if err := os.WriteFile(path, []byte(typ+" "+b64+" "+name+"@laptop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, b64
}

// TestM27Members: a teammate is invited with their key and a role, reaches
// the runtime over ssh only through the member socket, as themselves, and
// is held to it; revoking them removes their key and refuses them.
func TestM27Members(t *testing.T) {
	c := newCLI(t)
	keys := filepath.Join(c.home, "ssh", "authorized_keys")
	own, _ := e2eKey(t, c.home, "me", 7)
	ownLine, _ := os.ReadFile(own)
	if err := os.MkdirAll(filepath.Dir(keys), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keys, ownLine, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.cfg, []byte("[access]\nauthorized_keys = \""+keys+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("env", "create", "dev", "--managed")

	// Inviting needs a key, a role and where it applies, and a yes.
	alicePub, aliceB64 := e2eKey(t, c.home, "alice", 1)
	if _, errOut, code := c.run("access", "invite", "alice", "--key", alicePub); code != exitUsage || !strings.Contains(errOut, "--role") {
		t.Fatalf("invite without a role: %d %s", code, errOut)
	}
	if _, errOut, code := c.run("access", "invite", "alice", "--key", alicePub, "--role", "operator", "--env", "nope", "--yes"); code == 0 || !strings.Contains(errOut, "nope") {
		t.Fatalf("invite for a missing environment: %d %s", code, errOut)
	}
	if _, errOut, code := c.run("access", "invite", "alice", "--key", alicePub, "--role", "operator", "--env", "dev"); code != exitUsage || !strings.Contains(errOut, "not invited") {
		t.Fatalf("invite without a yes: %d %s", code, errOut)
	}
	// A key that already logs in unrestricted would never be held to a role.
	if _, errOut, code := c.run("access", "invite", "me", "--key", own, "--role", "observer", "--all", "--yes"); code == 0 || !strings.Contains(errOut, "without Hive's restriction") {
		t.Fatalf("invite of an unrestricted key: %d %s", code, errOut)
	}
	var invited runtime.AccessInvited
	decode(t, c.must("--json", "access", "invite", "alice", "--key", alicePub, "--role", "operator", "--env", "dev", "--yes"), &invited)
	alice := invited.Member
	if !access.ValidID(alice.ID) || alice.GrantsString() != "operator@dev" || invited.AuthorizedKeys != keys {
		t.Fatalf("invited = %+v", invited)
	}
	data, _ := os.ReadFile(keys)
	if !strings.HasPrefix(string(data), string(ownLine)) || !strings.Contains(string(data), invited.Line) ||
		!strings.Contains(invited.Line, "proxy --member "+alice.ID+`",restrict ssh-ed25519 `+aliceB64) {
		t.Fatalf("authorized_keys:\n%s", data)
	}
	if fi, err := os.Stat(keys); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("authorized_keys mode: %v %v", fi, err)
	}

	// A key printed for someone else to install leaves the file alone.
	bobPub, _ := e2eKey(t, c.home, "bob", 2)
	printed := c.must("access", "invite", "bob", "--key", bobPub, "--role", "observer", "--all", "--print")
	if !strings.Contains(printed, `command="`) || strings.Count(mustRead(t, keys), "hive:") != 1 {
		t.Fatalf("--print:\n%s\nauthorized_keys:\n%s", printed, mustRead(t, keys))
	}
	if list := c.must("access", "list"); !strings.Contains(list, "alice") || !strings.Contains(list, "operator@dev") || !strings.Contains(list, "observer@all") {
		t.Fatalf("list:\n%s", list)
	}
	if out := c.must("access", "whoami"); !strings.Contains(out, "owner") {
		t.Fatalf("whoami as the owner: %s", out)
	}

	// Alice, on her own machine, uses hive as usual; ssh gives her only
	// the member socket, as herself.
	ssh := filepath.Join(c.home, "member-ssh")
	if err := os.WriteFile(ssh, []byte(memberSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	mate := newCLI(t)
	mate.env = append(mate.env, "HIVE_SSH="+ssh, "HIVE_TEST_AUTHORIZED_KEYS="+keys, "HIVE_TEST_MEMBER_KEY="+aliceB64)
	var who runtime.WhoAmI
	decode(t, mate.must("--remote", "buildbox", "--json", "access", "whoami"), &who)
	if who.Owner || who.Member == nil || who.Member.ID != alice.ID || who.From != "198.51.100.4 50000" {
		t.Fatalf("whoami as alice = %+v", who)
	}
	// Until roles are enforced (RFC 0007, step 2), a member reaches nothing
	// else, and never what manages access.
	for _, args := range [][]string{{"env", "list"}, {"access", "list"}, {"access", "revoke", "alice"}} {
		if _, errOut, code := mate.run(append([]string{"--remote", "buildbox"}, args...)...); code == 0 || !strings.Contains(errOut, "may not call") {
			t.Fatalf("alice ran %v: %d %s", args, code, errOut)
		}
	}
	// Commands that run on the machine as a whole are refused by her key.
	if _, errOut, code := mate.run("--remote", "buildbox", "config", "path"); code == 0 || !strings.Contains(errOut, "reaches only Hive's member socket") {
		t.Fatalf("alice ran a command over ssh: %d %s", code, errOut)
	}

	audit := c.must("access", "audit", "--member", "alice")
	for _, want := range []string{"invited", "connected", "denied", "environment.list", "access.revoke", "198.51.100.4"} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit has no %q:\n%s", want, audit)
		}
	}

	// The doctor notices a missing line, and puts it back.
	if err := os.WriteFile(keys, ownLine, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, code := c.run("access", "doctor"); code != exitError || !strings.Contains(out, "alice has no line") {
		t.Fatalf("doctor: %d %s", code, out)
	}
	c.must("access", "doctor", "--fix")
	if !strings.Contains(mustRead(t, keys), invited.Line) {
		t.Fatalf("doctor --fix:\n%s", mustRead(t, keys))
	}

	// Roles change; the last one cannot be taken away.
	if out := c.must("access", "grant", "alice", "--all", "--role", "observer"); !strings.Contains(out, "observer@all, operator@dev") {
		t.Fatalf("grant: %s", out)
	}
	if _, errOut, code := c.run("access", "grant", "bob", "--all", "--role", "none"); code == 0 || !strings.Contains(errOut, "last grant") {
		t.Fatalf("taking the last grant: %d %s", code, errOut)
	}

	// Revoking removes her key and refuses her.
	c.must("access", "revoke", "alice")
	if data := mustRead(t, keys); strings.Contains(data, aliceB64) || !strings.HasPrefix(data, string(ownLine)) {
		t.Fatalf("authorized_keys after revoke:\n%s", data)
	}
	if _, _, code := mate.run("--remote", "buildbox", "access", "whoami"); code == 0 {
		t.Fatal("a revoked member still connects")
	}
	// Even with her line put back by hand, the daemon refuses her.
	if err := os.WriteFile(keys, []byte(string(ownLine)+invited.Line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := mate.run("--remote", "buildbox", "access", "whoami"); code == 0 || !strings.Contains(errOut, "revoked") {
		t.Fatalf("a revoked member with her line back: %d %s", code, errOut)
	}
	if show := c.must("access", "show", "alice"); !strings.Contains(show, "revoked") {
		t.Fatalf("show:\n%s", show)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
