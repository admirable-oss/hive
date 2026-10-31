package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// sandboxSpec is what a strictly isolated command may touch.
type sandboxSpec struct {
	// Writable are the plugin's own directories.
	Writable []string
	// Network and FilesystemWrite lift those restrictions (granted
	// capabilities).
	Network         bool
	FilesystemWrite bool
	// Socket is the plugin's own daemon socket, always reachable;
	// DaemonSocket is the daemon's main socket, never reachable.
	Socket       string
	DaemonSocket string

	// Plugin API 2: Confined limits reads to the system, Readable and
	// Writable (unless ReadAll, filesystem.read), and programs to Exec
	// (unless ExecAny).
	Confined bool
	ReadAll  bool
	Readable []string
	Exec     []string
	ExecAny  bool
	// ProxyPort is the plugin's proxy on loopback (macOS); ProxySocket its
	// Unix socket and ProxyToken its credential (Linux).
	ProxyPort   int
	ProxySocket string
	ProxyToken  string
	// Launcher is the hive binary that sets up the Linux sandbox from the
	// inside (Landlock, the proxy).
	Launcher string
}

// darwinSystemReads are what a confined plugin reads on macOS besides its
// own: the system, its libraries and tools, and package managers' trees.
var darwinSystemReads = []string{
	"/System", "/usr", "/bin", "/sbin", "/Library", "/private/etc", "/private/var/db/timezone",
	"/private/var/db/dyld", "/private/var/select", "/private/var/db/xcode_select_link", "/opt/homebrew",
	"/opt/local", "/nix", "/Applications/Xcode.app", "/dev",
}

// linuxSystemReads are bound read-only into a confined plugin's sandbox on
// Linux; anything else (home directories above all) does not exist there.
var linuxSystemReads = []string{"/usr", "/etc", "/opt", "/nix", "/gnu", "/run/current-system", "/var/lib/flatpak"}

// linuxRootLinks are top-level directories that are often symbolic links
// into /usr (merged /usr), kept as links.
var linuxRootLinks = []string{"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32"}

// shells are the interpreters a confined plugin's scripts may run under
// whatever it may run.
var shells = []string{"/bin/sh", "/bin/bash", "/bin/zsh", "/bin/dash", "/usr/bin/sh", "/usr/bin/bash", "/usr/bin/zsh", "/usr/bin/dash", "/usr/bin/env"}

// linuxLoaders are the dynamic loaders programs need executable under
// Landlock.
var linuxLoaders = []string{"/lib/ld-linux*", "/lib64/ld-linux*", "/lib/ld-musl*", "/usr/lib/ld-linux*", "/usr/lib64/ld-linux*", "/lib/*-linux-gnu/ld-linux*", "/usr/lib/*-linux-gnu/ld-linux*"}

// sandboxExec is macOS's sandbox runner.
const sandboxExec = "/usr/bin/sandbox-exec"

var sandboxProbe = sync.OnceValue(func() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := os.Stat(sandboxExec)
		return err == nil
	case "linux":
		_, err := exec.LookPath("bwrap")
		return err == nil
	}
	return false
})

// SandboxAvailable reports whether strict isolation can run here.
func SandboxAvailable() bool { return sandboxProbe() }

// Sandboxes, as plugin.list reports them.
const (
	SandboxNone       = "none"
	SandboxExec       = "sandbox-exec"
	SandboxBubblewrap = "bwrap"
)

// sandboxName is the sandbox strict isolation uses here.
func sandboxName() string {
	switch runtime.GOOS {
	case "darwin":
		return SandboxExec
	case "linux":
		return SandboxBubblewrap
	}
	return SandboxNone
}

// sandboxTool names what strict isolation needs on this system.
func sandboxTool() string {
	switch runtime.GOOS {
	case "linux":
		return "bubblewrap (bwrap)"
	case "darwin":
		return "sandbox-exec"
	}
	return "a sandbox (none on " + runtime.GOOS + ")"
}

// sandboxArgv wraps argv to run under the system sandbox.
func sandboxArgv(argv []string, s sandboxSpec) ([]string, error) {
	if !SandboxAvailable() {
		return nil, fmt.Errorf("strict isolation needs %s", sandboxTool())
	}
	switch runtime.GOOS {
	case "darwin":
		return append([]string{sandboxExec, "-p", darwinProfile(s), "--"}, argv...), nil
	case "linux":
		args := bwrapArgs(s)
		if s.Confined {
			if s.Launcher == "" {
				return nil, fmt.Errorf("the plugin sandbox needs the hive binary")
			}
			args = append(args, s.Launcher, SandboxCommand, "--")
		}
		return append(args, argv...), nil
	}
	return nil, fmt.Errorf("strict isolation is not available on %s", runtime.GOOS)
}

// realPath resolves symlinks (macOS's /var is /private/var), so sandbox
// rules match the paths the kernel sees.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

