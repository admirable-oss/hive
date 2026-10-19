// Package remote reaches Hive on other machines over SSH. A client dials a
// remote daemon by running `hive proxy` there through the system's ssh
// (so ~/.ssh/config, keys and agents apply), which bridges its stdin and
// stdout to the daemon's socket. Connections share one SSH connection per
// host (ControlMaster) and are kept alive.
//
// The package also installs or updates hive on a machine, and keeps the
// list of saved machines (see Registry).
package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// EnvSSH overrides the ssh command (split on spaces), for tests and for
// unusual setups.
const EnvSSH = "HIVE_SSH"

// Target is Hive on another machine.
type Target struct {
	// Host is an ssh destination: [user@]host, or a Host from ~/.ssh/config.
	Host string
	// Session is the remote session ("" is the default one).
	Session string
	// HivePath is the remote hive binary; empty finds hive on the remote
	// PATH, else in ~/.local/bin (where Install puts it).
	HivePath string
	// SSH is the ssh command; nil means $HIVE_SSH, else "ssh".
	SSH []string
	// ControlDir holds the shared connections' sockets (ControlPath);
	// empty turns sharing off.
	ControlDir string
	// HostKeyAlias, when set, is the name ssh checks the host key under
	// instead of the host (see Machine.Target).
	HostKeyAlias string
}

// ErrInvalidHost is returned for an empty host or one that looks like an
// ssh option.
var ErrInvalidHost = errors.New("remote: invalid host")

func (t Target) validate() error {
	if t.Host == "" || strings.HasPrefix(t.Host, "-") || strings.ContainsAny(t.Host, " \t\n") {
		return fmt.Errorf("%w %q", ErrInvalidHost, t.Host)
	}
	return nil
}

func (t Target) ssh() []string {
	if len(t.SSH) > 0 {
		return t.SSH
	}
	if s := strings.Fields(os.Getenv(EnvSSH)); len(s) > 0 {
		return s
	}
	return []string{"ssh"}
}

// args is the ssh command line that runs script on the host.
func (t Target) args(tty bool, script string) []string {
	argv := append([]string(nil), t.ssh()...)
	argv = append(argv,
		// Fail instead of hanging on an unreachable host; notice a dead
		// connection within about 45 s.
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	)
	if t.HostKeyAlias != "" {
		argv = append(argv, "-o", "HostKeyAlias="+t.HostKeyAlias)
	}
	if t.ControlDir != "" {
		argv = append(argv,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+t.ControlDir+"/%C",
			"-o", "ControlPersist=10m",
		)
	}
	if tty {
		argv = append(argv, "-t")
	} else {
		argv = append(argv, "-T")
	}
	return append(argv, t.Host, "--", script)
}

// hive is a shell snippet that sets $h to the remote hive binary.
func (t Target) hive() string {
	switch p := t.HivePath; {
	case p == "":
		return `h=$(command -v hive 2>/dev/null) || h="$HOME/.local/bin/hive"`
	case strings.HasPrefix(p, "~/"):
		return `h="$HOME"/` + Quote(p[2:])
	default:
		return "h=" + Quote(p)
	}
}

// hiveScript runs the remote hive with args (and the target's session).
func (t Target) hiveScript(args ...string) string {
	if t.Session != "" {
		args = append([]string{"--session", t.Session}, args...)
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = Quote(a)
	}
	return t.hive() + `; exec "$h" ` + strings.Join(q, " ")
}

// Command returns ssh running `hive args…` on the host, for commands that
// run there as a whole. With tty the remote side gets a terminal.
func (t Target) Command(ctx context.Context, tty bool, args ...string) (*exec.Cmd, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	argv := t.args(tty, t.hiveScript(args...))
	return exec.CommandContext(ctx, argv[0], argv[1:]...), nil
}

// Shell runs a POSIX shell script on the host and returns its output.
// stdin, when not nil, is the script's input.
func (t Target) Shell(ctx context.Context, script string, stdin io.Reader) ([]byte, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	argv := t.args(false, script)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), sshError(t.Host, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// CloseShared ends the host's shared SSH connection, so the next one is
// made afresh (ssh -O exit). Without one it does nothing.
func (t Target) CloseShared(ctx context.Context) error {
	if t.ControlDir == "" || t.validate() != nil {
		return nil
	}
	argv := append(append([]string(nil), t.ssh()...), "-o", "ControlPath="+t.ControlDir+"/%C", "-O", "exit", t.Host)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such file") && !strings.Contains(string(out), "Control socket connect") {
		return sshError(t.Host, err, string(out))
	}
	return nil
}

// sshError explains a failed ssh run with what it printed.
func sshError(host string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		switch ee.ExitCode() {
		case 255:
			return fmt.Errorf("ssh %s: %s", host, cmpStr(stderr, "connection failed"))
		case 126, 127:
			return fmt.Errorf("%w on %s: %s", ErrNotInstalled, host, cmpStr(stderr, "command not found"))
		}
	}
	if stderr != "" {
		return fmt.Errorf("on %s: %w: %s", host, err, stderr)
	}
	return fmt.Errorf("on %s: %w", host, err)
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Dial connects to the host's daemon: ssh runs `hive proxy` there, which
// starts the daemon when needed. The connection is a real socket (one end
// of a socket pair whose other end is ssh's stdin and stdout), so deadlines
// work; closing it ends ssh.
func (t Target) Dial(ctx context.Context) (net.Conn, error) {
	return t.dial(ctx, "proxy")
}

// DialObserver connects to the host's observer socket (`hive proxy
// --observer`): what an observer token works on, and nothing else does.
func (t Target) DialObserver(ctx context.Context) (net.Conn, error) {
	return t.dial(ctx, "proxy", "--observer")
}

func (t Target) dial(ctx context.Context, args ...string) (net.Conn, error) {
	cmd, err := t.Command(context.WithoutCancel(ctx), false, args...)
	if err != nil {
		return nil, err
	}
	conn, theirs, err := sshTransport()
	if err != nil {
		return nil, err
	}
	defer theirs.Close()
	c := &Conn{Conn: conn, host: t.Host, cmd: cmd, done: make(chan struct{})}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = theirs, theirs, &c.stderr
	if err := cmd.Start(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("start ssh: %w", err)
	}
	go func() {
		c.waitErr = cmd.Wait()
		close(c.done)
	}()
	return c, nil
}

// Conn is a connection to a remote daemon through ssh.
type Conn struct {
	net.Conn
	host string
	cmd  *exec.Cmd

	stderr  limitedBuffer
	done    chan struct{} // closed when ssh exited
	waitErr error
	once    sync.Once
}

// Read returns what the daemon sent. When ssh ends with an error before
// the daemon closed the connection, it says why instead of io.EOF.
func (c *Conn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if errors.Is(err, io.EOF) {
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			return n, err
		}
		if c.waitErr != nil {
			return n, sshError(c.host, c.waitErr, c.stderr.String())
		}
	}
	return n, err
}

// CloseWrite says this side is done sending; ssh passes the end of input
// on, and the daemon's answers keep coming.
func (c *Conn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

// Close closes the connection; ssh sees its input end and exits.
func (c *Conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			_ = c.cmd.Process.Kill()
			<-c.done
		}
	})
	return err
}

// limitedBuffer keeps the last few KiB written to it (ssh's stderr).
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const stderrKeep = 4 << 10

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > stderrKeep {
		b.buf = b.buf[len(b.buf)-stderrKeep:]
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// Quote quotes s for a POSIX shell.
func Quote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safeShell(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func safeShell(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)
}
