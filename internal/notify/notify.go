// Package notify tells the user when an agent wants them: it is blocked on
// a decision, or done. The UI raises a notification through any of a toast,
// the outer terminal (OSC 9 or OSC 777, which reach the user's own
// terminal over SSH too), the desktop's notifications (osascript,
// notify-send) and a sound.
package notify

import (
	"context"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Terminal modes: how the outer terminal is asked to notify.
const (
	TerminalAuto   = "auto"   // OSC 9, except in terminals known to lack it
	TerminalOSC9   = "osc9"   // iTerm2, WezTerm, Ghostty, kitty, foot, …
	TerminalOSC777 = "osc777" // rxvt-unicode, Ghostty, foot, …
	TerminalOff    = "off"
)

// TerminalModes lists the [notify] terminal values.
var TerminalModes = []string{TerminalAuto, TerminalOSC9, TerminalOSC777, TerminalOff}

// Config is the [notify] section.
type Config struct {
	// On lists the states that notify: blocked, done.
	On       []string
	Toast    bool
	Terminal string // see TerminalModes
	// System uses the desktop's notifications; never over SSH, where they
	// would show on the remote machine.
	System bool
	// Sound plays a sound (the system's, or the terminal bell).
	Sound bool
	// Agents overrides On per agent kind ([notify.agents] codex =
	// ["blocked"]); an empty list silences that agent.
	Agents map[string][]string
}

// Defaults returns the default [notify] section.
func Defaults() Config {
	return Config{On: []string{"blocked", "done"}, Toast: true, Terminal: TerminalAuto}
}

// Wants reports whether an agent of kind entering state notifies.
func (c Config) Wants(kind, state string) bool {
	if kind == "" {
		return false // not a recognised agent: a shell finishing is not news
	}
	on, ok := c.Agents[kind]
	if !ok {
		on = c.On
	}
	return slices.Contains(on, state)
}

// Note is one notification.
type Note struct {
	Title string // "Claude Code · api"
	Body  string // "needs your decision"
}

// TerminalMode resolves auto for the terminal described by getenv.
func TerminalMode(mode string, getenv func(string) string) string {
	if mode != TerminalAuto && mode != "" {
		return mode
	}
	switch getenv("TERM_PROGRAM") {
	case "Apple_Terminal", "vscode":
		return TerminalOff // they show OSC 9 as nothing at best
	}
	return TerminalOSC9
}

// Sequence is what to write to the outer terminal for n: the notification
// escape, then a bell for a sound. Empty means nothing.
func Sequence(mode string, n Note, sound bool) string {
	var b strings.Builder
	switch mode {
	case TerminalOSC9:
		b.WriteString("\x1b]9;" + clean(n.Title+": "+n.Body) + "\x07")
	case TerminalOSC777:
		b.WriteString("\x1b]777;notify;" + strings.ReplaceAll(clean(n.Title), ";", ",") + ";" + clean(n.Body) + "\x07")
	}
	if sound {
		b.WriteString("\a")
	}
	return b.String()
}

// clean drops control characters, which would end or corrupt the escape.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// SystemCommand returns the command that shows n on this desktop, nil when
// there is none.
func SystemCommand(goos string, n Note, sound bool, lookPath func(string) (string, error)) []string {
	switch goos {
	case "darwin":
		script := "display notification " + appleString(n.Body) + " with title " + appleString(n.Title)
		if sound {
			script += ` sound name "Glass"`
		}
		return []string{"osascript", "-e", script}
	case "linux", "freebsd", "openbsd", "netbsd":
		if _, err := lookPath("notify-send"); err != nil {
			return nil
		}
		return []string{"notify-send", "--app-name=Hive", n.Title, n.Body}
	}
	return nil
}

func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(clean(s)) + `"`
}

// System shows n with the desktop's notifications, waiting at most a few
// seconds. It does nothing where there is no way to.
func System(ctx context.Context, n Note, sound bool) {
	argv := SystemCommand(runtime.GOOS, n, sound, exec.LookPath)
	if argv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
}

// OverSSH reports whether this process runs in an SSH session.
func OverSSH(getenv func(string) string) bool {
	return getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != ""
}
