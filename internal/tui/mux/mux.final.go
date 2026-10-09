package mux

import (
	"context"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/vt"
)

// finalLogLines is how much of an exited agent's output is replayed to
// rebuild its last screen.
const finalLogLines = 1000

// finalScreenMsg carries an exited agent's last screen, rebuilt from its
// log.
type finalScreenMsg struct {
	procID string
	screen *vt.Screen
}

// loadFinal rebuilds the last screen of a terminal agent that ended before
// this client saw it (a command that fails at once, a pane opened after
// its agent exited). The daemon forgets a terminal when its process ends,
// but keeps the output: replaying the log's tail through an emulator of
// the pane's size gives the screen as it was, colours included. The
// result joins the screen cache, which drawing already reads.
func (a *App) loadFinal(p *process.Process, sz size) {
	if !p.Terminal || p.Active() || a.cache[p.ID] != nil || a.views[p.ID] != nil || a.ws.finals[p.ID] {
		return
	}
	a.ws.finals[p.ID] = true // once per agent, whatever the outcome
	id, c := p.ID, a.c
	w, h := max(sz.w, 20), max(sz.h, 5)
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		res, err := c.ProcessLogs(ctx, process.LogsRequest{ID: id, Tail: finalLogLines})
		if err != nil || res.Logs == "" {
			return nil
		}
		term := vt.New(w, h, vt.Options{})
		defer term.Close()
		_, _ = term.Write([]byte(res.Logs))
		scr := term.Snapshot()
		if scr.Text() == "" {
			return nil
		}
		return finalScreenMsg{procID: id, screen: scr}
	})
}

func (a *App) finalLoaded(m finalScreenMsg) {
	if a.cache[m.procID] == nil && a.views[m.procID] == nil {
		a.cache[m.procID] = &screenCache{screen: m.screen}
	}
}

// loadFinals rebuilds the screens of exited agents in the panes on screen.
func (a *App) loadFinals() {
	s := a.ws.snap
	if s == nil || a.ui.overview != nil {
		return
	}
	for id, r := range a.geometry() {
		if p := s.pane(id); p != nil && p.Process != nil {
			a.loadFinal(p.Process, size{r.W, r.H})
		}
	}
}
