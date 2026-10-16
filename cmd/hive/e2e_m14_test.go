//go:build !windows

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/runtime"
)

// TestM14Observers: credentials are made, checked, listed, used and
// revoked from the command line; an observer follows only its scope,
// locally and on another machine, and can do nothing but read.
func TestM14Observers(t *testing.T) {
	c := newCLI(t)
	c.must("env", "create", "dev", "--managed")
	c.must("env", "create", "ops", "--managed")
	devPane := strings.Fields(c.must("tab", "create", "dev", "--name", "work", "--", "sh", "-c", "echo dev-screen; read x"))[1]
	opsPane := strings.Fields(c.must("tab", "create", "ops", "--name", "secret", "--", "sh", "-c", "echo ops-screen; read x"))[1]

	// Making a credential needs a scope; the token is shown once.
	if _, errOut, code := c.run("observer", "create", "nothing"); code != exitUsage || !strings.Contains(errOut, "--all") {
		t.Fatalf("create without a scope: %d %s", code, errOut)
	}
	if _, errOut, code := c.run("observer", "create", "typo", "--env", "nope"); code == 0 || !strings.Contains(errOut, "nope") {
		t.Fatalf("create for a missing environment: %d %s", code, errOut)
	}
	var created runtime.ObserverCreated
	decode(t, c.must("--json", "observer", "create", "dash", "--env", "dev", "--screens", "--expires", "2h"), &created)
	token := created.Token
	if !strings.HasPrefix(token, "hvo_") || time.Until(created.Credential.ExpiresAt) > 2*time.Hour {
		t.Fatalf("created = %+v", created)
	}
	human := c.must("observer", "create", "metadata-only", "--pane", devPane)
	if !strings.Contains(human, "shown only now") || !strings.Contains(human, "hvo_") {
		t.Fatalf("create printed:\n%s", human)
	}
	if list := c.must("observer", "list"); !strings.Contains(list, "dash") || !strings.Contains(list, "environment:dev") || strings.Contains(list, "hvo_") {
		t.Fatalf("list:\n%s", list)
	}

	// Commands acting as an observer take the token from the environment.
	if _, errOut, code := c.run("observer", "status"); code != exitUsage || !strings.Contains(errOut, observerTokenEnv) {
		t.Fatalf("status without a token: %d %s", code, errOut)
	}
	c.env = append(c.env, observerTokenEnv+"="+token)
	if st := c.must("observer", "validate"); !strings.Contains(st, "active") {
		t.Fatalf("validate:\n%s", st)
	}
	if st := c.must("observer", "status"); !strings.Contains(st, "1 environment(s), 1 pane(s)") {
		t.Fatalf("status:\n%s", st)
	}
	var snap observer.Snapshot
	decode(t, c.must("observer", "snapshot"), &snap)
	if len(snap.Panes) != 1 || snap.Panes[0].ID != devPane {
		t.Fatalf("snapshot panes = %+v", snap.Panes)
	}
	c.waitFor("the dev screen", func() bool {
		out, _, _ := c.run("observer", "screen", devPane)
		return strings.Contains(out, "dev-screen")
	})
	if _, errOut, code := c.run("observer", "screen", opsPane); code == 0 || strings.Contains(errOut, "ops-screen") {
		t.Fatalf("screen out of scope: %d %s", code, errOut)
	}

	// A token belongs to one session: another session's daemon refuses it.
	c.must("--session", "work", "env", "list")
	if _, errOut, code := c.run("--session", "work", "observer", "status"); code == 0 || !strings.Contains(errOut, "invalid") {
		t.Fatalf("a token used on another session: %d %s", code, errOut)
	}
	defer c.run("--session", "work", "stop")

	// Events stream as JSON lines; the observer socket also works through
	// `hive proxy --observer`, which is what remote observers use.
	events := exec.Command(os.Args[0], "observer", "events")
	events.Env = append(os.Environ(), "HIVE_CLI_TEST_EXEC=1", "HIVE_HOME="+c.home, "HIVE_CONFIG="+c.cfg)
	events.Env = append(events.Env, c.env...)
	out, err := events.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = events.Process.Kill(); _ = events.Wait() }()
	time.Sleep(300 * time.Millisecond)
	c.must("tab", "create", "ops", "--name", "hidden-tab")
	c.must("tab", "create", "dev", "--name", "visible-tab")
	lines, stopReading := make(chan string), make(chan struct{})
	defer close(stopReading)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-stopReading:
				return
			}
		}
	}()
	deadline := time.After(15 * time.Second)
	for seen := false; !seen; {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("the event stream ended")
			}
			if strings.Contains(l, "hidden-tab") || strings.Contains(l, `"ops"`) {
				t.Fatalf("an event out of scope: %s", l)
			}
			var ev observer.Event
			if err := json.Unmarshal([]byte(l), &ev); err != nil {
				t.Fatal(err)
			}
			seen = strings.Contains(l, "visible-tab")
		case <-deadline:
			t.Fatal("no event for the new tab")
		}
	}

	// Revoking ends the credential; the audit log shows its life.
	c.must("observer", "revoke", created.Credential.ID)
	if _, _, code := c.run("observer", "validate"); code != exitError {
		t.Fatalf("validate a revoked token: exit %d", code)
	}
	if _, errOut, code := c.run("observer", "status"); code == 0 || !strings.Contains(errOut, "revoked") {
		t.Fatalf("status with a revoked token: %d %s", code, errOut)
	}
	audit := c.must("observer", "audit", "--credential", created.Credential.ID)
	for _, want := range []string{"created", "connected", "denied", "revoked", "refused"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit lacks %s:\n%s", want, audit)
		}
	}
}

// TestM14RemoteObserver: a token made on another machine works from here
// through ssh, against that machine's observer socket only.
func TestM14RemoteObserver(t *testing.T) {
	c := newCLI(t)
	root := c.fakeHosts()
	c.host(root, "alpha", true)
	c.must("--remote", "alpha", "env", "create", "dev", "--managed")
	var created runtime.ObserverCreated
	decode(t, c.must("--remote", "alpha", "--json", "observer", "create", "remote-dash", "--all"), &created)
	if !strings.HasPrefix(created.Socket, filepath.Join(root, "alpha")) && !strings.Contains(created.Socket, "observer.sock") {
		t.Fatalf("socket %s", created.Socket)
	}
	c.env = append(c.env, observerTokenEnv+"="+created.Token)
	if st := c.must("--remote", "alpha", "observer", "status"); !strings.Contains(st, "1 environment(s)") {
		t.Fatalf("remote status:\n%s", st)
	}
	// The same token means nothing to this machine's daemon.
	c.must("env", "list")
	if _, errOut, code := c.run("observer", "status"); code == 0 || !strings.Contains(errOut, "invalid") {
		t.Fatalf("a remote token used locally: %d %s", code, errOut)
	}
}
