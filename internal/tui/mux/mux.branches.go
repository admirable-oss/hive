package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/notify"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// Branches (hive branch) show in the sidebar beside their environments,
// and in the Overview with the branches they overlap. Their records
// arrive whole in branch.updated events, so following them needs no
// reads. The review overlay shows a branch's diff, and takes comments that
// go back to its agent.

// loadBranches reads the active branches. A daemon without them (older
// than M23) has none, which is not an error.
func loadBranches(ctx context.Context, c client.Client) []branch.Branch {
	list, err := client.NewBranches(c).List(ctx, "", false)
	if err != nil {
		return nil
	}
	return list
}

// applyBranchEvent patches the branches from a branch.* event, and tells
// the person when branches start to overlap.
func (a *App) applyBranchEvent(ev event.Event) {
	s := a.ws.snap
	switch ev.Type {
	case branch.EventUpdated:
		var b branch.Branch
		if json.Unmarshal(ev.Data, &b) != nil {
			return
		}
		i := slices.IndexFunc(s.branches, func(x branch.Branch) bool { return x.ID == b.ID })
		switch {
		case i >= 0 && s.branches[i].Rev > b.Rev:
			return // an older revision than the one shown
		case !b.State.Active() && i >= 0:
			s.branches = slices.Delete(s.branches, i, i+1)
		case !b.State.Active():
			return
		case i >= 0:
			s.branches[i] = b
		default:
			s.branches = append(s.branches, b)
		}
		a.dirty = true
	case branch.EventRemoved:
		var r struct {
			IDs []string `json:"ids"`
		}
		if json.Unmarshal(ev.Data, &r) == nil {
			s.branches = slices.DeleteFunc(s.branches, func(b branch.Branch) bool { return slices.Contains(r.IDs, b.ID) })
			a.dirty = true
		}
	case branch.EventOverlap:
		var o branch.OverlapEvent
		if json.Unmarshal(ev.Data, &o) != nil {
			return
		}
		what := "change " + plural(len(o.Files), "the same file", "the same files")
		kind := compositor.ToastWarning
		if len(o.Conflicts) > 0 {
			what = "would conflict in " + compositor.Truncate(strings.Join(o.Conflicts, ", "), 40)
			kind = compositor.ToastError
		}
		a.raise(notify.Note{Title: o.Names[0] + " · " + o.Names[1], Body: what}, kind,
			fmt.Sprintf("%s and %s %s (hive branch get %s)", o.Names[0], o.Names[1], what, o.IDs[0]))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// branchOf returns an environment's branch, if Hive follows one there.
func (s *snapshot) branchOf(envID string) *branch.Branch {
	if s == nil {
		return nil
	}
	for i := range s.branches {
		if s.branches[i].EnvironmentID == envID {
			return &s.branches[i]
		}
	}
	return nil
}

// branchMark is a branch's state as the sidebar shows it, with its colour:
// conflicts and overlaps first, since they need the person.
func (a *App) branchMark(b *branch.Branch) (string, color.Color) {
	switch {
	case b.InProgress:
		return "merge stopped", a.theme.Error
	case b.Conflicted():
		return "conflicts", a.theme.Error
	case b.Checking:
		return "checking", a.theme.Info
	case len(b.Overlaps) > 0:
		return "overlaps", a.theme.Warning
	}
	switch b.State {
	case branch.StateReady, branch.StateMerged:
		return string(b.State), a.theme.Success
	case branch.StateWorking:
		return "working", a.theme.Info
	}
	return string(b.State), a.theme.Muted
}

// branchSpans follow an environment in the sidebar: its branch's state.
func (a *App) branchSpans(envID string) []compositor.Span {
	b := a.ws.snap.branchOf(envID)
	if b == nil {
		return nil
	}
	mark, c := a.branchMark(b)
	spans := []compositor.Span{{Text: " " + mark, Style: uv.Style{Fg: c}}}
	if n := b.Pending(); n > 0 {
		spans = append(spans, compositor.Span{Text: fmt.Sprintf(" ✎%d", n), Style: uv.Style{Fg: a.theme.Muted}})
	}
	return spans
}

// branchLines are the Overview's branch rows.
func (a *App) branchLines(width int) []compositor.Spans {
	s := a.ws.snap
	if s == nil || len(s.branches) == 0 {
		return nil
	}
	muted := uv.Style{Fg: a.theme.Muted}
	lines := []compositor.Spans{{}, {{Text: "  BRANCHES", Style: muted}}}
	for i := range s.branches {
		b := &s.branches[i]
		mark, c := a.branchMark(b)
		changes := "-"
		if b.Files > 0 {
			changes = fmt.Sprintf("%d files +%d -%d", b.Files, b.Added, b.Deleted)
		}
		line := compositor.Spans{
			{Text: "● ", Style: uv.Style{Fg: c}},
			{Text: fmt.Sprintf("%-22s ", compositor.Truncate(b.Name, 22))},
			{Text: fmt.Sprintf("%-13s ", mark), Style: uv.Style{Fg: c}},
			{Text: changes, Style: muted},
		}
		if len(b.Overlaps) > 0 {
			var with []string
			for _, o := range b.Overlaps {
				with = append(with, o.Branch)
			}
			line = append(line, compositor.Span{Text: "  with " + compositor.Truncate(strings.Join(with, ", "), max(width-70, 8)), Style: uv.Style{Fg: c}})
		}
		lines = append(lines, line)
	}
	return lines
}

// --- review ---

// reviewTimeout bounds the gates the review overlay runs.
const reviewTimeout = 30 * time.Minute

// review is the overlay that shows a branch's diff and takes comments.
type review struct {
	ref    string // the branch's ID
	name   string
	diff   *branch.Diff
	lines  []compositor.Spans
	where  []diffPos // per line: the file and line a comment there is about
	files  []int     // the lines where files start
	cursor int
	scroll int
	// commenting is set while the comment line takes the keys.
	commenting bool
	in         input
	status     string
}

// diffPos is a place in the branch's version of a file.
type diffPos struct {
	path string
	line int
}

// openReview opens the review of the current environment's branch.
func (a *App) openReview() {
	b := a.ws.snap.branchOf(a.ws.envID)
	if b == nil {
		a.toast(compositor.ToastInfo, "no branch here: agents started with --isolate or --worktree get one")
		return
	}
	r := &review{ref: b.ID, name: b.Name, status: "reading the diff…"}
	a.ui.overlay = r
	r.reload(a)
}

// reload reads the branch's diff again.
func (r *review) reload(a *App) {
	c, ref := a.c, r.ref
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		d, err := client.NewBranches(c).Diff(ctx, branch.DiffRequest{Ref: ref})
		return func() {
			if a.ui.overlay != r {
				return
			}
			if err != nil {
				r.status = "diff: " + errText(err)
				return
			}
			r.setDiff(a, d)
		}
	})
}

