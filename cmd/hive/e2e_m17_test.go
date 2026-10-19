//go:build !windows

package main

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/discovery"
	"github.com/admirable-oss/hive/internal/remote"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// freeUDP returns a loopback address with a free UDP port.
func freeUDP(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().String()
}

// onHost runs a shell script on a fake host, as its user.
func (c *cli) onHost(root, host, script string) string {
	c.t.Helper()
	cmd := exec.Command(filepath.Join(c.home, "fake-ssh"), host, "--", script)
	cmd.Env = append(os.Environ(), "HIVE_FAKE_SSH_ROOT="+root, "HIVE_CLI_TEST_EXEC=1", "GORACE=atexit_sleep_ms=0")
	out, err := cmd.Output()
	if err != nil {
		c.t.Fatalf("on %s: %s: %v", host, script, err)
	}
	return string(out)
}

// machineList is `hive machine list --json`, decoded afresh.
func (c *cli) machineList(args ...string) []machineEntry {
	c.t.Helper()
	var list []machineEntry
	decode(c.t, c.must(append([]string{"--json", "machine", "list"}, args...)...), &list)
	return list
}

type discoverOutput struct {
	Runtimes []nearbyRuntime `json:"runtimes"`
	Asked    []string        `json:"asked"`
}

// TestM17DiscoverPairAndTrust: a runtime that announces itself is found
// with its identity, paired only after proving that identity and being
// confirmed, refused once it proves another, and trusted again only when
// accepted. Multicast is not available in CI, so the runtime answers on a
// loopback port (HIVE_DISCOVERY_ADDR) and is asked there directly, which
// is also the path for networks without multicast.
func TestM17DiscoverPairAndTrust(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	addr := freeUDP(t)
	c.env = append(c.env, discovery.EnvAddr+"="+addr)
	// The machine is 127.0.0.1 to ssh: that is the address it announces.
	studio := c.host(root, "127.0.0.1", true)
	if err := os.WriteFile(filepath.Join(studio, "config.toml"), []byte("[discovery]\nadvertise = true\nname = \"studio\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.must("--remote", "127.0.0.1", "env", "create", "secret-env", "--managed") // starts its daemon
	var proof struct {
		ID string `json:"id"`
	}
	decode(t, c.onHost(root, "127.0.0.1", `"$HOME/.local/bin/hive" machine identity --json`), &proof)
	var local struct {
		ID string `json:"id"`
	}
	decode(t, c.must("--json", "machine", "identity"), &local)
	if proof.ID == "" || proof.ID == local.ID {
		t.Fatalf("identities: there %q, here %q", proof.ID, local.ID)
	}

	// Found, with its identity; nothing about its sessions.
	var found discoverOutput
	var raw string
	c.waitFor("the announcement", func() bool {
		raw = c.must("--json", "machine", "discover", "--address", addr, "--timeout", "300ms")
		found = discoverOutput{}
		decode(t, raw, &found)
		return len(found.Runtimes) == 1
	})
	r := found.Runtimes[0]
	if r.Name != "studio" || r.ID != proof.ID || r.State != remote.StateDiscovered || r.Port != 22 || len(r.Caps) != 1 || r.Caps[0] != discovery.CapSSH {
		t.Fatalf("discovered %+v", r)
	}
	if strings.Contains(raw, "secret-env") || strings.Contains(raw, studio) {
		t.Fatalf("the announcement says too much: %s", raw)
	}
	if out := c.must("machine", "discover", "--address", addr, "--timeout", "300ms"); !strings.Contains(out, "new: hive machine pair studio") {
		t.Fatalf("discover: %s", out)
	}

	// Discovery alone saves and trusts nothing: pairing needs confirmation.
	if _, errOut, code := c.run("machine", "pair", "studio", "--address", addr, "--timeout", "300ms"); code == 0 || !strings.Contains(errOut, "--yes") || !strings.Contains(errOut, proof.ID) {
		t.Fatalf("pair without confirmation: exit %d: %s", code, errOut)
	}
	list := c.machineList()
	if len(list) != 0 {
		t.Fatalf("saved without confirmation: %+v", list)
	}

	// An announcement the machine at its address cannot back is refused.
	forged := freeUDP(t)
	impostor, err := discovery.LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- discovery.Advertise(ctx, discovery.ResponderConfig{
			Advertisement: discovery.Advertisement{Instance: "impostor", ID: impostor.ID, Port: 22, Caps: []string{discovery.CapSSH}},
			Addr:          forged,
		})
	}()
	_, errOut, code := c.run("machine", "pair", "impostor", "--address", forged, "--yes", "--timeout", "300ms")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if code == 0 || !strings.Contains(errOut, "not paired") || !strings.Contains(errOut, "announced identity") {
		t.Fatalf("forged announcement: exit %d: %s", code, errOut)
	}

	// Confirmed: paired, its identity pinned.
	c.must("machine", "pair", "studio", "--address", addr, "--yes", "--timeout", "300ms")
	list = c.machineList()
	if len(list) != 1 || list[0].Name != "studio" || list[0].State != remote.StateTrusted || list[0].ID != proof.ID ||
		!list[0].Discovered || list[0].HostKeyAlias != "hive-"+proof.ID || list[0].Host != "127.0.0.1" {
		t.Fatalf("paired: %+v", list)
	}
	if _, errOut, code := c.run("machine", "pair", "studio", "--address", addr, "--yes", "--as", "again", "--timeout", "300ms"); code == 0 || !strings.Contains(errOut, `saved as "studio"`) {
		t.Fatalf("paired twice: exit %d: %s", code, errOut)
	}
	found = discoverOutput{}
	decode(t, c.must("--json", "machine", "discover", "--address", addr, "--timeout", "300ms"), &found)
	if len(found.Runtimes) != 1 || found.Runtimes[0].State != remote.StateTrusted || found.Runtimes[0].Machine != "studio" {
		t.Fatalf("discover after pairing: %+v", found.Runtimes)
	}
	list = c.machineList("--discover", "--address", addr, "--timeout", "300ms")
	if len(list) != 1 || !list[0].Nearby {
		t.Fatalf("list --discover: %+v", list)
	}
	if out := c.must("--machine", "studio", "env", "list"); !strings.Contains(out, "secret-env") {
		t.Fatalf("--machine studio: %s", out)
	}
	var states []machineState
	decode(t, c.must("--json", "machine", "status", "studio"), &states)
	if len(states) != 1 || states[0].Identity != identityVerified {
		t.Fatalf("status: %+v", states)
	}

	// Another identity answers (hive's home was reset there): status says
	// so, and the machine is not used until the new identity is accepted.
	if err := os.Remove(filepath.Join(studio, ".hive", discovery.IdentityFile)); err != nil {
		t.Fatal(err)
	}
	out, errOut, _ := c.run("machine", "status", "studio")
	if !strings.Contains(out, "CHANGED") || !strings.Contains(errOut, "hive machine trust studio") {
		t.Fatalf("status after the change:\n%s\n%s", out, errOut)
	}
	if _, errOut, code := c.run("--machine", "studio", "env", "list"); code == 0 || !strings.Contains(errOut, "another identity") {
		t.Fatalf("used after an identity change: exit %d: %s", code, errOut)
	}
	list = c.machineList()
	if list[0].State != remote.StateChanged {
		t.Fatalf("state after the change: %+v", list[0])
	}
	if _, errOut, code := c.run("machine", "trust", "studio"); code == 0 || !strings.Contains(errOut, "CHANGED") || !strings.Contains(errOut, proof.ID) {
		t.Fatalf("trust without confirmation: exit %d: %s", code, errOut)
	}
	c.must("machine", "trust", "studio", "--yes")
	list = c.machineList()
	if list[0].State != remote.StateTrusted || list[0].ID == proof.ID || list[0].HostKeyAlias != "hive-"+proof.ID {
		t.Fatalf("trusted again: %+v", list[0])
	}
	c.must("--machine", "studio", "env", "list")
	if out := c.must("machine", "trust", "studio"); !strings.Contains(out, "proved its pinned identity") {
		t.Fatalf("trust of a verified machine: %s", out)
	}

	// Revoked: its identity forgotten, and not used.
	c.must("machine", "revoke", "studio")
	list = c.machineList()
	if list[0].State != remote.StateDisabled || list[0].ID != "" {
		t.Fatalf("revoked: %+v", list[0])
	}
	if _, errOut, code := c.run("--machine", "studio", "env", "list"); code == 0 || !strings.Contains(errOut, "disabled") {
		t.Fatalf("used after revoking: exit %d: %s", code, errOut)
	}

	// Gone from the network once its daemon stops.
	c.must("--remote", "127.0.0.1", "stop")
	out, errOut, _ = c.run("machine", "discover", "--address", addr, "--timeout", "300ms")
	if !strings.Contains(out, "no Hive runtimes found") || !strings.Contains(errOut, "discovery.advertise") {
		t.Fatalf("after its daemon stopped:\n%s\n%s", out, errOut)
	}
}

// TestM17ManualPairingAndDiscoveryOff: without discovery, a machine is
// paired by its ssh destination, and one saved without an identity gets
// it pinned by trust. Turning discovery off leaves all of that working.
func TestM17ManualPairingAndDiscoveryOff(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	c.host(root, "alpha", true)
	c.host(root, "beta", true)
	if err := os.WriteFile(c.cfg, []byte("[discovery]\nbrowse = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := c.run("machine", "discover"); code == 0 || !strings.Contains(errOut, "discovery.browse") {
		t.Fatalf("discover with browsing off: exit %d: %s", code, errOut)
	}
	if _, errOut, code := c.run("machine", "pair", "--host", "nowhere", "--yes"); code == 0 || !strings.Contains(errOut, "Could not resolve") {
		t.Fatalf("pair an unreachable host: exit %d: %s", code, errOut)
	}

	c.must("machine", "pair", "--host", "alpha", "--yes")
	c.must("machine", "add", "b", "beta")
	list := c.machineList()
	if len(list) != 2 || list[0].Name != "alpha" || list[0].State != remote.StateTrusted || list[0].Discovered || list[0].HostKeyAlias != "" ||
		list[1].State != remote.StateConfigured {
		t.Fatalf("list: %+v", list)
	}
	var states []machineState
	decode(t, c.must("--json", "machine", "status", "b"), &states)
	if states[0].Identity != identityUnpinned || states[0].IdentityID == "" {
		t.Fatalf("status of a machine without an identity: %+v", states[0])
	}
	if _, _, code := c.run("machine", "trust", "b"); code == 0 {
		t.Fatal("pinned without confirmation")
	}
	c.must("machine", "trust", "b", "--yes")
	list = c.machineList()
	if list[1].State != remote.StateTrusted || list[1].ID != states[0].IdentityID {
		t.Fatalf("trusted: %+v", list[1])
	}
	c.must("--machine", "b", "env", "list")
}

// procDial dials a daemon through a command's standard input and output
// (`hive machine proxy <name>`), as the desktop app does.
func procDial(c *cli, args ...string) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		cmd := exec.Command(os.Args[0], args...)
		cmd.Env = append(os.Environ(), "HIVE_CLI_TEST_EXEC=1", "HIVE_HOME="+c.home, "HIVE_CONFIG="+c.cfg, "GORACE=atexit_sleep_ms=0")
		cmd.Env = append(cmd.Env, c.env...)
		ours, theirs := net.Pipe()
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		cmd.Stdout = theirs
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		go func() {
			_, _ = io.Copy(stdin, theirs)
			_ = stdin.Close()
		}()
		go func() {
			_ = cmd.Wait()
			_ = theirs.Close()
		}()
		return ours, nil
	}
}

