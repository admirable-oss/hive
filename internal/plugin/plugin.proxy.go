package plugin

// The network proxy of a sandboxed plugin API 2 plugin that lists hosts
// ([permissions.network] hosts): the sandbox denies it the network, and
// it reaches the listed hosts through this proxy, which the daemon runs
// for it alone. The proxy sees host names, never the traffic inside TLS,
// and logs what it refuses to the plugin's log.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// proxyUser is the user name in the proxy's credentials.
const proxyUser = "hive"

// netProxy is one plugin's proxy.
type netProxy struct {
	hosts []string
	token string
	// tcp is its loopback address (macOS, where the sandbox allows that
	// one port); socket its Unix socket (Linux, bound into the sandbox).
	tcp    net.Listener
	unix   net.Listener
	dir    string
	logf   func(format string, args ...any)
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
	ctx    context.Context // ends when the proxy closes
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startProxy starts a proxy allowing hosts: on a Unix socket on Linux, on
// a loopback port elsewhere.
func startProxy(ctx context.Context, hosts []string, logf func(string, ...any)) (*netProxy, error) {
	p := &netProxy{hosts: hosts, token: randomToken(), logf: logf, conns: map[net.Conn]struct{}{}}
	p.ctx, p.cancel = context.WithCancel(context.WithoutCancel(ctx))
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	p.dial = d.DialContext
	var lc net.ListenConfig
	if runtime.GOOS == "linux" {
		// A short private directory: socket paths are bounded.
		dir, err := os.MkdirTemp("", "hp")
		if err != nil {
			return nil, err
		}
		p.dir = dir
		l, err := lc.Listen(ctx, "unix", filepath.Join(dir, "proxy.sock"))
		if err != nil {
			_ = os.RemoveAll(dir)
			p.cancel()
			return nil, err
		}
		p.unix = l
		p.serve(l)
		return p, nil
	}
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		p.cancel()
		return nil, err
	}
	p.tcp = l
	p.serve(l)
	return p, nil
}

// Socket is the proxy's Unix socket ("" when it listens on TCP).
func (p *netProxy) Socket() string {
	if p.unix == nil {
		return ""
	}
	return p.unix.Addr().String()
}

// Port is its loopback port (0 when it listens on a socket).
func (p *netProxy) Port() int {
	if p.tcp == nil {
		return 0
	}
	return p.tcp.Addr().(*net.TCPAddr).Port
}

// URL is the proxy URL a plugin's programs use, at addr.
func (p *netProxy) URL(addr string) string {
	return "http://" + proxyUser + ":" + p.token + "@" + addr
}

// ProxyEnv are the variables that send a program's HTTP(S) through the
// proxy at url.
func ProxyEnv(url string) []string {
	var out []string
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"} {
		out = append(out, k+"="+url)
	}
	return append(out, "NO_PROXY=", "no_proxy=")
}

func (p *netProxy) serve(l net.Listener) {
	p.wg.Go(func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			if !p.track(c, true) {
				_ = c.Close()
				return
			}
			p.wg.Go(func() {
				defer p.track(c, false)
				defer c.Close()
				p.handle(p.ctx, c)
			})
		}
	})
}

func (p *netProxy) track(c net.Conn, add bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if add {
		if p.closed {
			return false
		}
		p.conns[c] = struct{}{}
	} else {
		delete(p.conns, c)
	}
	return true
}

// Close stops the proxy and every connection through it.
func (p *netProxy) Close() {
	p.cancel()
	p.mu.Lock()
	p.closed = true
	for c := range p.conns {
		_ = c.Close()
	}
	p.mu.Unlock()
	for _, l := range []net.Listener{p.tcp, p.unix} {
		if l != nil {
			_ = l.Close()
		}
	}
	p.wg.Wait()
	if p.dir != "" {
		_ = os.RemoveAll(p.dir)
	}
}

func (p *netProxy) authorized(r *http.Request) bool {
	auth, ok := strings.CutPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
	if !ok {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth))
	if err != nil {
		return false
	}
	user, pass, _ := strings.Cut(string(raw), ":")
	return user == proxyUser && subtle.ConstantTimeCompare([]byte(pass), []byte(p.token)) == 1
}

func reply(c net.Conn, status int, msg string) {
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n", status, http.StatusText(status), len(msg)+1)
	if status == http.StatusProxyAuthRequired {
		fmt.Fprintf(c, "Proxy-Authenticate: Basic realm=\"hive\"\r\n")
	}
	fmt.Fprintf(c, "\r\n%s\n", msg)
}

// target is a request's host and port.
func target(r *http.Request) (string, int, error) {
	hostport := r.Host
	if r.Method != http.MethodConnect && r.URL.Host != "" {
		hostport = r.URL.Host
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, "80"
		if r.URL.Scheme == "https" {
			port = "443"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || host == "" {
		return "", 0, fmt.Errorf("bad target %q", hostport)
	}
	return host, n, nil
}

func (p *netProxy) handle(ctx context.Context, c net.Conn) {
	br := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Minute))
		r, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		_ = c.SetReadDeadline(time.Time{})
		if !p.authorized(r) {
			reply(c, http.StatusProxyAuthRequired, "hive: proxy credentials required")
			return
		}
		host, port, err := target(r)
		if err != nil {
			reply(c, http.StatusBadRequest, "hive: "+err.Error())
			return
		}
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		if !HostAllowed(p.hosts, host, port) {
			p.logf("network: refused %s (not in [permissions.network] hosts)", addr)
			reply(c, http.StatusForbidden, "hive: "+addr+" is not one of the hosts this plugin may reach")
			return
		}
		if r.Method == http.MethodConnect {
			p.tunnel(ctx, c, br, addr)
			return
		}
		if !p.forward(ctx, c, r, addr) {
			return
		}
	}
}

// tunnel joins the client to addr after a CONNECT.
func (p *netProxy) tunnel(ctx context.Context, c net.Conn, br *bufio.Reader, addr string) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	up, err := p.dial(dctx, "tcp", addr)
	cancel()
	if err != nil {
		reply(c, http.StatusBadGateway, "hive: "+err.Error())
		return
	}
	defer up.Close()
	if !p.track(up, true) {
		return
	}
	defer p.track(up, false)
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		// What the client sent with its CONNECT is already buffered.
		_, _ = io.Copy(up, br)
		if cw, ok := up.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(c, up)
		done <- struct{}{}
	}()
	<-done
}

// forward relays one plain HTTP request to addr; false ends the
// connection.
func (p *netProxy) forward(ctx context.Context, c net.Conn, r *http.Request, addr string) bool {
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("Proxy-Connection")
	r.RequestURI = ""
	if r.URL.Scheme == "" {
		r.URL.Scheme = "http"
	}
	if r.URL.Scheme != "http" {
		reply(c, http.StatusBadRequest, "hive: only http:// is proxied without CONNECT")
		return false
	}
	r.URL.Host = addr
	transport := &http.Transport{Proxy: nil, DialContext: p.dial, DisableKeepAlives: true, ResponseHeaderTimeout: time.Minute}
	defer transport.CloseIdleConnections()
	resp, err := transport.RoundTrip(r.WithContext(ctx))
	if err != nil {
		reply(c, http.StatusBadGateway, "hive: "+err.Error())
		return false
	}
	defer resp.Body.Close()
	keep := !r.Close && resp.ContentLength >= 0
	resp.Close = !keep
	if err := resp.Write(c); err != nil {
		return false
	}
	return keep
}
