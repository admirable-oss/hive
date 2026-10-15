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
	"sort"
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
	// Machine names the machine the client talks to ("" is this one), and
	// Machines are the others: the sidebar lists their agents, and picking
	// one shows its workspace instead.
	Machine  string
	Machines []Machine
	// Commands are the user's own commands, as the keymap binds them
	// (keymap.CommandAction(n) runs Commands[n]).
	Commands []Command
	// StatusRight is shown on the right of the tab bar.
	StatusRight []StatusSegment
}

// Machine is another machine running Hive, and a client of its daemon.
type Machine struct {
	Name   string
	Client client.Client
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
	probe := &graphicsProbe{active: true}
	out := &terminalOut{scr: scr, graphics: probe}
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
	graphicsTimer := time.NewTimer(graphicsProbeTimeout)
	defer graphicsTimer.Stop()
	graphicsTimerC := graphicsTimer.C
	var lastFrame time.Time

	for !a.Quit() {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-t.Events():
			if out.graphics.handle(ev) {
				continue
			}
			if a.graphicsReply(ev) {
				continue
			}
			if ws, ok := ev.(uv.WindowSizeEvent); ok {
				_ = scr.Resize(ws.Width, ws.Height)
			}
			a.HandleEvent(ev) //nolint:contextcheck // as above
		case <-graphicsTimerC:
			out.graphics.finish()
			a.dirty = true
			graphicsTimerC = nil
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
		out.frame(a) //nolint:contextcheck // as above
		lastFrame = now
	}
	return nil
}

// terminalOut applies frames to the real terminal: cells through the
// renderer's diff, plus the cursor, mouse mode, title and raw sequences.
type terminalOut struct {
	scr                *uv.TerminalScreen
	graphics           *graphicsProbe
	graphicsPlacements map[graphicsPlacementKey]string
	graphicsImages     map[uint32]struct{}
	graphicsNative     []string
	mouse              bool
	mode               uv.MouseMode
	shown              bool
	shape              vt.CursorShape
	blink              bool
	title              string
	styled             bool // the cursor style was changed from the default
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
	_, _ = o.scr.WriteString(kittyGraphicsQuery + primaryDA1Query + itermCapabilitiesQuery + "\x1b[16t")
	_ = o.scr.Flush()
}

func (o *terminalOut) leave() {
	for key := range o.graphicsPlacements {
		_, _ = o.scr.WriteString(deletePlacementSequence(key))
	}
	for id := range o.graphicsImages {
		_, _ = o.scr.WriteString(deleteImageSequence(id))
	}
	_, _ = o.scr.WriteString(ansi.ResetModeFocusEvent)
	_ = o.scr.Flush()
}

func (o *terminalOut) frame(a *App) {
	protocol := o.graphics.protocol()
	kitty := protocol == graphicsKitty
	scene, unlock := a.scene()
	targets := a.graphicsTargets(scene)
	plan := a.graphicsFramePlan(targets, protocol, o.graphics.caps.CellW, o.graphics.caps.CellH, !o.graphics.active)
	native := append(append([]string(nil), plan.sixels...), plan.iterm...)
	sort.Strings(native)
	nativeChanged := !sameStrings(o.graphicsNative, native)
	clearNative := len(o.graphicsNative) > 0 && (nativeChanged || (protocol != graphicsSixel && protocol != graphicsIterm))
	if clearNative {
		bounds := o.scr.Bounds()
		_ = o.scr.Resize(bounds.Max.X-bounds.Min.X, bounds.Max.Y-bounds.Min.Y)
		_, _ = o.scr.WriteString("\x1b[2J\x1b[H")
	}
	res := a.drawScene(o.scr, scene, unlock)
	if protocol == graphicsSixel || protocol == graphicsIterm {
		o.graphicsNative = append(o.graphicsNative[:0], native...)
	} else {
		o.graphicsNative = nil
	}
	if len(plan.cells) > 0 {
		applyCellGraphics(o.scr, plan.cells)
	}
	for key := range o.graphicsPlacements {
		if _, ok := plan.placements[key]; !ok {
			_, _ = o.scr.WriteString(deletePlacementSequence(key))
		}
	}
	for id := range o.graphicsImages {
		if _, ok := plan.images[id]; !ok {
			_, _ = o.scr.WriteString(deleteImageSequence(id))
		}
	}
	for _, command := range plan.uploads {
		_, _ = o.scr.WriteString(command)
	}
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
	if kitty {
		placements := make(map[graphicsPlacementKey]string, len(plan.placements))
		for key, placement := range plan.placements {
			sequence := placementSequence(placement)
			placements[key] = sequence
			if o.graphicsPlacements[key] != sequence {
				_, _ = o.scr.WriteString(sequence)
			}
		}
		o.graphicsPlacements = placements
		o.graphicsImages = plan.images
		if res.Cursor != nil {
			_ = o.scr.SetCursorPosition(res.Cursor.X, res.Cursor.Y)
		}
		if o.shown {
			_ = o.scr.ShowCursor()
		} else {
			_ = o.scr.HideCursor()
		}
	} else if protocol == graphicsSixel || protocol == graphicsIterm {
		sequences := plan.sixels
		if protocol == graphicsIterm {
			sequences = plan.iterm
		}
		for _, sequence := range sequences {
			_, _ = o.scr.WriteString(sequence)
		}
		if len(plan.sixels) > 0 && res.Cursor != nil {
			_ = o.scr.SetCursorPosition(res.Cursor.X, res.Cursor.Y)
		}
		if o.shown {
			_ = o.scr.ShowCursor()
		} else {
			_ = o.scr.HideCursor()
		}
		o.graphicsPlacements = nil
		o.graphicsImages = nil
	} else {
		o.graphicsPlacements = nil
		o.graphicsImages = nil
	}
	_ = o.scr.Flush()
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Draw paints the current frame onto scr (the terminal, or a buffer in
// tests) and returns where the cursor goes and what is clickable.
func (a *App) Draw(scr uv.Screen) compositor.Result {
	s, unlock := a.scene()
	return a.drawScene(scr, s, unlock)
}

func (a *App) drawScene(scr uv.Screen, s *compositor.Scene, unlock func()) compositor.Result {
	res := a.comp.Draw(scr, s)
	unlock()
	a.last = res
	a.dirty, a.urgent = false, false
	return res
}
