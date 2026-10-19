package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Saved machines (M7) and local discovery (M17) go through the CLI, with
// its JSON output: one implementation of ssh, identities and trust, and
// one machines.json. The app adds windows, not another way in.

// SavedMachine is a machine in `hive machine list`.
type SavedMachine struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	State    string `json:"state"` // trusted, configured, changed, disabled
	ID       string `json:"id,omitempty"`
	Platform string `json:"platform,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Changed  bool   `json:"identity_changed,omitempty"`
}

// connectable says the app connects to it: enabled, and not holding an
// identity change no one accepted.
func (m SavedMachine) connectable() bool { return !m.Disabled && !m.Changed }

// Nearby is a runtime `hive machine discover` found.
type Nearby struct {
	Name          string   `json:"name"`
	ID            string   `json:"id"`
	Addresses     []string `json:"addresses"`
	Port          int      `json:"port"`
	State         string   `json:"state"` // discovered, or the saved machine's state
	Machine       string   `json:"machine,omitempty"`
	Note          string   `json:"note,omitempty"`
	DuplicateName bool     `json:"duplicate_name,omitempty"`
}

// Discovery is what a search found, and why it may have found nothing.
type Discovery struct {
	Runtimes   []Nearby `json:"runtimes"`
	Interfaces []string `json:"interfaces"`
	Errors     []string `json:"errors"`
}

// IdentityCheck is a machine's proven identity, shown before the user
// trusts it (`--check`).
type IdentityCheck struct {
	Machine struct {
		Name string `json:"name"`
		Host string `json:"host"`
	} `json:"machine"`
	Proof struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Hostname string `json:"hostname"`
		Platform string `json:"platform"`
		Version  string `json:"version"`
	} `json:"proof"`
	Fingerprint string `json:"fingerprint"`
	// Trust is new, verified, unpinned or changed.
	Trust  string `json:"trust"`
	Pinned string `json:"pinned,omitempty"`
}

// PairRequest names what to pair: a nearby runtime (its ID) or an ssh
// destination (Host).
type PairRequest struct {
	ID   string `json:"id,omitempty"`
	Host string `json:"host,omitempty"`
	User string `json:"user,omitempty"`
	// As is the name to save it under ("" for its own).
	As string `json:"as,omitempty"`
}

var (
	machineNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	identityRE    = regexp.MustCompile(`^[a-z2-7]{26}$`)
	userRE        = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]{0,63}$`)
)

func validMachine(name string) error {
	if !machineNameRE.MatchString(name) {
		return fmt.Errorf("invalid machine name %q", name)
	}
	return nil
}

func (r PairRequest) args() ([]string, error) {
	var args []string
	switch {
	case r.ID != "" && r.Host == "":
		if !identityRE.MatchString(r.ID) {
			return nil, fmt.Errorf("invalid identity %q", r.ID)
		}
		args = []string{r.ID}
	case r.Host != "" && r.ID == "":
		if strings.HasPrefix(r.Host, "-") || strings.ContainsAny(r.Host, " \t\r\n") || len(r.Host) > 255 {
			return nil, fmt.Errorf("invalid host %q", r.Host)
		}
		args = []string{"--host", r.Host}
	default:
		return nil, errors.New("pair a nearby runtime or a host")
	}
	if r.User != "" {
		if !userRE.MatchString(r.User) {
			return nil, fmt.Errorf("invalid user %q", r.User)
		}
		args = append(args, "--user", r.User)
	}
	if r.As != "" {
		if err := validMachine(r.As); err != nil {
			return nil, err
		}
		args = append(args, "--as", r.As)
	}
	return args, nil
}

// cli runs `hive args…` and decodes its JSON output into out (nil: none).
// A failure is the CLI's own message.
func (h *Hub) cli(ctx context.Context, timeout time.Duration, out any, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd, err := h.hiveCommand(ctx, args...)
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "hive: "); i >= 0 {
			msg = msg[i+len("hive: "):]
		}
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("hive %s: unreadable output: %w", args[0], err)
	}
	return nil
}

// machineDial reaches a saved machine's daemon through `hive machine proxy`.
func (h *Hub) machineDial(name string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		// The process lives as long as the connection, not the dial.
		cmd, err := h.hiveCommand(context.WithoutCancel(ctx), "machine", "proxy", name)
		if err != nil {
			return nil, err
		}
		return procConn(cmd)
	}
}

