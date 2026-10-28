package plugin

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

const api2Head = `api = 2
id = "p"
version = "1.0.0"
`

// manifestOf is a manifest with extra between its head and its action
// (TOML keys after [[actions]] would belong to it).
func manifestOf(head, extra string) string {
	return head + extra + "[[actions]]\nid = \"a\"\ncommand = [\"true\"]\n"
}

func TestAPI2ManifestChecks(t *testing.T) {
	ok := map[string]string{
		"scoped network":  "[permissions]\nrequired = [\"network\"]\n[permissions.network]\nhosts = [\"api.linear.app:443\", \"*.example.org:443\", \"[::1]:8080\"]\n",
		"scoped programs": "[permissions]\noptional = [\"process.exec\"]\n[permissions.process]\nexec = [\"git\", \"/usr/bin/gh\"]\n",
		"workspace reads": "[permissions]\nrequired = [\"filesystem.read:workspace\"]\n",
		"isolation none":  "isolation = \"none\"\nisolation_reason = \"drives the GPU\"\n",
	}
	for name, extra := range ok {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(manifestOf(api2Head, extra))); err != nil {
				t.Fatal(err)
			}
		})
	}
	bad := map[string]string{
		"hosts without network":   "[permissions]\nrequired = []\n[permissions.network]\nhosts = [\"a.example:443\"]\n",
		"exec without capability": "[permissions.process]\nexec = [\"git\"]\n",
		"no port":                 "[permissions]\nrequired = [\"network\"]\n[permissions.network]\nhosts = [\"a.example\"]\n",
		"bad port":                "[permissions]\nrequired = [\"network\"]\n[permissions.network]\nhosts = [\"a.example:99999\"]\n",
		"inner wildcard":          "[permissions]\nrequired = [\"network\"]\n[permissions.network]\nhosts = [\"a.*.example:443\"]\n",
		"relative program":        "[permissions]\nrequired = [\"process.exec\"]\n[permissions.process]\nexec = [\"bin/tool\"]\n",
		"none without reason":     "isolation = \"none\"\n",
		"reason without none":     "isolation_reason = \"x\"\n",
	}
	for name, extra := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(manifestOf(api2Head, extra))); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	// Plugin API 1 gains nothing: API 2's capabilities and sections are
	// refused, so a manifest means one thing.
	v1 := strings.Replace(api2Head, "api = 2", "api = 1", 1)
	for name, extra := range map[string]string{
		"filesystem.read": "[permissions]\nrequired = [\"filesystem.read\"]\n",
		"unconfined":      "[permissions]\nrequired = [\"unconfined\"]\n",
		"hosts":           "[permissions]\nrequired = [\"network\"]\n[permissions.network]\nhosts = [\"a.example:443\"]\n",
		"none":            "isolation = \"none\"\nisolation_reason = \"x\"\n",
	} {
		if _, err := ParseManifest([]byte(manifestOf(v1, extra))); err == nil {
			t.Errorf("api 1 with %s accepted", name)
		}
	}
	// isolation = "none" requires unconfined; every API 2 plugin may be
	// granted it, for a system with no sandbox; API 1 plugins never.
	m, _ := ParseManifest([]byte(manifestOf(api2Head, ok["isolation none"])))
	if !slices.Contains(m.RequiredCapabilities(), CapUnconfined) {
		t.Fatal("isolation none does not require unconfined")
	}
	m, _ = ParseManifest([]byte(manifestOf(api2Head, "")))
	if !slices.Contains(m.OptionalCapabilities(), CapUnconfined) || slices.Contains(m.RequiredCapabilities(), CapUnconfined) {
		t.Fatal("unconfined is optional for a sandboxed API 2 plugin")
	}
	m1, _ := ParseManifest([]byte(manifestOf(v1, "")))
	if slices.Contains(m1.OptionalCapabilities(), CapUnconfined) {
		t.Fatal("an API 1 plugin may ask for unconfined")
	}
	// Approving an update never carries unconfined over: once per release.
	if slices.Contains(Approve(m, []string{CapUnconfined}), CapUnconfined) {
		t.Fatal("unconfined carried over to an update")
	}
}

func TestHostAllowed(t *testing.T) {
	hosts := []string{"api.linear.app:443", "*.example.org:443", "127.0.0.1:8080"}
	for _, c := range []struct {
		host string
		port int
		want bool
	}{
		{"api.linear.app", 443, true},
		{"API.Linear.App.", 443, true},
		{"api.linear.app", 80, false},
		{"evil-api.linear.app", 443, false},
		{"x.example.org", 443, true},
		{"a.b.example.org", 443, true},
		{"example.org", 443, false},
		{"badexample.org", 443, false},
		{"127.0.0.1", 8080, true},
	} {
		if got := HostAllowed(hosts, c.host, c.port); got != c.want {
			t.Errorf("HostAllowed(%s:%d) = %v", c.host, c.port, got)
		}
	}
}