// setDiff lays the diff out as lines, remembering where each one is.
func (r *review) setDiff(a *App, d branch.Diff) {
	r.diff = &d
	r.lines, r.where, r.files = nil, nil, nil
	add := func(s compositor.Spans, pos diffPos) {
		r.lines = append(r.lines, s)
		r.where = append(r.where, pos)
	}
	style := func(c color.Color) uv.Style { return uv.Style{Fg: c} }
	var pos diffPos
	for line := range strings.SplitSeq(strings.TrimSuffix(d.Patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			if len(r.lines) > 0 {
				add(compositor.Spans{}, diffPos{})
			}
			r.files = append(r.files, len(r.lines))
			pos = diffPos{}
			continue
		case strings.HasPrefix(line, "+++ "):
			pos.path = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			add(compositor.Spans{{Text: pos.path, Style: uv.Style{Fg: a.theme.Accent, Attrs: uv.AttrBold}}}, diffPos{path: pos.path})
			continue
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file"), strings.HasPrefix(line, "deleted file"),
			strings.HasPrefix(line, "similarity"), strings.HasPrefix(line, "old mode"), strings.HasPrefix(line, "new mode"):
			continue
		case strings.HasPrefix(line, "rename to "):
			pos.path = strings.TrimPrefix(line, "rename to ")
			add(compositor.Spans{{Text: line, Style: style(a.theme.Muted)}}, diffPos{path: pos.path})
			continue
		case strings.HasPrefix(line, "@@"):
			pos.line = hunkStart(line)
			add(compositor.Spans{{Text: line, Style: style(a.theme.Info)}}, diffPos{path: pos.path, line: pos.line})
			continue
		case strings.HasPrefix(line, "Binary files"):
			add(compositor.Spans{{Text: line, Style: style(a.theme.Muted)}}, diffPos{path: pos.path})
			continue
		}
		here := diffPos{path: pos.path, line: max(pos.line, 1)}
		switch {
		case strings.HasPrefix(line, "+"):
			add(compositor.Spans{{Text: line, Style: style(a.theme.Success)}}, here)
			pos.line++
		case strings.HasPrefix(line, "-"):
			add(compositor.Spans{{Text: line, Style: style(a.theme.Error)}}, here)
		default:
			add(compositor.Spans{{Text: line}}, here)
			pos.line++
		}
	}
	if len(r.lines) == 0 {
		add(compositor.Spans{{Text: "no changes against " + d.Base, Style: style(a.theme.Muted)}}, diffPos{})
	}
	r.cursor = min(r.cursor, len(r.lines)-1)
	r.status = fmt.Sprintf("%d files against %s", len(d.Files), d.Base)
	if d.Dirty {
		r.status += ", uncommitted changes included"
	}
	if d.Truncated {
		r.status += " (cut: hive branch diff shows it all)"
	}
}

