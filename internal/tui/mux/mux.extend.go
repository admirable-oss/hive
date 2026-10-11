package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// Command is one of the user's commands ([[keys.command]]); the nth is
// bound as keymap.CommandAction(n).
type Command struct {
	// Type is popup, pane, shell or plugin_action.
	Type    string
	Command string // a shell command line (popup, pane, shell)
	Plugin  string // plugin_action
	Action  string
	Title   string
}

// StatusSegment is one item on the right of the tab bar.
type StatusSegment struct {
	Type     string // text, datetime, hostname or command
	Text     string
	Format   string // datetime, strftime style
	Command  string
	Interval time.Duration
}

// --- the user's commands ---

// runCommand runs the user's nth command where the user is.
func (a *App) runCommand(n int) {
	if n < 0 || n >= len(a.opts.Commands) {
		return
	}
	c := a.opts.Commands[n]
	title := cmpOr(c.Title, cmpOr(c.Command, c.Plugin+" "+c.Action))
	p := a.focusedPane()
	t := a.tab()
	envID := a.ws.envID
	vars := map[string]string{}
	if p != nil {
		vars["HIVE_PANE_ID"] = p.ID
	}
	if t != nil {
		vars["HIVE_TAB_ID"] = t.ID
	}
	api, cl := a.api, a.c
	switch c.Type {
	case "popup":
		if t == nil {
			return
		}
		a.call(title, func(ctx context.Context) error {
			_, err := api.PanePopup(ctx, pane.PopupRequest{TabID: t.ID, Spec: pane.Spec{Name: c.Title, Command: []string{"sh", "-c", c.Command}, Env: vars}})
			return err
		}, nil)
	case "pane":
		if p == nil {
			return
		}
		a.call(title, func(ctx context.Context) error {
			_, err := api.PaneSplit(ctx, pane.SplitRequest{Pane: p.ID, Spec: pane.Spec{Name: c.Title, Command: []string{"sh", "-c", c.Command}, Env: vars}})
			return err
		}, nil)
	case "shell":
		if envID == "" {
			return
		}
		a.spawn(func(ctx context.Context) Msg {
			out, err := runShell(ctx, cl, envID, c.Command, vars)
			return commandDoneMsg{title: title, out: out, err: err}
		})
	case "plugin_action":
		req := client.PluginRun{Plugin: c.Plugin, Action: c.Action, Environment: envID}
		if p != nil {
			req.Pane = p.ID
		}
		if t != nil {
			req.Tab = t.ID
		}
		a.spawn(func(ctx context.Context) Msg {
			res, err := client.NewPlugins(cl).Run(ctx, req)
			if err == nil && res.ExitCode != 0 {
				err = fmt.Errorf("exited with status %d: %s", res.ExitCode, lastLine(res.Output))
			}
			return commandDoneMsg{title: title, out: res.Output, err: err}
		})
	}
}

// commandDoneMsg reports a background command's end.
type commandDoneMsg struct {
	title, out string
	err        error
}

func (a *App) commandDone(m commandDoneMsg) {
	if m.err != nil {
		a.toast(compositor.ToastError, m.title+": "+errText(m.err))
		return
	}
	text := m.title
	if l := lastLine(m.out); l != "" {
		text += ": " + l
	}
	a.toast(compositor.ToastInfo, text)
}

// lastLine is the last non-blank line of out.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n "), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// shellCommandTimeout bounds a shell command run from a key.
const shellCommandTimeout = 5 * time.Minute

// runShell runs a command line in an environment, on the daemon's
// machine (the one shown), and returns its output.
func runShell(ctx context.Context, c client.Client, envID, command string, vars map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, shellCommandTimeout)
	defer cancel()
	p, err := c.ProcessStart(ctx, process.StartRequest{EnvironmentID: envID, Command: "sh", Args: []string{"-c", command}, Env: vars})
	if err != nil {
		return "", err
	}
	for p.Active() {
		select {
		case <-ctx.Done():
			_ = c.ProcessStop(context.WithoutCancel(ctx), p.ID)
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if p, err = c.ProcessGet(ctx, p.ID); err != nil {
			return "", err
		}
	}
	out, _ := c.ProcessLogs(ctx, process.LogsRequest{ID: p.ID, Tail: 20})
	errs, _ := c.ProcessLogs(ctx, process.LogsRequest{ID: p.ID, Stream: process.StreamStderr, Tail: 5})
	if p.ExitCode != nil && *p.ExitCode != 0 {
		return out.Logs, fmt.Errorf("exited with status %d: %s", *p.ExitCode, lastLine(errs.Logs+"\n"+out.Logs))
	}
	return out.Logs, nil
}

// --- metadata ---

