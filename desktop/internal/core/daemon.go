package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// EnvHive names the hive binary the app uses, overriding the search.
const EnvHive = "HIVE_BIN"

// ErrNoHive means no hive binary was found.
var ErrNoHive = errors.New("hive is not installed (install it, or set HIVE_BIN)")

// FindHive finds the hive binary: explicit, $HIVE_BIN, one bundled with the
// app (beside it, or in the macOS bundle's Resources), then the PATH and
// ~/.local/bin, where install.sh puts it.
func FindHive(explicit string) (string, error) {
	exe := "hive"
	if runtime.GOOS == "windows" {
		exe = "hive.exe"
	}
	var candidates []string
	for _, c := range []string{explicit, os.Getenv(EnvHive)} {
		if c != "" {
			candidates = append(candidates, c)
		}
	}
	if self, err := os.Executable(); err == nil {
		dir := filepath.Dir(self)
		candidates = append(candidates, filepath.Join(dir, exe), filepath.Join(dir, "..", "Resources", exe))
	}
	if p, err := exec.LookPath(exe); err == nil {
		candidates = append(candidates, p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", exe))
		if runtime.GOOS == "windows" {
			if local := os.Getenv("LOCALAPPDATA"); local != "" {
				candidates = append(candidates, filepath.Join(local, "Programs", "Hive", exe))
			}
		}
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() { //nolint:gosec // G703: known install places, probed
			return c, nil
		}
	}
	return "", ErrNoHive
}

// hiveCommand runs hive with args for the configured home and session.
// Started from the Dock or the Start menu, the app has a bare environment;
// on macOS and Linux hive runs through the user's login shell, so the
// daemon it may start (and every agent under it) gets the user's PATH.
func (h *Hub) hiveCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	hive, err := FindHive(h.cfg.Hive)
	if err != nil {
		return nil, err
	}
	if h.cfg.Session != "" {
		args = append([]string{"--session", h.cfg.Session}, args...)
	}
	var cmd *exec.Cmd
	shell := os.Getenv("SHELL")
	if runtime.GOOS != "windows" && h.cfg.LoginShell && shell != "" {
		// "$0" is hive and "$@" its arguments: nothing is interpreted.
		cmd = exec.CommandContext(ctx, shell, append([]string{"-l", "-c", `exec "$0" "$@"`, hive}, args...)...) //nolint:gosec // G702: the user's shell; hive and args are passed as arguments, never parsed
	} else {
		cmd = exec.CommandContext(ctx, hive, args...)
	}
	cmd.Env = os.Environ()
	if h.cfg.Home != "" {
		cmd.Env = append(cmd.Env, "HIVE_HOME="+h.cfg.Home)
	}
	return cmd, nil
}

// startDaemon starts the daemon (`hive daemon start`).
func (h *Hub) startDaemon(ctx context.Context) error {
	cmd, err := h.hiveCommand(ctx, "daemon", "start")
	if err != nil {
		return err
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start the hive daemon: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// Settings are the parts of the user's Hive configuration the app follows,
// read through `hive config show --json` so there is one config file.
type Settings struct {
	// NotifyOn are the agent states worth a notification.
	NotifyOn []string `json:"notify_on"`
	// SystemNotifications says the daemon posts system notifications
	// itself (notify.system); the app then posts none, so none come twice.
	SystemNotifications bool `json:"system_notifications"`
	// Theme is the configured theme name ("auto": follow the system).
	Theme string `json:"theme"`
	// Prefix is the multiplexer's prefix key, shown in the app's hints.
	Prefix []string `json:"prefix"`
	// Keys are the effective key bindings, as `hive config keys` prints
	// them: the built-in ones with the [keys] section applied. The app
	// follows the prefix-mode bindings, as `hive ui` does.
	Keys []KeyBinding `json:"keys"`
	// UpdateChannel is update.channel: stable, or preview for release
	// candidates too. The app's own updates follow it.
	UpdateChannel string `json:"update_channel"`
}

// KeyBinding is one binding of the effective key map.
type KeyBinding struct {
	Mode   string   `json:"mode"`
	Action string   `json:"action"`
	Keys   []string `json:"keys"`
}

// DefaultSettings are used when the configuration cannot be read.
func DefaultSettings() Settings {
	return Settings{NotifyOn: []string{"blocked", "done"}, Theme: "auto", Prefix: []string{"ctrl+b"}, Keys: []KeyBinding{}, UpdateChannel: "stable"}
}

func parseSettings(data []byte) (Settings, error) {
	var cfg struct {
		Notify struct {
			On     []string `json:"on"`
			System bool     `json:"system"`
		} `json:"notify"`
		Theme struct {
			Name string `json:"name"`
		} `json:"theme"`
		Keys struct {
			PrefixKeys []string `json:"prefix_keys"`
		} `json:"keys"`
		Update struct {
			Channel string `json:"channel"`
		} `json:"update"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultSettings(), fmt.Errorf("read hive's configuration: %w", err)
	}
	s := Settings{NotifyOn: cfg.Notify.On, SystemNotifications: cfg.Notify.System, Theme: cfg.Theme.Name, Prefix: cfg.Keys.PrefixKeys, Keys: []KeyBinding{}, UpdateChannel: cfg.Update.Channel}
	if s.UpdateChannel == "" {
		s.UpdateChannel = "stable"
	}
	if s.NotifyOn == nil {
		s.NotifyOn = []string{}
	}
	if s.Theme == "" {
		s.Theme = "auto"
	}
	return s, nil
}

// readSettings reads the configuration through the CLI.
func (h *Hub) readSettings(ctx context.Context) (Settings, error) {
	cmd, err := h.hiveCommand(ctx, "config", "show", "--json")
	if err != nil {
		return DefaultSettings(), err
	}
	out, err := cmd.Output()
	if err != nil {
		return DefaultSettings(), fmt.Errorf("hive config show: %w", err)
	}
	s, err := parseSettings(out)
	if err != nil {
		return s, err
	}
	// The key map, best effort: a hive older than `config keys` leaves the
	// app on its own shortcuts.
	if cmd, err := h.hiveCommand(ctx, "config", "keys", "--json"); err == nil {
		if out, err := cmd.Output(); err == nil {
			if keys, err := parseKeys(out); err == nil {
				s.Keys, s.Prefix = keys.Bindings, keys.Prefix
			}
		}
	}
	return s, nil
}

type keyMap struct {
	Prefix   []string     `json:"prefix"`
	Bindings []KeyBinding `json:"bindings"`
}

func parseKeys(data []byte) (keyMap, error) {
	var km keyMap
	if err := json.Unmarshal(data, &km); err != nil {
		return keyMap{}, fmt.Errorf("read hive's key map: %w", err)
	}
	if km.Bindings == nil {
		km.Bindings = []KeyBinding{}
	}
	return km, nil
}

func (s Settings) notifies(state string) bool { return slices.Contains(s.NotifyOn, state) }