// procConn is a connection over a process's standard input and output.
func procConn(cmd *exec.Cmd) (net.Conn, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	ours, theirs := net.Pipe()
	stderr := &limited{}
	cmd.Stdout, cmd.Stderr = theirs, stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		_, _ = io.Copy(stdin, theirs)
		_ = stdin.Close()
	}()
	c := &processConn{Conn: ours, cmd: cmd, done: make(chan struct{}), stderr: stderr}
	go func() {
		c.waitErr = cmd.Wait()
		_ = theirs.Close()
		close(c.done)
	}()
	return c, nil
}

type processConn struct {
	net.Conn
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
	stderr  *limited
}

// Read returns what the daemon sent; when the proxy failed instead, it
// says why (its own message) rather than io.EOF.
func (c *processConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		select {
		case <-c.done:
			if c.waitErr != nil {
				if msg := c.stderr.message(); msg != "" {
					return n, errors.New(msg)
				}
			}
		case <-time.After(2 * time.Second):
		}
	}
	return n, err
}

func (c *processConn) Close() error {
	err := c.Conn.Close()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	return err
}

// limited keeps the last bytes written (a proxy's errors).
type limited struct {
	mu sync.Mutex
	b  []byte
}

func (l *limited) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	if len(l.b) > 4096 {
		l.b = l.b[len(l.b)-4096:]
	}
	return len(p), nil
}

// message is the last line written, without hive's prefix.
func (l *limited) message() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(l.b)), "\n")
	return strings.TrimPrefix(lines[len(lines)-1], "hive: ")
}

// --- what HiveService offers about machines ---

// SavedMachines lists the saved machines.
func (h *Hub) SavedMachines(ctx context.Context) ([]SavedMachine, error) {
	var out []SavedMachine
	err := h.cli(ctx, 20*time.Second, &out, "machine", "list", "--json")
	if out == nil {
		out = []SavedMachine{}
	}
	return out, err
}

// Discover asks the local network for runtimes.
func (h *Hub) Discover(ctx context.Context) (Discovery, error) {
	var raw struct {
		Runtimes   []Nearby `json:"runtimes"`
		Interfaces []struct {
			Name string `json:"name"`
		} `json:"interfaces"`
		Errors []string `json:"errors"`
	}
	d := Discovery{Runtimes: []Nearby{}, Interfaces: []string{}, Errors: []string{}}
	if err := h.cli(ctx, 60*time.Second, &raw, "machine", "discover", "--json"); err != nil {
		return d, err
	}
	d.Runtimes = append(d.Runtimes, raw.Runtimes...)
	for _, i := range raw.Interfaces {
		d.Interfaces = append(d.Interfaces, i.Name)
	}
	d.Errors = append(d.Errors, raw.Errors...)
	return d, nil
}

// CheckPair proves the identity of what would be paired, saving nothing.
func (h *Hub) CheckPair(ctx context.Context, r PairRequest) (IdentityCheck, error) {
	var c IdentityCheck
	args, err := r.args()
	if err != nil {
		return c, err
	}
	err = h.cli(ctx, 3*time.Minute, &c, append([]string{"machine", "pair", "--check"}, args...)...)
	return c, err
}

// Pair saves a machine, if it proves the identity the user confirmed.
func (h *Hub) Pair(ctx context.Context, r PairRequest, expectID string) error {
	if !identityRE.MatchString(expectID) {
		return errors.New("confirm an identity first")
	}
	args, err := r.args()
	if err != nil {
		return err
	}
	return h.cli(ctx, 3*time.Minute, nil, append([]string{"machine", "pair", "--yes", "--expect-id", expectID}, args...)...)
}

// CheckTrust proves a saved machine's identity, saving nothing.
func (h *Hub) CheckTrust(ctx context.Context, name string) (IdentityCheck, error) {
	var c IdentityCheck
	if err := validMachine(name); err != nil {
		return c, err
	}
	err := h.cli(ctx, 2*time.Minute, &c, "machine", "trust", name, "--check")
	return c, err
}

// Trust pins the identity the user confirmed for a saved machine.
func (h *Hub) Trust(ctx context.Context, name, expectID string) error {
	if err := validMachine(name); err != nil {
		return err
	}
	if !identityRE.MatchString(expectID) {
		return errors.New("confirm an identity first")
	}
	return h.cli(ctx, 2*time.Minute, nil, "machine", "trust", name, "--yes", "--expect-id", expectID)
}

// MachineAction enables, disables, revokes or removes a saved machine.
func (h *Hub) MachineAction(ctx context.Context, name, action string) error {
	if err := validMachine(name); err != nil {
		return err
	}
	switch action {
	case "enable", "disable", "revoke", "remove":
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return h.cli(ctx, 30*time.Second, nil, "machine", action, name)
}
