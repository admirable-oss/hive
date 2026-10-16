package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/remote"
)

// remoteArgs are the global flags that send a command to another machine.
type remoteArgs struct {
	host, machine, session string
	yes                    bool
}

// globalFlags are read before cobra parses the command line, because they
// decide what the whole command tree talks to. With a value: --remote,
// --machine, --session; without: --yes.
var globalFlags = map[string]bool{"--remote": true, "--machine": true, "--session": true, "--yes": false}

// parseRemoteArgs finds the remote flags before any "--".
func parseRemoteArgs(args []string) (remoteArgs, bool) {
	var r remoteArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, value, inline := strings.Cut(arg, "=")
		takes, ok := globalFlags[name]
		if !ok {
			continue
		}
		if takes && !inline && i+1 < len(args) {
			value = args[i+1]
			i++
		}
		switch name {
		case "--remote":
			r.host = value
		case "--machine":
			r.machine = value
		case "--session":
			r.session = value
		case "--yes":
			r.yes = !inline || value == "true"
		}
	}
	return r, r.host != "" || r.machine != ""
}

// withoutRemoteArgs drops the global flags the remote side must not see
// (it gets the session from the target).
func withoutRemoteArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(out, args[i:]...)
		}
		name, _, inline := strings.Cut(arg, "=")
		if takes, ok := globalFlags[name]; ok {
			if takes && !inline {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

// machines is the registry of saved machines, shared by every session.
func (a *app) machines() *remote.Registry {
	return remote.NewRegistry(filepath.Join(a.base, "machines.json"))
}

// controlDir holds the sockets of shared SSH connections. It is kept short:
// a unix socket path must fit in about 100 bytes.
func (a *app) controlDir() string {
	if goruntime.GOOS == "windows" {
		return "" // Windows' OpenSSH has no connection sharing (ControlMaster)
	}
	dir := filepath.Join(a.base, "ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// target completes a remote target with this machine's settings.
func (a *app) target(t remote.Target) remote.Target {
	t.ControlDir = a.controlDir()
	if a.getenv(remote.EnvSSH) == "" {
		t.SSH = a.cfg.SSHArgv()
	}
	return t
}

// remoteClient is a client of the daemon on t.
func (a *app) remoteClient(t remote.Target, name string) client.Client {
	return client.NewService(client.Config{
		Name: name, Dial: t.Dial, Remote: true,
		FrameBudget: a.cfg.Remote.FrameBudgetKB << 10,
		ImageBudget: a.cfg.Remote.ImageBudgetKB << 10,
	})
}

// useRemote points the app at another machine: a host, or a saved machine.
func (a *app) useRemote(r remoteArgs) error {
	var t remote.Target
	switch {
	case r.machine != "":
		m, err := a.machines().Get(r.machine)
		if err != nil {
			return err
		}
		if m.Disabled {
			return fmt.Errorf("machine %q is disabled (`hive machine enable %s`)", m.Name, m.Name)
		}
		t = m.Target("")
		a.remoteName = m.Name
	default:
		t = remote.Target{Host: r.host}
		a.remoteName = r.host
	}
	if r.session != "" {
		t.Session = r.session
	}
	t = a.target(t)
	if _, err := t.Command(context.Background(), false); err != nil {
		return err // an invalid host
	}
	if a.client != nil {
		_ = a.client.Close()
	}
	a.remote, a.yes = &t, r.yes
	a.client = a.remoteClient(t, "cli")
	return nil
}

// localCommands run on this machine even with --remote: they are about
// this machine, or work the same anywhere. (`hive observer` commands that
// act as an observer reach the remote observer socket themselves, with the
// token from this machine; the web observer runs here and reaches runtimes
// itself.)
var localCommands = []string{"machine", "completion", "help", "version", "proxy", "observer", "web", "__complete", "__completeNoDesc"}

// passThrough runs a command that has no daemon client of its own (daemon
// management, configuration, integrations, update) on the remote machine
// as a whole: ssh runs `hive <args>` there. It reports whether it did.
func (a *app) passThrough(ctx context.Context, root *cobra.Command, args []string, stdout, stderr io.Writer) (int, bool) {
	cmd, _, err := root.Find(args)
	if err != nil || cmd.Annotations[annDaemon] == "true" {
		return 0, false
	}
	top := cmd
	for top.HasParent() && top.Parent() != root {
		top = top.Parent()
	}
	if slices.Contains(localCommands, top.Name()) {
		return 0, false
	}
	tty := term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
	run := func() (int, error) {
		c, err := a.remote.Command(ctx, tty, withoutRemoteArgs(args)...)
		if err != nil {
			return exitUsage, err
		}
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, stdout, stderr
		err = c.Run()
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	code, err := run()
	if err == nil && code == 127 {
		// The remote shell found no hive: offer to install it, then try again.
		if err = a.ensureInstalled(ctx); err == nil {
			code, err = run()
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "hive:", err)
		return exitError, true
	}
	return code, true
}

// ensureRemote makes sure the remote machine has a daemon this client can
// talk to: hive installed there and not older than this one. It offers to
// install or update it.
func (a *app) ensureRemote(ctx context.Context) error {
	err := a.client.Ping(ctx)
	if err == nil {
		st, serr := a.client.Status(ctx)
		if serr == nil && st.APILevel < protocol.APILevel {
			p, perr := a.remote.Probe(ctx)
			if perr != nil {
				return perr
			}
			return a.bootstrap(ctx, p, fmt.Sprintf("hive %s on %s is older than this one (%s)", st.Version, a.remoteName, buildinfo.Get().Version), true)
		}
		return nil
	}
	p, perr := a.remote.Probe(ctx)
	switch {
	case perr != nil:
		return perr // ssh itself failed: its message says why
	case !p.Installed():
		return a.bootstrap(ctx, p, "hive is not installed on "+a.remoteName, false)
	case p.APILevel < protocol.APILevel:
		return a.bootstrap(ctx, p, fmt.Sprintf("hive %s on %s is older than this one (%s)", p.Version, a.remoteName, buildinfo.Get().Version), false)
	}
	return err
}

// ensureInstalled offers to install hive on the remote machine when it has
// none.
func (a *app) ensureInstalled(ctx context.Context) error {
	p, err := a.remote.Probe(ctx)
	if err != nil {
		return err
	}
	if p.Installed() {
		return nil
	}
	return a.bootstrap(ctx, p, "hive is not installed on "+a.remoteName, false)
}

// bootstrap installs hive on the remote machine, after confirmation: a
// copy of this binary when the systems match, else the latest release.
// running says a daemon runs there, which is restarted on the new binary
// (its agents keep running).
func (a *app) bootstrap(ctx context.Context, p remote.Probe, why string, running bool) error {
	same := p.OS == goruntime.GOOS && p.Arch == goruntime.GOARCH
	action := fmt.Sprintf("copy this hive (%s) there", buildinfo.Get().Version)
	if !same {
		action = fmt.Sprintf("install the latest release there (%s/%s)", cmpStr(p.OS, "unknown system"), cmpStr(p.Arch, "?"))
	}
	if !a.confirm(why + "; " + action + "?") {
		return fmt.Errorf("%s; run again with --yes to %s", why, action)
	}
	if same {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			return fmt.Errorf("locate this hive: %w", err)
		}
		f, err := os.Open(exe)
		if err != nil {
			return err
		}
		defer f.Close()
		path, err := a.remote.Install(ctx, f)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.errOut, "hive: installed hive on %s at %s\n", a.remoteName, path)
	} else {
		if err := a.remote.InstallRelease(ctx); err != nil {
			return err
		}
		fmt.Fprintf(a.errOut, "hive: installed the latest hive release on %s\n", a.remoteName)
	}
	if running {
		if err := a.remote.RestartDaemon(ctx); err != nil {
			return fmt.Errorf("restart the daemon on %s: %w", a.remoteName, err)
		}
		fmt.Fprintf(a.errOut, "hive: restarted the daemon on %s (agents kept running)\n", a.remoteName)
	}
	_ = a.client.Close() // the next call connects to the new one
	return a.client.Ping(ctx)
}

// confirm asks a yes/no question on the terminal; --yes answers it. Without
// a terminal the answer is no.
func (a *app) confirm(question string) bool {
	if a.yes {
		return true
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return false
	}
	fmt.Fprintf(a.errOut, "hive: %s [y/N] ", question)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
