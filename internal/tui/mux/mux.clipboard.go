package mux

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image/png"
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

// toolTimeout bounds the clipboard tool and the link opener.
const toolTimeout = 5 * time.Second

// maxOSC52 is the largest copy sent through the terminal; many terminals
// drop longer OSC 52 sequences, and base64 grows them by a third.
const maxOSC52 = 74 << 10

// DefaultImageMaxBytes bounds a clipboard image unless configured.
const DefaultImageMaxBytes = 5 << 20

// maxClipboardPixels is the daemon's hard pixel ceiling; the daemon checks
// its own configured bound authoritatively.
const maxClipboardPixels = 64 << 20

// imageSendTimeout bounds one image paste, retries included, on a slow
// remote link.
const imageSendTimeout = 3 * time.Minute

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
				// Not a.ctx: copying and then leaving at once must still copy
				// (Close waits for it).
				ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
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

// readClipboardImagePNG reads only PNG image data from the local clipboard.
// It never falls back to text, HTML, or an image URL.
func readClipboardImagePNG(ctx context.Context, limit int) ([]byte, error) {
	candidates := [][]string{}
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pngpaste", "-"}}
	case "linux":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, []string{"wl-paste", "--no-newline", "--type", "image/png"})
		}
		candidates = append(candidates, []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"})
	}
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("pngpaste"); err != nil {
			// NSPasteboard may expose TIFF rather than PNG. JXA converts either
			// representation to PNG and prints base64 so the bridge stays binary-safe.
			const script = `ObjC.import("AppKit"); var p=$.NSPasteboard.generalPasteboard; var d=p.dataForType($.NSPasteboardTypePNG); if(!d){d=p.dataForType($.NSPasteboardTypeTIFF);} if(!d){throw Error("no image");} var r=$.NSBitmapImageRep.alloc.initWithData(d); var o=r.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $({})); if(!o){throw Error("PNG conversion failed");} ObjC.unwrap(o.base64EncodedStringWithOptions(0));`
			cmd := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", script)
			encoded, err := cmd.Output()
			if err == nil && len(encoded) <= (limit+2)/3*4+16 {
				data, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
				if decErr == nil {
					cfg, imgErr := png.DecodeConfig(bytes.NewReader(data))
					if imgErr == nil && len(data) <= limit && cfg.Width > 0 && cfg.Height > 0 && int64(cfg.Width)*int64(cfg.Height) <= maxClipboardPixels {
						return data, nil
					}
				}
			}
		}
	}
	var lastErr error
	for _, argv := range candidates {
		if _, err := exec.LookPath(argv[0]); err != nil {
			lastErr = err
			continue
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		var out limitedBuffer
		out.limit = limit
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			lastErr = err
			continue
		}
		data := out.Bytes()
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxClipboardPixels {
			return nil, fmt.Errorf("clipboard image is not a supported PNG")
		}
		return data, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no local PNG clipboard reader is available")
	}
	return nil, lastErr
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, fmt.Errorf("clipboard image exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(p)
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
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout) // outlives a quick detach
	cmd := exec.CommandContext(ctx, opener, url)
	if err := cmd.Start(); err != nil {
		cancel()
		a.copyText(url)
		return
	}
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		defer cancel()
		_ = cmd.Wait()
	}()
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// passOnCopies puts text that programs in visible panes copied (OSC 52)
// on the clipboard, as configured: a copy in an agent on another machine
// lands on the clipboard of the one the user sits at.
func (a *App) passOnCopies() {
	for _, v := range a.views {
		v.mu.Lock()
		copies := v.copies
		v.copies = nil
		v.mu.Unlock()
		for _, text := range copies {
			a.clipboard(text)
		}
	}
}
