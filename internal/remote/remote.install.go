package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrNotInstalled means the host has no (usable) hive.
var ErrNotInstalled = errors.New("hive is not installed")

// InstallScriptURL is the release installer, for hosts whose system differs
// from this one (so this binary cannot be copied there).
const InstallScriptURL = "https://raw.githubusercontent.com/admirable-oss/hive/main/distribution/install.sh"

// Probe is what a host has.
type Probe struct {
	// OS and Arch are the host's, as Go names them (linux, arm64); empty
	// when unknown.
	OS, Arch string
	// Path is the hive binary found there; empty when there is none.
	Path string
	// Version and APILevel are that hive's (see `hive version`).
	Version  string
	APILevel int
}

// Installed reports whether the host has a hive that runs.
func (p Probe) Installed() bool { return p.Path != "" && p.Version != "" }

// Probe looks at the host: its system, and the hive it has.
func (t Target) Probe(ctx context.Context) (Probe, error) {
	script := t.hive() + `
printf 'os=%s\narch=%s\n' "$(uname -s)" "$(uname -m)"
if [ -x "$h" ]; then
	printf 'path=%s\n' "$h"
	printf 'version='; "$h" version --json 2>/dev/null | tr -d '\n'; echo
fi`
	out, err := t.Shell(ctx, script, nil)
	if err != nil {
		return Probe{}, err
	}
	return parseProbe(out), nil
}

func parseProbe(out []byte) Probe {
	var p Probe
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "os":
			p.OS = GoOS(v)
		case "arch":
			p.Arch = GoArch(v)
		case "path":
			p.Path = v
		case "version":
			var info struct {
				Version  string `json:"version"`
				APILevel int    `json:"api_level"`
			}
			if json.Unmarshal([]byte(v), &info) == nil {
				p.Version, p.APILevel = info.Version, info.APILevel
			}
		}
	}
	return p
}

// GoOS maps `uname -s` to Go's name for the system.
func GoOS(uname string) string {
	switch strings.ToLower(strings.TrimSpace(uname)) {
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	}
	return strings.ToLower(strings.TrimSpace(uname))
}

// GoArch maps `uname -m` to Go's name for the architecture.
func GoArch(uname string) string {
	switch m := strings.ToLower(strings.TrimSpace(uname)); m {
	case "x86_64", "amd64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return m
	}
}

// Install copies a hive binary (bin) to the host: over the hive it has, or
// to ~/.local/bin/hive. The swap is atomic, so a daemon and shims running
// the old one keep it. It returns where the binary went.
func (t Target) Install(ctx context.Context, bin io.Reader) (string, error) {
	script := `set -e
` + t.hive() + `
case "$h" in */*) ;; *) h="$HOME/.local/bin/hive" ;; esac
d=$(dirname "$h")
mkdir -p "$d"
tmp="$d/.hive-install.$$"
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
chmod 755 "$tmp"
mv -f "$tmp" "$h"
printf '%s\n' "$h"`
	out, err := t.Shell(ctx, script, bin)
	if err != nil {
		return "", fmt.Errorf("install hive: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// InstallRelease installs the latest release on the host with the release
// installer (for a host whose system differs from this one).
func (t Target) InstallRelease(ctx context.Context) error {
	script := `set -e
if command -v curl >/dev/null 2>&1; then curl -fsSL ` + Quote(InstallScriptURL) + ` | sh
else wget -qO- ` + Quote(InstallScriptURL) + ` | sh; fi`
	if _, err := t.Shell(ctx, script, nil); err != nil {
		return fmt.Errorf("install the latest release: %w", err)
	}
	return nil
}

// RestartDaemon restarts the host's daemon on the hive installed there,
// keeping its agents (and starts it when it is not running).
func (t Target) RestartDaemon(ctx context.Context) error {
	cmd, err := t.Command(ctx, false, "daemon", "restart")
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return sshError(t.Host, err, stderr.String())
	}
	return nil
}
