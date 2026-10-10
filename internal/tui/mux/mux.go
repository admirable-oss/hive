// Package mux is the multiplexer UI: tabs of split panes showing live
// agent terminals, a sidebar of environments and agents, an Overview of
// every agent, copy mode, pickers and prompts. It draws with the compositor
// onto an ultraviolet screen and routes keys and the mouse, by mode, to
// panes and actions (see package keymap).
//
// The App holds the state and logic and is driven by events; Run connects
// it to the real terminal. The workspace itself (environments, tabs, panes,
// layouts) lives in the daemon: the UI asks for changes and redraws from
// what the daemon reports, so every client shows the same workspace.
package mux

import (
	"context"
	"fmt"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/notify"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/tui/theme"
	"github.com/admirable-oss/hive/internal/vt"
)

// Options configure the UI; the zero value is the default.
type Options struct {
	// Keymap resolves keys; nil is the built-in key map.
	Keymap *keymap.Keymap
	// Theme names the colour scheme: auto (default), a built-in theme, or
	// custom with CustomTheme. An unknown name falls back to the default;
	// the caller reports it (see theme.Resolve).
	Theme       string
	CustomTheme *theme.Theme
	// Env is the environment to show first; empty picks the one for Cwd,
	// else the first.
	Env string
	// Cwd is the directory the UI was started in.
	Cwd string
	// HideSidebar starts with the sidebar hidden; it is always hidden at
	// first on screens of compositor.MobileWidth columns or less.
	HideSidebar  bool
	SidebarWidth int // default 28
	// NoMouse leaves the mouse to the outer terminal.
	NoMouse bool
	// Clipboard says where copied text goes.
	Clipboard Clipboard
	// Notify says when and how to tell the user an agent wants them; the
	// zero value is notify.Defaults().
	Notify notify.Config
	// Warnings are shown as toasts when the UI opens (config problems).
	Warnings []string
}

// DefaultSidebarWidth is the sidebar's width unless configured.
const DefaultSidebarWidth = 28

func (o Options) withDefaults() Options {
	if o.Keymap == nil {
		o.Keymap = keymap.Default()
	}
	if o.SidebarWidth <= 0 {
		o.SidebarWidth = DefaultSidebarWidth
	}
	if o.Notify.On == nil && o.Notify.Terminal == "" {
		o.Notify = notify.Defaults()
	}
	if o.Clipboard == "" {
		o.Clipboard = ClipboardAuto
	}
	return o
}

// resolveTheme picks the theme for a dark or light terminal background.
func (o Options) resolveTheme(dark bool) theme.Theme {
	t, _ := theme.Resolve(o.Theme, o.CustomTheme, dark)
	return t
}

// Frame pacing. Screens that change on their own (agents printing) are
// drawn at most once per frameInterval; input, and the echo that follows
// it within echoWindow, are drawn at once, so typing never waits on the
// frame cap.
const (
	frameInterval = 8 * time.Millisecond
	echoWindow    = 50 * time.Millisecond
)

// frameDue reports whether to draw now, given the last frame's time, or
// how long to wait before the next one.
func (a *App) frameDue(now, last time.Time) (bool, time.Duration) {
	if !a.dirty || !a.sized {
		return false, 0
	}
	if a.urgent || now.Sub(a.lastInput) < echoWindow {
		return true, 0
	}
	if since := now.Sub(last); since < frameInterval {
		return false, frameInterval - since
	}
	return true, 0
}

// Run shows the UI on the controlling terminal until the user detaches or
// ctx ends.
func Run(ctx context.Context, c client.Client, opts Options) error {
	t := uv.DefaultTerminal()
	scr := t.Screen()
	if err := t.Start(); err != nil {
		return fmt.Errorf("open the terminal: %w", err)
	}
	a := New(ctx, c, opts)
	out := &terminalOut{scr: scr}
	out.enter(!a.opts.NoMouse)

	defer func() {
		a.Close()
		out.leave()
		_ = t.Stop()
	}()

	a.Start() //nolint:contextcheck // the App's context derives from ctx (New)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	armed := false
	var lastFrame time.Time

	for !a.Quit() {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-t.Events():
			if ws, ok := ev.(uv.WindowSizeEvent); ok {
				_ = scr.Resize(ws.Width, ws.Height)
			}
			a.HandleEvent(ev) //nolint:contextcheck // as above
		case m := <-a.Msgs():
			a.Handle(m) //nolint:contextcheck // as above
		case <-a.Wake():
			a.dirty = true
		case <-timer.C:
			armed = false
		}
		now := time.Now()
		draw, wait := a.frameDue(now, lastFrame)
		if !draw {
			if wait > 0 && !armed {
				timer.Reset(wait)
				armed = true
			}
			continue
		}
		out.frame(a)
		lastFrame = now
	}
	return nil
}

// terminalOut applies frames to the real terminal: cells through the
// renderer's diff, plus the cursor, mouse mode, title and raw sequences.
type terminalOut struct {
	scr    *uv.TerminalScreen
	mouse  bool
	mode   uv.MouseMode
	shown  bool
	shape  vt.CursorShape
	blink  bool
	title  string
	styled bool // the cursor style was changed from the default
}

func (o *terminalOut) enter(mouse bool) {
	o.mouse = mouse
	_ = o.scr.EnterAltScreen()
	_ = o.scr.EnableBracketedPaste()
	_ = o.scr.SetSynchronizedUpdates(true)
	if mouse {
		o.mode = uv.MouseModeDrag
		_ = o.scr.SetMouseMode(o.mode)
	}
	_, _ = o.scr.WriteString(ansi.SetModeFocusEvent + ansi.RequestBackgroundColor)
	_ = o.scr.Flush()
}

func (o *terminalOut) leave() {
	_, _ = o.scr.WriteString(ansi.ResetModeFocusEvent)
	_ = o.scr.Flush()
}

func (o *terminalOut) frame(a *App) {
	res := a.Draw(o.scr)
	for _, s := range a.TakeOutput() {
		_, _ = o.scr.WriteString(s)
	}
	if o.mouse {
		mode := uv.MouseModeDrag
		if a.WantsHover() {
			mode = uv.MouseModeMotion
		}
		if mode != o.mode {
			o.mode = mode
			_ = o.scr.SetMouseMode(mode)
		}
	}
	if title := a.WindowTitle(); title != o.title {
		o.title = title
		_ = o.scr.SetWindowTitle(title)
	}
	if c := res.Cursor; c != nil && !c.Hidden {
		if c.Shape != o.shape || c.Blink != o.blink || !o.styled {
			o.shape, o.blink, o.styled = c.Shape, c.Blink, true
			_ = o.scr.SetCursorStyle(uv.CursorShape(c.Shape), c.Blink)
		}
		_ = o.scr.SetCursorPosition(c.X, c.Y)
		if !o.shown {
			o.shown = true
			_ = o.scr.ShowCursor()
		}
	} else if o.shown {
		o.shown = false
		_ = o.scr.HideCursor()
	}
	_ = o.scr.Render()
	_ = o.scr.Flush()
}

// Draw paints the current frame onto scr (the terminal, or a buffer in
// tests) and returns where the cursor goes and what is clickable.
func (a *App) Draw(scr uv.Screen) compositor.Result {
	s, unlock := a.scene()
	res := a.comp.Draw(scr, s)
	unlock()
	a.last = res
	a.dirty, a.urgent = false, false
	return res
}
