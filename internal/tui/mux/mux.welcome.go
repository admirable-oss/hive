package mux

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/tui/bee"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
)

// The welcome screen fills the pane area while there is nothing in it:
// connecting, cut off from the daemon, a first run with no environments, an
// environment with no tabs. The bee sits beside a speech bubble that types
// itself out, over a checklist of the next steps and their keys. It only
// animates while it is on screen; a screen too small for it gets the plain
// text instead.

type welcomeKind uint8

const (
	welcomeNone welcomeKind = iota
	welcomeConnecting
	welcomeUnreachable
	welcomeError
	welcomeFirstRun
	welcomeNoTabs
)

// welcome is the screen's animation: the bee, how much of the greeting is
// typed, and the spinner.
type welcome struct {
	kind    welcomeKind
	bee     bee.Model
	typed   int           // runes of the greeting shown
	spin    int           // spinner frame
	beeWait time.Duration // since the bee's last frame
	ticking bool
}

// welcomeTickMsg advances the welcome screen's animation.
type welcomeTickMsg struct{}

const (
	typeEvery  = 25 * time.Millisecond // a rune at a time, quick enough to read along
	spinEvery  = 80 * time.Millisecond
	bubbleGap  = 2  // columns between the bee and its bubble
	stepLabelW = 11 // the checklist's label column
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// welcomeKind says which welcome the pane area shows, if any.
func (a *App) welcomeKind() welcomeKind {
	snap := a.ws.snap
	switch {
	case snap == nil && errors.Is(a.ws.err, client.ErrUnavailable):
		return welcomeUnreachable
	case snap == nil && a.ws.err != nil:
		return welcomeError
	case snap == nil:
		return welcomeConnecting
	case a.ui.overview != nil:
		return welcomeNone
	case a.env() == nil:
		return welcomeFirstRun
	case a.tab() == nil:
		return welcomeNoTabs
	}
	return welcomeNone
}

// show switches the screen to kind, starting over when it changed.
func (w *welcome) show(kind welcomeKind) {
	if w.kind != kind {
		*w = welcome{kind: kind, bee: bee.New(), ticking: w.ticking}
		switch kind {
		case welcomeConnecting:
			w.bee.SetState(bee.StateActive)
		case welcomeUnreachable, welcomeError:
			w.bee.SetState(bee.StateDisconnected)
		}
	}
}

// animate schedules the next frame: one timer at a time.
func (w *welcome) animate(a *App) {
	if w.ticking {
		return
	}
	w.ticking = true
	a.after(w.delay(a), welcomeTickMsg{})
}

// delay is the time to the next frame: typing is the quickest, then the
// spinner, then the bee.
func (w *welcome) delay(a *App) time.Duration {
	switch {
	case w.typed < utf8.RuneCountInString(a.greeting(w.kind)):
		return typeEvery
	case w.kind == welcomeConnecting:
		return spinEvery
	}
	return w.bee.FrameDelay()
}

func (a *App) welcomeTick() {
	w := &a.ui.welcome
	w.ticking = false
	kind := a.welcomeKind()
	if kind == welcomeNone || kind != w.kind {
		return // the next frame that shows a welcome starts it again
	}
	d := w.delay(a)
	if n := utf8.RuneCountInString(a.greeting(kind)); w.typed < n {
		w.typed++
	}
	w.spin = (w.spin + 1) % len(spinner)
	if w.beeWait += d; w.beeWait >= w.bee.FrameDelay() {
		w.beeWait = 0
		w.bee.Tick()
	}
	w.animate(a)
}

// greeting is what the bee says.
func (a *App) greeting(kind welcomeKind) string {
	switch kind {
	case welcomeConnecting:
		return "Waking up the hive…"
	case welcomeUnreachable:
		return "I can't find the daemon. Trying again…"
	case welcomeError:
		return "The daemon is having a moment. Trying again…"
	case welcomeFirstRun:
		return "Welcome to Hive! Let's get your agents buzzing."
	case welcomeNoTabs:
		return "Nothing open in " + a.ws.envID + " yet. Let's fix that."
	}
	return ""
}

// addWelcome puts the welcome screen in the pane area, when one is due and
// fits, and keeps it moving while it shows.
func (a *App) addWelcome(s *compositor.Scene) {
	kind := a.welcomeKind()
	if kind == welcomeNone {
		return
	}
	a.ui.welcome.show(kind)
	r := s.Regions.Panes
	if s.Splash = a.welcomeLines(kind, r.Dx(), r.Dy()); s.Splash != nil {
		a.ui.welcome.animate(a)
	}
}

// welcomeLines draws the welcome screen for an area of w by h cells, or
// returns nil when it does not fit.
func (a *App) welcomeLines(kind welcomeKind, w, h int) []compositor.Spans {
	st := a.welcomeStyles()
	steps := a.welcomeSteps(kind, st)
	bubble := a.bubble(kind, st)

	var lines []compositor.Spans
	art := a.ui.welcome.bee.Lines()
	top := (len(art) - len(bubble)) / 2
	for i, l := range art {
		row := append(compositor.Spans{}, l...)
		if j := i - top; j >= 0 && j < len(bubble) {
			row = append(row, compositor.Span{Text: strings.Repeat(" ", bubbleGap)})
			row = append(row, bubble[j]...)
		}
		lines = append(lines, row)
	}
	lines = append(lines, compositor.Spans{})
	lines = append(lines, steps...)

	width := 0
	for _, l := range lines {
		width = max(width, l.Width())
	}
	if width > w || len(lines) > h {
		return nil
	}
	return lines
}

type welcomeStyles struct {
	text, muted, accent, key, chip uv.Style
	done, now, failed              uv.Style
}

func (a *App) welcomeStyles() welcomeStyles {
	th := a.theme
	chip := uv.Style{Fg: th.Bg, Bg: th.Accent, Attrs: uv.AttrBold}
	if th.Bg == nil {
		chip = uv.Style{Attrs: uv.AttrBold | uv.AttrReverse}
	}
	return welcomeStyles{
		text:   uv.Style{Fg: th.Fg},
		muted:  uv.Style{Fg: th.Muted},
		accent: uv.Style{Fg: th.Accent, Attrs: uv.AttrBold},
		key:    uv.Style{Fg: th.Accent},
		chip:   chip,
		done:   uv.Style{Fg: th.Success},
		now:    uv.Style{Fg: th.Accent, Attrs: uv.AttrBold},
		failed: uv.Style{Fg: th.Error, Attrs: uv.AttrBold},
	}
}

// bubble is the speech bubble: Hive's name and version over the greeting,
// typed so far. Its size is the whole greeting's from the start, so it does
// not grow as the words appear.
func (a *App) bubble(kind welcomeKind, st welcomeStyles) []compositor.Spans {
	greeting := a.greeting(kind)
	version := buildinfo.Get().Version
	inner := max(utf8.RuneCountInString(greeting), len("hive ")+utf8.RuneCountInString(version))
	typed := greeting
	if n := a.ui.welcome.typed; n < utf8.RuneCountInString(greeting) {
		typed = string([]rune(greeting)[:n])
	}
	pad := func(n int) compositor.Span { return compositor.Span{Text: strings.Repeat(" ", max(n, 0))} }
	edge := st.muted
	head := len("hive ") + utf8.RuneCountInString(version)
	cursor := compositor.Span{}
	if typed != greeting {
		cursor = compositor.Span{Text: "▍", Style: st.key}
	}
	return []compositor.Spans{
		{{Text: " ╭" + strings.Repeat("─", inner+2) + "╮", Style: edge}},
		{{Text: " │ ", Style: edge}, {Text: "hive", Style: st.accent}, {Text: " " + version, Style: st.muted}, pad(inner - head), {Text: " │", Style: edge}},
		{{Text: "─┤ ", Style: edge}, {Text: typed, Style: st.text}, cursor, pad(inner - utf8.RuneCountInString(typed) - compositor.Spans{cursor}.Width()), {Text: " │", Style: edge}},
		{{Text: " ╰" + strings.Repeat("─", inner+2) + "╯", Style: edge}},
	}
}

// welcomeSteps is the checklist under the bee: what is done, what to do
// now, and what comes after, each with its key.
func (a *App) welcomeSteps(kind welcomeKind, st welcomeStyles) []compositor.Spans {
	type state uint8
	const (
		todo state = iota
		current
		done
		failed
	)
	step := func(s state, label string, detail ...compositor.Span) compositor.Spans {
		mark, markStyle, labelStyle := "○", st.muted, st.muted
		switch s {
		case current:
			mark, markStyle, labelStyle = "◆", st.now, st.chip
		case done:
			mark, markStyle, labelStyle = "✔", st.done, st.text
		case failed:
			mark, markStyle, labelStyle = "✖", st.failed, st.failed
		}
		if s == current && kind == welcomeConnecting {
			mark = spinner[a.ui.welcome.spin]
		}
		l := compositor.Spans{{Text: "  " + mark + "  ", Style: markStyle}, {Text: " " + label + " ", Style: labelStyle}}
		l = append(l, compositor.Span{Text: strings.Repeat(" ", max(stepLabelW-len(label)-2, 1))})
		return append(l, detail...)
	}
	// more continues the step above on a line of its own, under its text.
	more := func(detail ...compositor.Span) compositor.Spans {
		return append(compositor.Spans{{Text: strings.Repeat(" ", 5+stepLabelW)}}, detail...)
	}
	text := func(s string) compositor.Span { return compositor.Span{Text: s, Style: st.text} }
	muted := func(s string) compositor.Span { return compositor.Span{Text: s, Style: st.muted} }
	key := func(act keymap.Action) compositor.Span { return compositor.Span{Text: a.keyHint(act), Style: st.key} }
	agent := step(todo, "agent", muted("run claude, codex or any agent in a tab"))
	keys := step(todo, "keys", key(keymap.Help), muted(" every key  ·  "), key(keymap.Detach), muted(" leaves; agents keep running"))
	switch kind {
	case welcomeConnecting:
		return []compositor.Spans{
			step(current, "daemon", text("Connecting to the hive daemon…")),
			step(todo, "project", muted("open a tab where your code is")),
			agent, keys,
		}
	case welcomeUnreachable, welcomeError:
		lines := []compositor.Spans{step(failed, "daemon", text("Cannot reach the hive daemon"), muted(" · retrying"))}
		if kind == welcomeError {
			lines = []compositor.Spans{
				step(failed, "daemon", text("The hive daemon returned an error"), muted(" · retrying")),
				more(muted(compositor.Truncate(errText(a.ws.err), 56))),
			}
		}
		return append(lines,
			step(todo, "start it", muted("run "), compositor.Span{Text: "hive daemon start", Style: st.key}, muted(" in a shell")),
			keys,
		)
	case welcomeFirstRun:
		return []compositor.Spans{
			step(done, "daemon", muted("connected")),
			step(current, "project", text("No environments yet")),
			more(key(keymap.NewTab), text(" opens a tab in "+tailOf(displayDir(a.opts.Cwd), 36))),
			agent, keys,
		}
	case welcomeNoTabs:
		return []compositor.Spans{
			step(done, "daemon", muted("connected")),
			step(done, "project", muted(a.ws.envID)),
			step(current, "tab", text("No tabs in "+a.ws.envID)),
			more(key(keymap.NewTab), text(" opens one")),
			agent, keys,
		}
	}
	return nil
}

// tailOf keeps the end of a path, where the project's name is, in n runes.
func tailOf(p string, n int) string {
	r := []rune(p)
	if len(r) <= n {
		return p
	}
	return "…" + string(r[len(r)-n+1:])
}
