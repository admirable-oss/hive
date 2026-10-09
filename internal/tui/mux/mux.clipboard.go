package mux

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Clipboard says where copied text goes.
type Clipboard string

const (
	// ClipboardAuto sends OSC 52 to the outer terminal, and also uses the
	// local clipboard tool when not over SSH.
	ClipboardAuto Clipboard = "auto"
	// ClipboardOSC52 only asks the outer terminal (works over SSH when the
	// terminal allows it).
	ClipboardOSC52 Clipboard = "osc52"
	// ClipboardLocal only runs the local tool (pbcopy, wl-copy, xclip,
	// xsel).
	ClipboardLocal Clipboard = "local"
	// ClipboardOff keeps copies inside Hive (paste with the paste key).
	ClipboardOff Clipboard = "off"
)

// ParseClipboard accepts the [ui] clipboard values.
func ParseClipboard(s string) (Clipboard, bool) {
	switch c := Clipboard(strings.ToLower(strings.TrimSpace(s))); c {
	case "", ClipboardAuto:
		return ClipboardAuto, true
	case ClipboardOSC52, ClipboardLocal, ClipboardOff:
		return c, true
	}
	return ClipboardAuto, false
}

// maxOSC52 is the largest copy sent through the terminal; many terminals
// drop longer OSC 52 sequences, and base64 grows them by a third.
const maxOSC52 = 74 << 10

// clipboard copies text as configured.
func (a *App) clipboard(text string) {
	mode := a.opts.Clipboard
	if mode == ClipboardOff {
		return
	}
	if (mode == ClipboardAuto || mode == ClipboardOSC52) && len(text) <= maxOSC52 {
		a.out = append(a.out, ansi.SetSystemClipboard(text))
	}
	if mode == ClipboardLocal || (mode == ClipboardAuto && os.Getenv("SSH_TTY") == "" && os.Getenv("SSH_CONNECTION") == "") {
		if argv := localClipboard(); argv != nil {
			a.bg.Add(1)
			go func() {
				defer a.bg.Done()
				ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Stdin = strings.NewReader(text)
				_ = cmd.Run()
			}()
		}
	}
}

// localClipboard finds the command that sets the system clipboard.
func localClipboard() []string {
	candidates := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	if runtime.GOOS == "darwin" {
		candidates = [][]string{{"pbcopy"}}
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" && runtime.GOOS != "darwin" {
		candidates = candidates[1:]
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err == nil {
			return c
		}
	}
	return nil
}

// openURL opens a link with the system's handler.
func (a *App) openURL(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if _, err := exec.LookPath(opener); err != nil {
		a.copyText(url)
		return
	}
	cmd := exec.CommandContext(a.ctx, opener, url)
	if err := cmd.Start(); err != nil {
		a.copyText(url)
		return
	}
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		_ = cmd.Wait()
	}()
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