// loadMetadata reads every target's tokens and title; a daemon older than
// M8 has none.
func loadMetadata(ctx context.Context, c client.Client) map[metadata.Target]metadata.Entry {
	list, err := client.NewPlugins(c).Metadata(ctx)
	if err != nil {
		return nil
	}
	out := make(map[metadata.Target]metadata.Entry, len(list))
	for _, e := range list {
		out[e.Target] = e
	}
	return out
}

// applyMetadata records a metadata.changed event.
func (a *App) applyMetadata(ev event.Event) {
	var e metadata.Entry
	if json.Unmarshal(ev.Data, &e) != nil || a.ws.snap == nil {
		return
	}
	s := a.ws.snap
	if s.meta == nil {
		s.meta = map[metadata.Target]metadata.Entry{}
	}
	if e.Empty() {
		delete(s.meta, e.Target)
	} else {
		s.meta[e.Target] = e
	}
	a.dirty = true
}

// metaSpans are a target's tokens, for a sidebar row.
func (a *App) metaSpans(t metadata.Target) []compositor.Span {
	s := a.ws.snap
	if s == nil {
		return nil
	}
	var out []compositor.Span
	for _, tok := range s.meta[t].Tokens {
		out = append(out, compositor.Span{Text: "  " + tok.Text, Style: uv.Style{Fg: a.theme.Info}})
	}
	return out
}

// metaTitle is a target's reported title, if any.
func (a *App) metaTitle(t metadata.Target) string {
	if s := a.ws.snap; s != nil {
		return s.meta[t].Title
	}
	return ""
}

// --- status ---

// statusMsg carries a command segment's latest output.
type statusMsg struct {
	index int
	text  string
}

// statusTickMsg redraws time-based segments.
type statusTickMsg struct{}

// startStatus runs the status segments' commands and clock.
func (a *App) startStatus() {
	if len(a.opts.StatusRight) == 0 {
		return
	}
	a.status = make([]string, len(a.opts.StatusRight))
	clock := false
	for i, seg := range a.opts.StatusRight {
		switch seg.Type {
		case "command":
			a.bg.Go(func() { a.runStatusCommand(i, seg) })
		case "datetime":
			clock = true
		}
	}
	if clock {
		a.after(time.Second, statusTickMsg{})
	}
}

func (a *App) runStatusCommand(i int, seg StatusSegment) {
	interval := seg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	for {
		ctx, cancel := context.WithTimeout(a.ctx, min(interval, 10*time.Second))
		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, "sh", "-c", seg.Command)
		cmd.Stdout = &out
		err := cmd.Run()
		cancel()
		if a.ctx.Err() != nil {
			return
		}
		text := strings.TrimSpace(strings.SplitN(out.String(), "\n", 2)[0])
		if err != nil && text == "" {
			text = "!"
		}
		a.post(statusMsg{index: i, text: text})
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// statusSpans are the status segments, for the tab bar's right end.
func (a *App) statusSpans() []compositor.Span {
	var out []compositor.Span
	muted := uv.Style{Fg: a.theme.Muted}
	for i, seg := range a.opts.StatusRight {
		var text string
		switch seg.Type {
		case "text":
			text = seg.Text
		case "hostname":
			text = a.mach.label(a.mach.name)
		case "datetime":
			text = strftime(a.now(), cmpOr(seg.Format, "%H:%M"))
		case "command":
			if i < len(a.status) {
				text = a.status[i]
			}
		}
		if text == "" {
			continue
		}
		out = append(out, compositor.Span{Text: text + "  ", Style: muted})
	}
	return out
}

// strftime formats t with the common strftime directives.
func strftime(t time.Time, f string) string {
	var b strings.Builder
	for i := 0; i < len(f); i++ {
		if f[i] != '%' || i+1 == len(f) {
			b.WriteByte(f[i])
			continue
		}
		i++
		switch f[i] {
		case 'H':
			b.WriteString(t.Format("15"))
		case 'I':
			b.WriteString(t.Format("03"))
		case 'M':
			b.WriteString(t.Format("04"))
		case 'S':
			b.WriteString(t.Format("05"))
		case 'p':
			b.WriteString(t.Format("PM"))
		case 'd':
			b.WriteString(t.Format("02"))
		case 'e':
			b.WriteString(t.Format("_2"))
		case 'm':
			b.WriteString(t.Format("01"))
		case 'y':
			b.WriteString(t.Format("06"))
		case 'Y':
			b.WriteString(t.Format("2006"))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'A':
			b.WriteString(t.Format("Monday"))
		case 'b':
			b.WriteString(t.Format("Jan"))
		case 'B':
			b.WriteString(t.Format("January"))
		case 'Z':
			b.WriteString(t.Format("MST"))
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(f[i])
		}
	}
	return b.String()
}
