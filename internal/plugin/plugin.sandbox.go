package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
}

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

// sandboxTool names what strict isolation needs on this system.
func sandboxTool() string {
	if runtime.GOOS == "linux" {
		return "bubblewrap (bwrap)"
	}
	return "sandbox-exec"
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
		return append(bwrapArgs(s), argv...), nil
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

// darwinProfile is the SBPL profile for s. Later rules take precedence,
// so the general denials come first and the plugin's own allowances after.
func darwinProfile(s sandboxSpec) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	if !s.Network {
		b.WriteString("(deny network*)\n")
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
	if s.FilesystemWrite {
		args = append(args, "--bind", "/", "/")
	} else {
		args = append(args, "--ro-bind", "/", "/")
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp")
	for _, d := range s.Writable {
		args = append(args, "--bind", d, d)
	}
	if !s.Network {
		args = append(args, "--unshare-net")
	}
	if s.DaemonSocket != "" {
		args = append(args, "--ro-bind", "/dev/null", s.DaemonSocket)
	}
	return append(args, "--")
}