// hunkStart reads the new side's first line from "@@ -a,b +c,d @@".
func hunkStart(line string) int {
	_, rest, ok := strings.Cut(line, " +")
	if !ok {
		return 0
	}
	n, _, _ := strings.Cut(rest, ",")
	n, _, _ = strings.Cut(n, " ")
	v, _ := strconv.Atoi(n)
	return v
}

func (r *review) key(a *App, k uv.Key) {
	if r.commenting {
		r.commentKey(a, k)
		return
	}
	page := max(a.height-6, 1)
	name := keyName(k)
	if len(k.Text) == 1 && k.Text != " " {
		name = k.Text // G, not shift+g
	}
	switch name {
	case "esc", "q", "ctrl+c":
		a.closeOverlay()
		return
	case "up", "k":
		r.cursor--
	case "down", "j":
		r.cursor++
	case "pgup", "ctrl+u":
		r.cursor -= page
	case "pgdown", "ctrl+d", "space":
		r.cursor += page
	case "g", "home":
		r.cursor = 0
	case "G", "end":
		r.cursor = len(r.lines) - 1
	case "n", "]":
		if i := slices.IndexFunc(r.files, func(l int) bool { return l > r.cursor }); i >= 0 {
			r.cursor = r.files[i]
		}
	case "p", "[":
		for i := len(r.files) - 1; i >= 0; i-- {
			if r.files[i] < r.cursor {
				r.cursor = r.files[i]
				break
			}
		}
	case "c":
		r.commenting, r.in = true, newInput("")
	case "C":
		r.commenting, r.in = true, newInput("")
		r.cursor = -1 // about the whole change
	case "s":
		r.send(a)
	case "r":
		r.ready(a)
	case "R":
		r.status = "reading the diff…"
		r.reload(a)
	}
	r.clamp(page)
}

