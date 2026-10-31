package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/notify"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// Plans (hive task) show in the Overview under the agents: their progress
// and the tasks waiting for a person. Their summaries arrive whole in
// task.plan events, so following them needs no reads.

// loadPlans reads the plans' summaries. A daemon without tasks (older than
// M22) has none, which is not an error.
func loadPlans(ctx context.Context, c client.Client) []task.Summary {
	plans, err := client.NewTasks(c).List(ctx)
	if err != nil {
		return nil
	}
	return plans
}

// applyTaskEvent patches the plans from a task.* event, and notifies when
// a task waits for approval.
func (a *App) applyTaskEvent(ev event.Event) {
	s := a.ws.snap
	switch ev.Type {
	case task.EventPlan:
		var p task.Summary
		if json.Unmarshal(ev.Data, &p) != nil {
			return
		}
		i := slices.IndexFunc(s.plans, func(x task.Summary) bool { return x.ID == p.ID })
		switch {
		case i < 0:
			s.plans = slices.Insert(s.plans, 0, p) // newest first
		case s.plans[i].Rev < p.Rev:
			s.plans[i] = p
		default:
			return // an older revision than the one shown
		}
		a.dirty = true
	case task.EventRemoved:
		var r struct {
			IDs []string `json:"ids"`
		}
		if json.Unmarshal(ev.Data, &r) == nil {
			s.plans = slices.DeleteFunc(s.plans, func(p task.Summary) bool { return slices.Contains(r.IDs, p.ID) })
			a.dirty = true
		}
	case task.EventApproval:
		var v task.TaskView
		if json.Unmarshal(ev.Data, &v) == nil && !s.self.Notify {
			a.notifyApproval(v) // a daemon that does not say who is told: decide here
		}
	}
}

// notifyApproval tells the user a task waits for their approval, as
// configured.
func (a *App) notifyApproval(v task.TaskView) {
	if a.opts.Notify.Wants(v.Kind, "blocked") {
		a.raise(notify.Note{Title: "plan " + v.PlanID + " · " + v.ID, Body: "waits for your approval"}, compositor.ToastWarning,
			fmt.Sprintf("task %s of plan %s waits for approval (hive task approve %s %s)", v.ID, v.PlanID, v.PlanID, v.ID))
	}
}

// overviewPlans is how many finished plans the Overview shows besides the
// running ones.
const overviewPlans = 3

// planLines are the Overview's plan rows: running plans, then the latest
// finished ones.
func (a *App) planLines(width int) []compositor.Spans {
	s := a.ws.snap
	if s == nil || len(s.plans) == 0 {
		return nil
	}
	muted := uv.Style{Fg: a.theme.Muted}
	lines := []compositor.Spans{{}, {{Text: "  PLANS", Style: muted}}}
	finished := 0
	for _, p := range s.plans {
		if p.State.Finished() {
			if finished == overviewPlans {
				continue
			}
			finished++
		}
		glyph, c := a.planGlyph(p)
		name := p.ID
		if p.Name != "" {
			name += " " + p.Name
		}
		done := p.Counts[task.StateSucceeded]
		text := fmt.Sprintf("%d/%d", done, p.Total)
		for _, st := range []task.State{task.StateRunning, task.StateBlocked, task.StateQueued, task.StateFailed} {
			if n := p.Counts[st]; n > 0 {
				text += fmt.Sprintf(" · %d %s", n, st)
			}
		}
		var waiting []string
		for _, t := range p.Attention {
			waiting = append(waiting, t.ID)
		}
		line := compositor.Spans{
			{Text: glyph + " ", Style: uv.Style{Fg: c}},
			{Text: fmt.Sprintf("%-22s ", compositor.Truncate(name, 22))},
			{Text: text, Style: uv.Style{Fg: c}},
		}
		if len(waiting) > 0 {
			line = append(line, compositor.Span{Text: "  waits: " + compositor.Truncate(strings.Join(waiting, ", "), max(width-50, 8)), Style: uv.Style{Fg: a.theme.Warning}})
		}
		lines = append(lines, line)
	}
	return lines
}

// planGlyph is a plan's mark and colour by state.
func (a *App) planGlyph(p task.Summary) (string, color.Color) {
	switch {
	case len(p.Attention) > 0:
		return "◆", a.theme.Warning
	case p.State == task.PlanRunning:
		return "●", a.theme.Info
	case p.State == task.PlanSucceeded:
		return "✓", a.theme.Success
	}
	return "✗", a.theme.Error
}

// runningPlans counts the plans that run.
func runningPlans(s *snapshot) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, p := range s.plans {
		if !p.State.Finished() {
			n++
		}
	}
	return n
}
