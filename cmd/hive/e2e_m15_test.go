//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/web"
)

// TestM15WebObserver: allow a runtime, serve the web observer, share a link,
// follow an agent in a "browser", and revoke it, all from the command line.
func TestM15WebObserver(t *testing.T) {
	c := newCLI(t)
	bin := t.TempDir()
	script := filepath.Join(bin, "claude")
	if err := os.WriteFile(script, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	c.must("env", "create", "dev", "--managed")
	c.must("tab", "create", "dev", "--name", "agent", "--", script)

	// Nothing is exposed until a runtime is allowed, explicitly.
	if _, errOut, code := c.run("web", "runtime", "add", "laptop"); code != exitUsage || !strings.Contains(errOut, "--all") {
		t.Fatalf("add without a scope: %d %s", code, errOut)
	}
	c.must("web", "runtime", "add", "laptop", "--all", "--screens")
	if list := c.must("web", "runtime", "list"); !strings.Contains(list, "laptop") || !strings.Contains(list, "this machine") {
		t.Fatalf("runtime list:\n%s", list)
	}
	if _, errOut, code := c.run("web", "share"); code == 0 || !strings.Contains(errOut, "not running") {
		t.Fatalf("share without a server: %d %s", code, errOut)
	}
	if _, errOut, code := c.run("web", "serve", "--listen", "0.0.0.0:0"); code != exitUsage || !strings.Contains(errOut, "TLS") {
		t.Fatalf("serving the network over HTTP: %d %s", code, errOut)
	}

	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	serve := exec.Command(os.Args[0], "web", "serve", "--listen", addr)
	serve.Env = append(os.Environ(), "HIVE_CLI_TEST_EXEC=1", "HIVE_HOME="+c.home, "HIVE_CONFIG="+c.cfg)
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serve.Process.Signal(os.Interrupt); _ = serve.Wait() }()
	base := "http://" + addr
	c.waitFor("the web observer", func() bool {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
		}
		return err == nil && resp.StatusCode == http.StatusOK
	})

	var shared web.Shared
	decode(t, c.must("--json", "web", "share", "--screens", "--label", "phone"), &shared)
	if !strings.HasPrefix(shared.Link, "http://localhost:"+strconv.Itoa(port)+"/login#hwc_") {
		t.Fatalf("link %s", shared.Link)
	}
	human := c.must("web", "share", "--expires", "1h")
	if !strings.Contains(human, "/login#hwc_") || !strings.Contains(human, "works 1 time") {
		t.Fatalf("share printed:\n%s", human)
	}

	// The "browser": redeem the code, read the dashboard until the agent shows.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	codeValue := strings.SplitN(shared.Link, "#", 2)[1]
	form := url.Values{"code": {codeValue}}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp, err := browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	c.waitFor("the agent on the runtime page", func() bool {
		_, body := fetch(t, browser, base+"/r/laptop")
		return strings.Contains(body, "claude")
	})
	if st := c.must("web", "status"); !strings.Contains(st, "laptop") || !strings.Contains(st, "live") {
		t.Fatalf("status:\n%s", st)
	}
	// The daemon knows it is observed.
	if list := c.must("observer", "list"); !strings.Contains(list, "hive-web:laptop") {
		t.Fatalf("observer list:\n%s", list)
	}
	if grants := c.must("web", "grants"); !strings.Contains(grants, "phone") || !strings.Contains(grants, "NOTIFIED DEVICES") {
		t.Fatalf("grants:\n%s", grants)
	}
	if st := c.must("web", "push", "status"); !strings.Contains(st, "VAPID public key") || !strings.Contains(st, "no devices") {
		t.Fatalf("push status:\n%s", st)
	}
	if out := c.must("web", "push", "test", shared.Grant.ID); !strings.Contains(out, "0 device(s)") {
		t.Fatalf("push test:\n%s", out)
	}
	// The installed app's pieces are served.
	if at, body := fetch(t, browser, base+"/manifest.webmanifest"); at != "/manifest.webmanifest" || !strings.Contains(body, `"standalone"`) {
		t.Fatalf("manifest: %s %s", at, body)
	}

	c.must("web", "revoke", shared.Grant.ID)
	if at, _ := fetch(t, browser, base+"/"); at != "/login?reason=revoked" {
		t.Fatalf("after revoking: %s", at)
	}
	audit := c.must("web", "audit")
	for _, want := range []string{"shared", "signed_in", "revoked"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit lacks %s:\n%s", want, audit)
		}
	}

	// Removing the runtime revokes its credential on the daemon.
	c.must("web", "runtime", "remove", "laptop")
	var obs struct {
		Credentials []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal([]byte(c.must("--json", "observer", "list")), &obs); err != nil {
		t.Fatal(err)
	}
	for _, cr := range obs.Credentials {
		if cr.Name == "hive-web:laptop" && cr.State != "revoked" {
			t.Fatalf("the web observer's credential is %s after removing the runtime", cr.State)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// fetch returns where a GET ended (path and query) and its body.
func fetch(t *testing.T, c *http.Client, u string) (string, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	at := resp.Request.URL.Path
	if resp.Request.URL.RawQuery != "" {
		at += "?" + resp.Request.URL.RawQuery
	}
	return at, string(body)
}
