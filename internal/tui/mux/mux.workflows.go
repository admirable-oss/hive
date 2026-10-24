package mux

import (
	"cmp"
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
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/workflow"
)

// Workflow runs (hive workflow) show in the Overview under the plans: the
// runs going on, and the latest that finished. Their summaries arrive
// whole in workflow.run events, so following them needs no reads.

// overviewRunsRead is how many runs the TUI reads when it starts.
const overviewRunsRead = 20

// loadWorkflowRuns reads the latest runs' summaries. A daemon without
// workflows (older than M24) has none, which is not an error.
func loadWorkflowRuns(ctx context.Context, c client.Client) []workflow.RunSummary {
	runs, err := client.NewWorkflows(c).Runs(ctx, "", overviewRunsRead)
	if err != nil {
		return nil
	}
	return runs
}

// applyWorkflowEvent patches the runs from a workflow.* event, and
// notifies when a run fails.
func (a *App) applyWorkflowEvent(ev event.Event) {
	s := a.ws.snap
	switch ev.Type {
	case workflow.EventRun:
		var r workflow.RunSummary
		if json.Unmarshal(ev.Data, &r) != nil {
			return
		}
		i := slices.IndexFunc(s.runs, func(x workflow.RunSummary) bool { return x.ID == r.ID })
		was := workflow.State("")
		switch {
		case i < 0:
			s.runs = slices.Insert(s.runs, 0, r) // newest first
		case s.runs[i].Rev < r.Rev:
			was = s.runs[i].State
			s.runs[i] = r
		default:
			return // an older revision than the one shown
		}
		a.dirty = true
		if r.State == workflow.StateFailed && was != workflow.StateFailed && a.opts.Notify.Wants("workflow", "blocked") {
			a.raise(notify.Note{Title: "workflow " + r.Workflow, Body: "run " + r.ID + " failed"}, compositor.ToastError,
				fmt.Sprintf("workflow %s failed: %s (hive workflow report %s)", r.Workflow, cmp.Or(r.Reason, "a step failed"), r.ID))
		}
	case workflow.EventRemoved:
		var r struct {
			IDs []string `json:"ids"`
		}
		if json.Unmarshal(ev.Data, &r) == nil {
			s.runs = slices.DeleteFunc(s.runs, func(x workflow.RunSummary) bool { return slices.Contains(r.IDs, x.ID) })
			a.dirty = true
		}
	}
}

// overviewRuns is how many finished runs the Overview shows besides the
// ones going on.
const overviewRuns = 3

// workflowLines are the Overview's workflow rows: runs going on, then the
// latest finished ones.
func (a *App) workflowLines(width int) []compositor.Spans {
	s := a.ws.snap
	if s == nil || len(s.runs) == 0 {
		return nil
	}
	muted := uv.Style{Fg: a.theme.Muted}
	lines := []compositor.Spans{{}, {{Text: "  WORKFLOWS", Style: muted}}}
	finished := 0
	for _, r := range s.runs {
		if r.State.Finished() {
			if finished == overviewRuns {
				continue
			}
			finished++
		}
		glyph, c := a.runGlyph(r)
		done := r.Counts[workflow.StepSucceeded] + r.Counts[workflow.StepFailed] + r.Counts[workflow.StepSkipped] + r.Counts[workflow.StepCancelled]
		text := fmt.Sprintf("%d/%d %s", done, r.Total, r.State)
		if n := r.Counts[workflow.StepFailed]; n > 0 {
			text += fmt.Sprintf(" · %d failed", n)
		}
		line := compositor.Spans{
			{Text: glyph + " ", Style: uv.Style{Fg: c}},
			{Text: fmt.Sprintf("%-22s ", compositor.Truncate(r.ID+" "+r.Workflow, 22))},
			{Text: text, Style: uv.Style{Fg: c}},
			{Text: "  " + r.Trigger, Style: muted},
		}
		if len(r.Current) > 0 {
			line = append(line, compositor.Span{Text: "  now: " + compositor.Truncate(strings.Join(r.Current, ", "), max(width-60, 8)), Style: uv.Style{Fg: a.theme.Info}})
		}
		lines = append(lines, line)
	}
	return lines
}

// runGlyph is a run's mark and colour by state.
func (a *App) runGlyph(r workflow.RunSummary) (string, color.Color) {
	switch r.State {
	case workflow.StateQueued:
		return "○", a.theme.Muted
	case workflow.StateRunning:
		return "●", a.theme.Info
	case workflow.StateSucceeded:
		return "✓", a.theme.Success
	case workflow.StateCancelled:
		return "⊘", a.theme.Muted
	}
	return "✗", a.theme.Error
}

// runningRuns counts the runs going on.
func runningRuns(s *snapshot) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, r := range s.runs {
		if !r.State.Finished() {
			n++
		}
	}
	return n
}
