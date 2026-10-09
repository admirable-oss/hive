package process

import (
	"maps"
	"os"
	"slices"
	"strings"
)

// LaunchEnv is what every process's environment is built from, in order:
// Base (the daemon's environment, minus StripPrefixes), the environment's
// variables, the request's variables, then Vars and the per-process HIVE_*
// variables, which always win.
type LaunchEnv struct {
	Base []string
	// Vars are added to every process (HIVE_SOCKET_PATH, HIVE_BIN, …).
	Vars map[string]string
}

// stripped are variables a process must not inherit from the shell that
// started the daemon: they describe that shell's multiplexer or terminal,
// and would make programs inside Hive believe they run inside it (tmux
// refusing to nest, editors talking to the wrong kitty). Entries ending in
// "_" or "*" match prefixes.
var stripped = []string{
	"TMUX", "TMUX_PANE", "STY", "WINDOW",
	"ZELLIJ", "ZELLIJ_*",
	"HERDR_*",
	"WEZTERM_*",
	"KITTY_WINDOW_ID", "KITTY_PID", "KITTY_LISTEN_ON", "KITTY_PUBLIC_KEY",
	"ITERM_SESSION_ID", "ITERM_PROFILE", "TERM_SESSION_ID",
	"ALACRITTY_WINDOW_ID", "ALACRITTY_SOCKET", "ALACRITTY_LOG",
	"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM", "COLORTERM",
	"VSCODE_INJECTION", "VSCODE_GIT_IPC_HANDLE",
	"COLUMNS", "LINES",
	"HIVE_*",
}

func isStripped(name string) bool {
	for _, s := range stripped {
		if prefix, ok := strings.CutSuffix(s, "*"); ok {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		} else if name == s {
			return true
		}
	}
	return false
}

// DaemonLaunchEnv returns a LaunchEnv based on this process's environment.
func DaemonLaunchEnv(vars map[string]string) LaunchEnv {
	return LaunchEnv{Base: os.Environ(), Vars: vars}
}

// build returns the environment of a process, as KEY=VALUE entries sorted
// by key, each key once.
func (l LaunchEnv) build(layers ...map[string]string) []string {
	merged := map[string]string{}
	for _, kv := range l.Base {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" && !isStripped(k) {
			merged[k] = v
		}
	}
	for _, layer := range append(layers, l.Vars) {
		maps.Copy(merged, layer)
	}
	out := make([]string, 0, len(merged))
	for _, k := range slices.Sorted(maps.Keys(merged)) {
		out = append(out, k+"="+merged[k])
	}
	return out
}