// TestM17ConfirmExactlyWhatWasShownAndProxy: a program shows a machine's
// identity (--check, saving nothing), then pairs only that identity
// (--expect-id); it reaches the machine's daemon through `hive machine
// proxy`, which refuses machines that are disabled.
func TestM17ConfirmExactlyWhatWasShownAndProxy(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	c.host(root, "alpha", true)

	var check identityCheck
	decode(t, c.must("machine", "pair", "--host", "alpha", "--check"), &check)
	if check.Trust != "new" || check.Proof.ID == "" || !strings.HasPrefix(check.Fingerprint, "SHA256:") || check.Machine.Name != "alpha" {
		t.Fatalf("check: %+v", check)
	}
	if list := c.machineList(); len(list) != 0 {
		t.Fatalf("--check saved something: %+v", list)
	}
	other := strings.Repeat("a", 26)
	if _, errOut, code := c.run("machine", "pair", "--host", "alpha", "--yes", "--expect-id", other); code == 0 || !strings.Contains(errOut, "not the identity confirmed") {
		t.Fatalf("pairing another identity: exit %d: %s", code, errOut)
	}
	c.must("machine", "pair", "--host", "alpha", "--yes", "--expect-id", check.Proof.ID)
	decode(t, c.must("machine", "trust", "alpha", "--check"), &check)
	if check.Trust != "verified" {
		t.Fatalf("trust --check of a paired machine: %+v", check)
	}

	c.must("--machine", "alpha", "env", "create", "far", "--managed")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cl, err := hiveapi.ConnectContext(ctx, hiveapi.Options{Name: "test", Dial: procDial(c, "machine", "proxy", "alpha")})
	if err != nil {
		t.Fatal(err)
	}
	envs, err := cl.Environments(ctx)
	_ = cl.Close()
	if err != nil || len(envs) != 1 || envs[0].ID != "far" {
		t.Fatalf("environments through the proxy: %+v %v", envs, err)
	}

	c.must("machine", "disable", "alpha")
	if _, errOut, code := c.run("machine", "proxy", "alpha"); code == 0 || !strings.Contains(errOut, "disabled") {
		t.Fatalf("proxy to a disabled machine: exit %d: %s", code, errOut)
	}
}
