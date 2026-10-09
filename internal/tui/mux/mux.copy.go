package mux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/copymode"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/vt"
)

// historyLines is how much scrollback copy mode and edit-scrollback read.
const historyLines = 10_000

// copyState is copy mode on one pane: a frozen copy of its history and
// screen. A mouse selection uses it too, without history.
type copyState struct {
	paneID string
	c      *copymode.Copy // nil while the history loads
	mouse  bool           // made by a mouse drag; ends when the drag does
	scroll int            // wheel scroll to apply once loaded
}

// copyLoadedMsg carries a pane's history for copy mode.
type copyLoadedMsg struct {
	paneID  string
	history []string
	screen  *vt.Screen
	err     error
}

// enterCopy starts copy mode on a pane. With history it reads the
// scrollback first; scroll moves the view once ready (the wheel).
func (a *App) enterCopy(paneID string, history bool, scroll int) {
	p := a.ws.snap.pane(paneID)
	if p == nil {
		return
	}
	v := a.views[p.ProcessID]
	var scr *vt.Screen
	if v != nil {
		scr = v.clone()
	} else if c := a.cache[p.ProcessID]; c != nil {
		scr = c.screen.Clone()
	}
	if scr == nil {
		a.toast(compositor.ToastInfo, "nothing to copy yet")
		return
	}
	a.ui.mode = keymap.ModeCopy
	a.ui.copy = &copyState{paneID: paneID, scroll: scroll}
	if !history {
		a.copyLoaded(copyLoadedMsg{paneID: paneID, screen: scr})
		return
	}
	api := a.api
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		lines, err := api.PaneRead(ctx, paneID, client.ReadRequest{Source: "history", Lines: historyLines})
		return copyLoadedMsg{paneID: paneID, history: lines, screen: scr, err: err}
	})
}

func (a *App) copyLoaded(m copyLoadedMsg) {
	c := a.ui.copy
	if c == nil || c.paneID != m.paneID || c.c != nil {
		return // copy mode ended, or moved on, meanwhile
	}
	if m.err != nil {
		a.toast(compositor.ToastWarning, "scrollback unavailable: "+errText(m.err))
	}
	lines := append(copymode.FromText(m.history), m.screen.Lines...)
	w, h := m.screen.Cols, m.screen.Rows
	if r, ok := a.geometry()[m.paneID]; ok {
		w, h = r.W, r.H
	}
	// Screen rows are relative to the last h lines; a pane shorter than
	// its screen shows the screen's top, so the cursor row shifts.
	cur := m.screen.Cursor
	cur.Y -= max(m.screen.Rows-h, 0)
	c.c = copymode.New(lines, w, h, cur)
	if c.scroll != 0 {
		c.c.Scroll(c.scroll)
	}
}

// exitCopy leaves copy mode.
func (a *App) exitCopy() {
	if a.ui.copy == nil {
		return
	}
	a.ui.copy = nil
	if a.ui.mode == keymap.ModeCopy {
		a.ui.mode = keymap.ModeTerminal
	}
}

// copyKey routes a key in copy mode.
func (a *App) copyKey(k uv.Key) {
	act, ok := a.km.Lookup(keymap.ModeCopy, k)
	if !ok {
		if _, prefix := a.km.IsPrefix(k); prefix {
			a.ui.mode, a.ui.prefixKey = keymap.ModePrefix, k
		}
		return
	}
	c := a.ui.copy
	if c == nil || c.c == nil {
		if act == keymap.CopyExit {
			a.exitCopy()
		}
		return // still loading
	}
	switch act {
	case keymap.CopySearchForward, keymap.CopySearchBack:
		forward := act == keymap.CopySearchForward
		label := "/"
		if !forward {
			label = "?"
		}
		a.ui.overlay = &prompt{title: "Search", label: label + " ", in: newInput(c.c.Query()), submit: func(q string) {
			if a.ui.copy == nil || a.ui.copy.c == nil || q == "" {
				return
			}
			if !a.ui.copy.c.Search(q, forward) {
				a.toast(compositor.ToastInfo, "not found: "+q)
			}
		}}
		return
	}
	done, yanked := c.c.Do(act)
	if yanked != "" {
		a.copyText(yanked)
	}
	if done {
		a.exitCopy()
	}
}

// copyText puts text on the clipboard and says so.
func (a *App) copyText(text string) {
	a.ui.clip = text
	a.clipboard(text)
	n := strings.Count(text, "\n") + 1
	what := fmt.Sprintf("%d characters", len([]rune(text)))
	if n > 1 {
		what = fmt.Sprintf("%d lines", n)
	}
	a.toast(compositor.ToastSuccess, "copied "+what)
}

// --- edit-scrollback ---

// editScrollback opens the focused pane's history and screen in $EDITOR,
// in a popup over the tab; the file is removed when the popup closes.
func (a *App) editScrollback() {
	p := a.focusedPane()
	if p == nil {
		return
	}
	paneID, tabID, api := p.ID, p.TabID, a.api
	editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	a.call("edit scrollback", func(ctx context.Context) error {
		hist, err := api.PaneRead(ctx, paneID, client.ReadRequest{Source: "history", Lines: historyLines})
		if err != nil {
			return err
		}
		screen, err := api.PaneRead(ctx, paneID, client.ReadRequest{Source: "visible"})
		if err != nil {
			return err
		}
		dir, err := os.MkdirTemp("", "hive-scrollback-")
		if err != nil {
			return err
		}
		path := filepath.Join(dir, paneID+".txt")
		text := strings.Join(append(hist, screen...), "\n") + "\n"
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
		// The shell removes the file after the editor exits, wherever the
		// popup's life ends.
		script := editor + ` "$1"; rm -rf "$2"`
		_, err = api.PanePopup(ctx, pane.PopupRequest{
			TabID: tabID, WidthPct: 90, HeightPct: 90,
			Spec: pane.Spec{Name: "scrollback", Command: []string{"/bin/sh", "-c", script, "hive-edit", path, dir}},
		})
		if err != nil {
			_ = os.RemoveAll(dir)
		}
		return err
	}, nil)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
