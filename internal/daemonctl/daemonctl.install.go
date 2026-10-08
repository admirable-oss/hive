package daemonctl

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Commander runs service-manager commands. It is a port so tests can check
// the exact commands without touching launchd or systemd.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecCommander runs commands for real.
type ExecCommander struct{}

func (ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// ErrUnsupportedOS means Hive has no service integration for this OS.
var ErrUnsupportedOS = errors.New("installing the daemon as a service is supported on macOS (launchd) and Linux (systemd)")

// Manager installs and removes the daemon's service definition.
type Manager struct {
	GOOS string // runtime.GOOS unless overridden in tests
	Home string // the user's home directory
	UID  int
	Cmd  Commander
}

// NewManager returns a Manager for the current user and OS.
func NewManager(home string) Manager {
	return Manager{GOOS: runtime.GOOS, Home: home, UID: os.Getuid(), Cmd: ExecCommander{}}
}

// Path is where the service definition lives.
func (m Manager) Path() (string, error) {
	switch m.GOOS {
	case "darwin":
		return filepath.Join(m.Home, "Library", "LaunchAgents", Label+".plist"), nil
	case "linux":
		return filepath.Join(m.Home, ".config", "systemd", "user", Unit), nil
	}
	return "", ErrUnsupportedOS
}

// Render returns the service definition for spec on this OS.
func (m Manager) Render(spec UnitSpec) ([]byte, error) {
	switch m.GOOS {
	case "darwin":
		return LaunchdPlist(spec), nil
	case "linux":
		return SystemdUnit(spec), nil
	}
	return nil, ErrUnsupportedOS
}

// Install writes the service definition and (re)loads it so the daemon runs
// now and at every login. Re-installing replaces an existing definition.
func (m Manager) Install(ctx context.Context, spec UnitSpec) (string, error) {
	path, err := m.Path()
	if err != nil {
		return "", err
	}
	data, err := m.Render(spec)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(spec.LogDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}

	switch m.GOOS {
	case "darwin":
		domain := "gui/" + strconv.Itoa(m.UID)
		// bootout fails when the agent is not loaded; that is fine.
		_, _ = m.Cmd.Run(ctx, "launchctl", "bootout", domain+"/"+Label)
		if _, err := m.Cmd.Run(ctx, "launchctl", "bootstrap", domain, path); err != nil {
			return path, err
		}
	case "linux":
		if _, err := m.Cmd.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return path, err
		}
		if _, err := m.Cmd.Run(ctx, "systemctl", "--user", "enable", "--now", Unit); err != nil {
			return path, err
		}
	}
	return path, nil
}

// Uninstall stops the service and removes its definition. The daemon stops
// its agents as it shuts down. Uninstalling twice is not an error.
func (m Manager) Uninstall(ctx context.Context) (string, error) {
	path, err := m.Path()
	if err != nil {
		return "", err
	}
	switch m.GOOS {
	case "darwin":
		_, _ = m.Cmd.Run(ctx, "launchctl", "bootout", "gui/"+strconv.Itoa(m.UID)+"/"+Label)
	case "linux":
		_, _ = m.Cmd.Run(ctx, "systemctl", "--user", "disable", "--now", Unit)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return path, err
	}
	if m.GOOS == "linux" {
		_, _ = m.Cmd.Run(ctx, "systemctl", "--user", "daemon-reload")
	}
	return path, nil
}