func sbString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// pathFilters are SBPL filters for paths: a subpath for a directory, a
// literal for a file, each as the kernel resolves it.
func pathFilters(paths []string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, p := range paths {
		for _, q := range []string{p, realPath(p)} {
			if q == "" || seen[q] {
				continue
			}
			seen[q] = true
			if info, err := os.Stat(q); err == nil && !info.IsDir() {
				fmt.Fprintf(&b, " (literal %s)", sbString(q))
			} else {
				fmt.Fprintf(&b, " (subpath %s)", sbString(q))
			}
		}
	}
	return b.String()
}

// darwinProfile is the SBPL profile for s. Later rules take precedence,
// so the general denials come first and the plugin's own allowances after.
func darwinProfile(s sandboxSpec) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	if !s.Network || s.ProxyPort != 0 {
		b.WriteString("(deny network*)\n")
	}
	if s.ProxyPort != 0 {
		// Its own proxy, and nothing else.
		fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", s.ProxyPort)
	}
	if s.Confined && !s.ReadAll && !s.FilesystemWrite {
		// Contents only: metadata stays readable, so paths resolve.
		reads := append(append(append(slices.Clone(darwinSystemReads), s.Readable...), s.Writable...), s.Exec...)
		if s.Socket != "" {
			reads = append(reads, s.Socket)
		}
		b.WriteString("(deny file-read-data)\n(allow file-read-data (literal \"/\")" + pathFilters(reads) + ")\n")
	}
	if s.Confined && !s.ExecAny {
		b.WriteString("(deny process-exec)\n(allow process-exec" + pathFilters(append(slices.Clone(shells), s.Exec...)) + ")\n")
	}
	// No local sockets but its own, network or not: not the daemon's, not
	// another session's, not other programs'.
	b.WriteString("(deny network-outbound (remote unix-socket))\n")
	if s.Socket != "" {
		fmt.Fprintf(&b, "(allow network-outbound (remote unix-socket (path-literal %s)))\n", sbString(realPath(s.Socket)))
	}
	if !s.FilesystemWrite {
		b.WriteString("(deny file-write*)\n(allow file-write*")
		for _, d := range s.Writable {
			fmt.Fprintf(&b, " (subpath %s)", sbString(realPath(d)))
		}
		b.WriteString(` (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/fd/"))` + "\n")
	}
	return b.String()
}

// bwrapArgs runs a command under bubblewrap: a read-only view of the
// system with the plugin's directories writable, no network unless
// granted (Unix sockets on disk still work), and the daemon's main socket
// covered.
func bwrapArgs(s sandboxSpec) []string {
	args := []string{"bwrap", "--die-with-parent"}
	switch {
	case s.FilesystemWrite:
		args = append(args, "--bind", "/", "/")
	case !s.Confined || s.ReadAll:
		args = append(args, "--ro-bind", "/", "/")
	default:
		// Only the system: home directories and everything else are not
		// there at all.
		for _, d := range linuxSystemReads {
			args = append(args, "--ro-bind-try", d, d)
		}
		for _, d := range linuxRootLinks {
			if target, err := os.Readlink(d); err == nil {
				args = append(args, "--symlink", target, d)
			} else {
				args = append(args, "--ro-bind-try", d, d)
			}
		}
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc")
	// A plugin granted writes everywhere sees the real /tmp, so what it
	// writes there stays; any other gets a private one, and what it needs
	// from the host's /tmp (its package, its programs, the launcher, its
	// socket: all there in tests) is bound back in.
	privateTmp := !s.Confined || !s.FilesystemWrite
	if privateTmp {
		args = append(args, "--tmpfs", "/tmp")
	}
	if s.Confined && !s.FilesystemWrite {
		for _, d := range append(append(slices.Clone(s.Readable), s.Exec...), s.Launcher) {
			if d != "" {
				args = append(args, "--ro-bind-try", d, d)
			}
		}
		if s.Socket != "" {
			args = append(args, "--bind-try", s.Socket, s.Socket)
		}
	}
	for _, d := range s.Writable {
		args = append(args, "--bind", d, d)
	}
	if !s.Network || s.ProxySocket != "" {
		args = append(args, "--unshare-net")
	}
	proxySocket := s.ProxySocket // where the real /tmp is, at its own path
	if s.ProxySocket != "" && privateTmp {
		args = append(args, "--bind", s.ProxySocket, sandboxProxySocket)
		proxySocket = sandboxProxySocket
	}
	if s.DaemonSocket != "" {
		args = append(args, "--ro-bind-try", "/dev/null", s.DaemonSocket)
	}
	if s.Confined {
		spec := launchSpec{ExecAny: s.ExecAny, ProxySocket: proxySocket, ProxyToken: s.ProxyToken}
		if !s.ExecAny {
			spec.Exec = append(append(slices.Clone(shells), s.Exec...), loaders()...)
		}
		raw, _ := json.Marshal(spec)
		args = append(args, "--setenv", launchEnv, string(raw))
	}
	return append(args, "--")
}

// sandboxProxySocket is where a plugin's proxy socket is inside its
// sandbox when /tmp is the sandbox's own.
const sandboxProxySocket = "/tmp/.hive-proxy.sock"

// loaders are this system's dynamic loaders.
func loaders() []string {
	var out []string
	for _, pattern := range linuxLoaders {
		m, _ := filepath.Glob(pattern)
		out = append(out, m...)
	}
	return out
}
