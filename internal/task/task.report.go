package task

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Report is what a plan did, task by task: what `hive task report` prints.
type Report struct {
	ID       string    `json:"id"`
	Name     string    `json:"name,omitempty"`
	State    PlanState `json:"state"`
	Reason   string    `json:"reason,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitzero"`
	Duration int64     `json:"duration_ms"`
	// Counts tasks by state.
	Counts map[State]int `json:"counts"`
	Tasks  []TaskReport  `json:"tasks"`
}

// TaskReport is one task's part of a report.
type TaskReport struct {
	ID       string   `json:"id"`
	Title    string   `json:"title,omitempty"`
	Kind     string   `json:"kind"`
	State    State    `json:"state"`
	Needs    []string `json:"needs,omitempty"`
	Attempts int      `json:"attempts"`
	Machine  string   `json:"machine,omitempty"`
	// Branch is the worktree branch it worked on, Path its directory.
	Branch   string    `json:"branch,omitempty"`
	Path     string    `json:"path,omitempty"`
	Started  time.Time `json:"started,omitzero"`
	Ended    time.Time `json:"ended,omitzero"`
	Duration int64     `json:"duration_ms,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Approval *Approval `json:"approval,omitempty"`
	Result   *Result   `json:"result,omitempty"`
}

// NewReport builds a plan's report; now is the end of what still runs.
func NewReport(p Plan, now time.Time) Report {
	r := Report{
		ID: p.ID, Name: p.Name, State: p.State, Reason: p.Reason,
		Started: p.CreatedAt, Ended: p.EndedAt, Counts: p.Summary().Counts,
		Tasks: make([]TaskReport, 0, len(p.Tasks)),
	}
	r.Duration = span(p.CreatedAt, p.EndedAt, now).Milliseconds()
	for i := range p.Tasks {
		t := &p.Tasks[i]
		tr := TaskReport{
			ID: t.Spec.ID, Title: t.Spec.Title, Kind: t.Spec.Kind, State: t.State, Needs: t.Spec.Needs,
			Attempts: t.Attempt, Machine: t.Machine, Branch: t.Spec.Branch(p.ID), Path: t.Path,
			Started: t.StartedAt, Ended: t.EndedAt, Reason: t.Reason, Approval: t.Approval, Result: t.Result,
		}
		if !t.StartedAt.IsZero() {
			tr.Duration = span(t.StartedAt, t.EndedAt, now).Milliseconds()
		}
		r.Tasks = append(r.Tasks, tr)
	}
	return r
}

func span(from, to, now time.Time) time.Duration {
	if to.IsZero() {
		to = now
	}
	return max(to.Sub(from), 0)
}

// WriteText writes a report for people: the plan, then each task with its
// outcome, changed files and summary. screen is how many of each task's
// last screen lines to show (0: none).
func (r Report) WriteText(w io.Writer, screen int) error {
	b := &strings.Builder{}
	title := r.ID
	if r.Name != "" {
		title += " · " + r.Name
	}
	fmt.Fprintf(b, "Plan %s: %s in %s", title, r.State, round(r.Duration))
	if r.Reason != "" {
		fmt.Fprintf(b, " (%s)", r.Reason)
	}
	b.WriteString("\n")
	var counts []string
	for _, s := range States {
		if n := r.Counts[s]; n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, s))
		}
	}
	fmt.Fprintf(b, "  %d tasks: %s\n", len(r.Tasks), strings.Join(counts, ", "))
	for _, t := range r.Tasks {
		b.WriteString("\n")
		fmt.Fprintf(b, "%s %s  %s", mark(t.State), t.ID, t.State)
		if t.Duration > 0 {
			fmt.Fprintf(b, " in %s", round(t.Duration))
		}
		if t.Attempts > 1 {
			fmt.Fprintf(b, ", %d attempts", t.Attempts)
		}
		where := []string{t.Kind}
		if t.Machine != "" {
			where = append(where, "on "+t.Machine)
		}
		if t.Branch != "" {
			where = append(where, "branch "+t.Branch)
		}
		fmt.Fprintf(b, "  (%s)\n", strings.Join(where, ", "))
		if t.Title != "" {
			fmt.Fprintf(b, "    %s\n", t.Title)
		}
		if len(t.Needs) > 0 {
			fmt.Fprintf(b, "    needs: %s\n", strings.Join(t.Needs, ", "))
		}
		if t.Reason != "" {
			fmt.Fprintf(b, "    why: %s\n", t.Reason)
		}
		if t.Approval != nil && !t.Approval.DecidedAt.IsZero() {
			verdict := "approved"
			if !t.Approval.Approved {
				verdict = "refused"
			}
			if t.Approval.By != "" {
				verdict += " by " + t.Approval.By
			}
			fmt.Fprintf(b, "    %s\n", verdict)
		}
		res := t.Result
		if res == nil {
			continue
		}
		if res.ExitCode != nil {
			fmt.Fprintf(b, "    exit status: %d\n", *res.ExitCode)
		}
		if len(res.Changed) > 0 {
			more := ""
			if res.ChangedMore > 0 {
				more = fmt.Sprintf(" and %d more", res.ChangedMore)
			}
			fmt.Fprintf(b, "    changed (%d%s):\n", len(res.Changed), more)
			for _, f := range res.Changed {
				fmt.Fprintf(b, "      %s\n", f)
			}
		}
		if res.Summary != "" {
			b.WriteString("    summary:\n")
			for line := range strings.SplitSeq(res.Summary, "\n") {
				fmt.Fprintf(b, "      %s\n", line)
			}
		}
		if lines := tail(res.Output, screen); len(lines) > 0 {
			b.WriteString("    check output:\n")
			for _, l := range lines {
				fmt.Fprintf(b, "      │ %s\n", l)
			}
		}
		if lines := tail(res.Screen, screen); len(lines) > 0 {
			b.WriteString("    screen:\n")
			for _, l := range lines {
				fmt.Fprintf(b, "      │ %s\n", l)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func tail(lines []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

// mark is a state's one-character sign in a report.
func mark(s State) string {
	switch s {
	case StateSucceeded:
		return "✓"
	case StateFailed:
		return "✗"
	case StateCancelled, StateSkipped:
		return "−"
	case StateBlocked, StateApproval:
		return "!"
	case StateRunning:
		return "▶"
	}
	return "·"
}

func round(ms int64) time.Duration {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= time.Minute:
		return d.Round(time.Second)
	case d >= time.Second:
		return d.Round(100 * time.Millisecond)
	}
	return d.Round(time.Millisecond)
}