// withSandbox runs f as if this system had (or lacked) a sandbox.
func withSandbox(t *testing.T, available bool) {
	t.Helper()
	old := sandboxProbe
	sandboxProbe = func() bool { return available }
	t.Cleanup(func() { sandboxProbe = old })
}

func TestAPI2Isolation(t *testing.T) {
	api2Base := manifestOf(api2Head, "")
	none := manifestOf(api2Head, "isolation = \"none\"\nisolation_reason = \"drives the GPU\"\n")
	v1Strict := manifestOf(strings.Replace(api2Head, "api = 2", "api = 1", 1), "isolation = \"strict\"\n")
	cases := []struct {
		name             string
		manifest         string
		sandbox          bool
		isolation        string
		granted          []string
		strict, confined bool
		problem          string
	}{
		{name: "sandboxed by default", manifest: api2Base, sandbox: true, isolation: IsolationAuto, strict: true, confined: true},
		{name: "standard never loosens API 2", manifest: api2Base, sandbox: true, isolation: IsolationStandard, strict: true, confined: true},
		{name: "no sandbox: refused", manifest: api2Base, sandbox: false, isolation: IsolationAuto, problem: "hive plugin grant p unconfined"},
		{name: "no sandbox: approved unconfined", manifest: api2Base, sandbox: false, isolation: IsolationAuto, granted: []string{CapUnconfined}},
		{name: "none, approved", manifest: none, sandbox: true, isolation: IsolationAuto, granted: []string{CapUnconfined}},
		{name: "strict overrides none", manifest: none, sandbox: true, isolation: IsolationStrict, granted: []string{CapUnconfined}, strict: true, confined: true},
		{name: "strict without a sandbox", manifest: api2Base, sandbox: false, isolation: IsolationStrict, granted: []string{CapUnconfined}, problem: "isolation is strict"},
		{name: "api2 refuses API 1", manifest: v1Strict, sandbox: true, isolation: IsolationAPI2, problem: `isolation = "api2"`},
		{name: "API 1 strict, as before", manifest: v1Strict, sandbox: true, isolation: IsolationAuto, strict: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withSandbox(t, c.sandbox)
			m, err := ParseManifest([]byte(c.manifest))
			if err != nil {
				t.Fatal(err)
			}
			h := NewHost(HostConfig{Isolation: c.isolation})
			l := &loaded{Plugin: Plugin{Installed: Installed{ID: "p"}, Manifest: m}}
			l.grant = Decide(m, append(Approve(m, nil), c.granted...), Policy{})
			problem := h.isolate(l, m)
			if (c.problem == "") != (problem == "") || !strings.Contains(problem, c.problem) {
				t.Fatalf("problem %q, want %q", problem, c.problem)
			}
			if problem == "" && (l.strict != c.strict || l.confined != c.confined) {
				t.Fatalf("strict %v confined %v, want %v %v", l.strict, l.confined, c.strict, c.confined)
			}
		})
	}
}

func proxyRequest(t *testing.T, addr, raw string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.WriteString(c, raw); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line)
}

func TestProxy(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			c, err := upstream.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(c, "hello\n")
			_ = c.Close()
		}
	}()
	port := upstream.Addr().(*net.TCPAddr).Port
	var mu sync.Mutex
	var logged []string
	p, err := startProxy(t.Context(), []string{fmt.Sprintf("127.0.0.1:%d", port)}, func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Tests reach it over TCP everywhere: on Linux through its socket.
	addr := ""
	if s := p.Socket(); s != "" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go forwardProxy(t.Context(), l, s)
		addr = l.Addr().String()
	} else {
		addr = fmt.Sprintf("127.0.0.1:%d", p.Port())
	}
	auth := "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(proxyUser+":"+p.token)) + "\r\n"
	listed := fmt.Sprintf("127.0.0.1:%d", port)
	if got := proxyRequest(t, addr, "CONNECT "+listed+" HTTP/1.1\r\nHost: "+listed+"\r\n\r\n"); !strings.Contains(got, "407") {
		t.Fatalf("without credentials: %s", got)
	}
	if got := proxyRequest(t, addr, "CONNECT "+listed+" HTTP/1.1\r\nHost: "+listed+"\r\n"+auth+"\r\n"); !strings.Contains(got, "200") {
		t.Fatalf("a listed host: %s", got)
	}
	if got := proxyRequest(t, addr, "CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\n"+auth+"\r\n"); !strings.Contains(got, fmt.Sprint(http.StatusForbidden)) {
		t.Fatalf("an unlisted host: %s", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "refused 127.0.0.1:1") {
		t.Fatalf("refusals are logged: %v", logged)
	}
}