// clamp keeps the cursor on a line and in view.
func (r *review) clamp(page int) {
	if r.cursor == -1 && r.commenting {
		return
	}
	r.cursor = min(max(r.cursor, 0), max(len(r.lines)-1, 0))
	if r.cursor < r.scroll {
		r.scroll = r.cursor
	}
	if r.cursor >= r.scroll+page {
		r.scroll = r.cursor - page + 1
	}
}

func (r *review) commentKey(a *App, k uv.Key) {
	switch keyName(k) {
	case "esc", "ctrl+c":
		r.commenting = false
		r.clamp(max(a.height-6, 1))
	case "enter":
		r.commenting = false
		body := strings.TrimSpace(r.in.String())
		if body != "" {
			r.comment(a, body)
		}
		r.clamp(max(a.height-6, 1))
	default:
		r.in.edit(k)
	}
}

// comment records a comment on the line under the cursor.
func (r *review) comment(a *App, body string) {
	req := branch.CommentRequest{Ref: r.ref, Body: body, By: "tui"}
	if r.cursor >= 0 && r.cursor < len(r.where) {
		req.Path, req.Line = r.where[r.cursor].path, r.where[r.cursor].line
	}
	c := a.c
	a.call("comment", func(ctx context.Context) error {
		b, err := client.NewBranches(c).Comment(ctx, req)
		if err == nil {
			a.post(func() {
				r.status = fmt.Sprintf("%d comments, %d not sent (s sends them)", len(b.Comments), b.Pending())
			})
		}
		return err
	}, nil)
}

// send sends the comments not sent yet to the branch's agent.
func (r *review) send(a *App) {
	c, ref := a.c, r.ref
	a.call("send comments", func(ctx context.Context) error {
		_, err := client.NewBranches(c).Send(ctx, ref)
		if err == nil {
			a.post(func() { r.status = "comments sent to the agent" })
		}
		return err
	}, nil)
}

// ready runs the branch's gates.
func (r *review) ready(a *App) {
	c, ref, name := a.c, r.ref, r.name
	r.status = "running the gates…"
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, reviewTimeout)
		defer cancel()
		b, err := client.NewBranches(c).Ready(ctx, ref, nil)
		return func() {
			switch {
			case err != nil:
				r.status = "gates: " + errText(err)
			case b.State == branch.StateReady:
				r.status = "ready: every gate passed"
				a.toast(compositor.ToastSuccess, name+" is ready")
			default:
				var failed []string
				for _, g := range b.Gates {
					if !g.Passed && !g.Skipped {
						failed = append(failed, g.Name)
					}
				}
				r.status = "not ready: " + strings.Join(failed, ", ") + " failed (hive branch get " + ref + ")"
			}
		}
	})
}

func (r *review) paste(_ *App, text string) {
	if r.commenting {
		r.in.insert(text)
	}
}

func (r *review) click(a *App, line int) {
	if line >= 0 && line < len(r.lines) {
		r.cursor = line
		r.clamp(max(a.height-6, 1))
	}
}

func (r *review) view(a *App) compositor.Overlay {
	ov := compositor.Overlay{
		Title: "Review · " + r.name, Width: a.width, Height: a.height,
		Lines: r.lines, Selected: r.cursor, Scroll: r.scroll,
		Footer: []compositor.Span{{Text: r.status + "   ↑↓ move  n/p file  c comment  C on the whole  s send  r ready  R reload  esc close"}},
	}
	if r.commenting {
		where := "the whole change"
		if r.cursor >= 0 && r.cursor < len(r.where) && r.where[r.cursor].path != "" {
			where = r.where[r.cursor].path
			if l := r.where[r.cursor].line; l > 0 {
				where += ":" + strconv.Itoa(l)
			}
		}
		ov.Input = r.in.view("comment on " + where + " › ")
		ov.Footer = []compositor.Span{{Text: "⏎ add the comment  esc cancel"}}
	}
	return ov
}
