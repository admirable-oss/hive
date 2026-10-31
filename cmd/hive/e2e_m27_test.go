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
	// A member never reaches what manages access, whatever its role.
	for _, args := range [][]string{{"access", "list"}, {"access", "revoke", "alice"}} {
		if _, errOut, code := mate.run(append([]string{"--remote", "buildbox"}, args...)...); code == 0 || !strings.Contains(errOut, "may not call") {
			t.Fatalf("alice ran %v: %d %s", args, code, errOut)
		}
	}
	// Commands that run on the machine as a whole are refused by her key.
	if _, errOut, code := mate.run("--remote", "buildbox", "config", "path"); code == 0 || !strings.Contains(errOut, "reaches only Hive's member socket") {
		t.Fatalf("alice ran a command over ssh: %d %s", code, errOut)
	}

	audit := c.must("access", "audit", "--member", "alice")
	for _, want := range []string{"invited", "connected", "denied", "access.list", "access.revoke", "198.51.100.4"} {
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

// teamRuntime is a runtime whose members' keys go into its own
// authorized_keys, with a fake ssh to reach it as a member, and /bin/sh as
// its shell.
type teamRuntime struct {
	*cli
	keys, ssh string
}

func newTeamRuntime(t *testing.T) *teamRuntime {
	t.Helper()
	c := newCLI(t)
	r := &teamRuntime{cli: c, keys: filepath.Join(c.home, "ssh", "authorized_keys"), ssh: filepath.Join(c.home, "member-ssh")}
	cfg := "[terminal]\nshell = \"/bin/sh\"\n\n[access]\nauthorized_keys = \"" + r.keys + "\"\n"
	if err := os.WriteFile(c.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.ssh, []byte(memberSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

// invite lets name in with role where (an environment, or "--all") and
// returns a CLI acting as them from their own machine.
func (r *teamRuntime) invite(name string, seed byte, role, where string) *cli {
	r.t.Helper()
	pub, b64 := e2eKey(r.t, r.home, name, seed)
	scope := []string{"--env", where}
	if where == "--all" {
		scope = []string{"--all"}
	}
	r.must(append([]string{"access", "invite", name, "--key", pub, "--role", role, "--yes"}, scope...)...)
	mate := newCLI(r.t)
	mate.env = append(mate.env, "HIVE_SSH="+r.ssh, "HIVE_TEST_AUTHORIZED_KEYS="+r.keys, "HIVE_TEST_MEMBER_KEY="+b64)
	return mate
}

// remote runs a member's command against the runtime.
func asMember(args ...string) []string { return append([]string{"--remote", "buildbox"}, args...) }

// TestM27Roles: an operator prompts an agent but starts nothing and types
// into no shell; an observer only reads; both see only their environments,
// and records without their secrets.
func TestM27Roles(t *testing.T) {
	r := newTeamRuntime(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	r.env = append(r.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.must("env", "create", "dev", "--managed", "-e", "DEV_TOKEN=s3cret")
	r.must("env", "create", "ops", "--managed", "-e", "OPS_TOKEN=s3cret")
	agentPane := strings.TrimSpace(r.must("agent", "start", "--kind", "claude", "--env", "dev", "--name", "helper"))
	shellPane := strings.Fields(r.must("tab", "create", "dev", "--name", "sh"))[1]
	r.must("ps", "start", "ops", "--", "sh", "-c", "sleep 600")
	r.must("ps", "start", "dev", "--", "sh", "-c", "sleep 600 # --password=hunter2")

	alice := r.invite("alice", 1, "operator", "dev")
	bob := r.invite("bob", 2, "observer", "--all")

	// Alice sees dev alone, without its secrets.
	if out := alice.must(asMember("--json", "env", "list")...); !strings.Contains(out, `"dev"`) || strings.Contains(out, `"ops"`) || strings.Contains(out, "s3cret") {
		t.Fatalf("alice's environments:\n%s", out)
	}
	if _, errOut, code := alice.run(asMember("env", "get", "ops")...); code == 0 || !strings.Contains(errOut, "not found") {
		t.Fatalf("alice reading ops: %d %s", code, errOut)
	}
	procs := alice.must(asMember("--json", "ps", "list")...)
	// (The CLI prints what it decoded: what was left out shows as empty.)
	for _, bad := range []string{"OPS_TOKEN", "DEV_TOKEN", "s3cret", "hunter2", `"environment_id": "ops"`, `"working_dir": "/`} {
		if strings.Contains(procs, bad) {
			t.Fatalf("alice's process list has %s:\n%s", bad, procs)
		}
	}
	if !strings.Contains(procs, `"command"`) {
		t.Fatalf("alice's process list has no programs:\n%s", procs)
	}

	// She prompts the agent and reads it.
	alice.must(asMember("agent", "prompt", agentPane, "write the tests")...)
	r.waitFor("the agent to take alice's prompt", func() bool { // the fake works on any prompt
		return strings.Contains(r.must("agent", "read", agentPane), "Osmosing")
	})
	if out := alice.must(asMember("agent", "read", agentPane)...); !strings.Contains(out, "Osmosing") {
		t.Fatalf("alice reading the agent:\n%s", out)
	}

	// She starts nothing, and types into no shell.
	refused := map[string][]string{
		"agent.start":    {"agent", "start", "--kind", "claude", "--env", "dev"},
		"process.start":  {"ps", "start", "dev", "--", "true"},
		"pane.split":     {"pane", "split", agentPane},
		"tab.create":     {"tab", "create", "dev"},
		"pane.send_text": {"pane", "send-text", shellPane, "echo hi"},
	}
	for method, args := range refused {
		_, errOut, code := alice.run(asMember(args...)...)
		if code == 0 || !strings.Contains(errOut, "may not call "+method) {
			t.Fatalf("alice ran %s: %d %s", method, code, errOut)
		}
	}
	if _, errOut, _ := alice.run(asMember("pane", "send-text", shellPane, "echo hi")...); !strings.Contains(errOut, "never into a shell") {
		t.Fatalf("typing into a shell: %s", errOut)
	}

	// Bob reads everything, and changes nothing.
	if out := bob.must(asMember("env", "list")...); !strings.Contains(out, "dev") || !strings.Contains(out, "ops") {
		t.Fatalf("bob's environments:\n%s", out)
	}
	if procs := bob.must(asMember("--json", "ps", "list")...); strings.Contains(procs, "s3cret") {
		t.Fatalf("bob's process list has a secret:\n%s", procs)
	}
	if _, errOut, code := bob.run(asMember("agent", "prompt", agentPane, "hello")...); code == 0 || !strings.Contains(errOut, "may not call agent.prompt") {
		t.Fatalf("bob prompting: %d %s", code, errOut)
	}

	// The owner still sees everything as it is.
	if env := r.must("--json", "env", "get", "dev"); !strings.Contains(env, "s3cret") {
		t.Fatalf("the owner's environment lost its variables:\n%s", env)
	}
	if procs := r.must("--json", "ps", "list"); !strings.Contains(procs, "hunter2") {
		t.Fatalf("the owner's process list lost its arguments:\n%s", procs)
	}
	if env := alice.must(asMember("--json", "env", "get", "dev")...); strings.Contains(env, "s3cret") {
		t.Fatalf("alice reads dev's variables:\n%s", env)
	}

	var entries []access.AuditEntry
	audit := r.must("--json", "access", "audit", "--member", "alice")
	decode(t, audit, &entries)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action+" "+e.Method+" "+e.Outcome] = true
	}
	for _, want := range []string{"action agent.prompt ok", "denied agent.start permission_denied", "denied pane.send_text permission_denied", "denied environment.get not_found"} {
		if !seen[want] {
			t.Fatalf("alice's audit has no %q:\n%s", want, audit)
		}
	}
	if strings.Contains(audit, "write the tests") {
		t.Fatal("the audit holds what alice typed")
	}
}

// TestM27OneTypistAtATime: a member typing into an agent's pane holds it;
// the owner's prompt is refused, naming her, until she lets go.
func TestM27OneTypistAtATime(t *testing.T) {
	r := newTeamRuntime(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	r.env = append(r.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.must("env", "create", "dev", "--managed")
	agentPane := strings.TrimSpace(r.must("agent", "start", "--kind", "claude", "--env", "dev", "--name", "helper"))
	alice := r.invite("alice", 1, "operator", "dev")

	if out := alice.must(asMember("pane", "claim", agentPane)...); !strings.Contains(out, "alice") {
		t.Fatalf("claim: %s", out)
	}
	if _, errOut, code := r.run("agent", "prompt", agentPane, "mine now"); code == 0 || !strings.Contains(errOut, "alice is typing") {
		t.Fatalf("the owner prompting over alice: %d %s", code, errOut)
	}
	if out := r.must("presence"); !strings.Contains(out, "TYPING") || !strings.Contains(out, "alice") || !strings.Contains(out, agentPane) {
		t.Fatalf("presence:\n%s", out)
	}
	alice.must(asMember("pane", "release", agentPane)...)
	r.must("agent", "prompt", agentPane, "mine now")
	if out := r.must("presence"); !strings.Contains(out, "TYPING") {
		t.Fatalf("the owner's prompt does not hold the pane:\n%s", out)
	}
	if _, errOut, code := alice.run(asMember("agent", "prompt", agentPane, "hers")...); code == 0 || !strings.Contains(errOut, "is typing") {
		t.Fatalf("alice prompting over the owner: %d %s", code, errOut)
	}
}
