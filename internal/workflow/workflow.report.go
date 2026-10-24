package workflow

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Report is a run as a person (or CI) reads it: what started it, with
// which inputs, and each step's outcome and results.
type Report struct {
	ID         string            `json:"id"`
	Workflow   string            `json:"workflow"`
	State      State             `json:"state"`
	Reason     string            `json:"reason,omitempty"`
	Trigger    Trigger           `json:"trigger"`
	Inputs     map[string]string `json:"inputs"`
	Digest     string            `json:"digest"`
	Dir        string            `json:"dir,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	StartedAt  time.Time         `json:"started_at,omitzero"`
	EndedAt    time.Time         `json:"ended_at,omitzero"`
	DurationMS int64             `json:"duration_ms"`
	Outputs    map[string]string `json:"outputs,omitempty"`
	Steps      []StepReport      `json:"steps"`
}

// StepReport is one step in a Report.
type StepReport struct {
	ID         string            `json:"id"`
	Kind       StepKind          `json:"kind"`
	State      StepState         `json:"state"`
	Attempts   int               `json:"attempts"`
	DurationMS int64             `json:"duration_ms"`
	Reason     string            `json:"reason,omitempty"`
	Agent      string            `json:"agent,omitempty"`
	Plan       string            `json:"plan,omitempty"`
	ExitCode   *int              `json:"exit_code,omitempty"`
	Output     []string          `json:"output,omitempty"`
	Outputs    map[string]string `json:"outputs,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Changed    []string          `json:"changed,omitempty"`
	Items      int               `json:"items,omitempty"`
}

// NewReport makes a run's report; now times what still runs.
func NewReport(r Run, now time.Time) Report {
	rep := Report{
		ID: r.ID, Workflow: r.Workflow, State: r.State, Reason: r.Reason, Trigger: r.Trigger,
		Inputs: r.Inputs, Digest: r.Digest, Dir: r.Dir, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt,
		EndedAt: r.EndedAt, DurationMS: span(r.StartedAt, r.EndedAt, now), Outputs: r.Outputs,
	}
	if rep.Inputs == nil {
		rep.Inputs = map[string]string{}
	}
	for _, st := range r.Steps {
		sr := StepReport{
			ID: st.ID, Kind: st.Kind, State: st.State, Attempts: st.Attempts, Reason: st.Reason,
			DurationMS: span(st.StartedAt, st.EndedAt, now), Items: len(st.Items),
		}
		res := st.Last()
		if res == nil || st.State == StepRunning || st.State == StepRetrying {
			res = st.Current
		}
		if res != nil {
			sr.Agent, sr.Plan, sr.ExitCode, sr.Output, sr.Outputs = res.Kind, res.Plan, res.ExitCode, res.Output, res.Outputs
			sr.Summary, sr.Changed = res.Summary, res.Changed
			if sr.Reason == "" {
				sr.Reason = res.Reason
			}
		}
		rep.Steps = append(rep.Steps, sr)
	}
	return rep
}

func span(from, to, now time.Time) int64 {
	switch {
	case from.IsZero():
		return 0
	case to.IsZero():
		return now.Sub(from).Milliseconds()
	}
	return to.Sub(from).Milliseconds()
}

// WriteText prints the report for a person; lines bounds each step's
// output (0: 10 lines).
func (r Report) WriteText(w io.Writer, lines int) error {
	if lines <= 0 {
		lines = 10
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run %s of %s: %s", r.ID, r.Workflow, r.State)
	if r.DurationMS > 0 {
		fmt.Fprintf(&b, " in %s", (time.Duration(r.DurationMS) * time.Millisecond).Round(time.Second))
	}
	b.WriteString("\n")
	if r.Reason != "" {
		fmt.Fprintf(&b, "  %s\n", r.Reason)
	}
	fmt.Fprintf(&b, "  trigger: %s", r.Trigger.Type)
	if r.Trigger.By != "" {
		fmt.Fprintf(&b, " by %s", r.Trigger.By)
	}
	if r.Trigger.From != "" {
		fmt.Fprintf(&b, " of run %s", r.Trigger.From)
	}
	b.WriteString("\n")
	for _, k := range sortedKeys(r.Inputs) {
		fmt.Fprintf(&b, "  input %s = %q\n", k, r.Inputs[k])
	}
	for _, st := range r.Steps {
		fmt.Fprintf(&b, "\n%s %s (%s)", stepMark(st.State), st.ID, st.State)
		var details []string
		if st.Agent != "" {
			details = append(details, st.Agent)
		}
		if st.Attempts > 1 {
			details = append(details, fmt.Sprintf("%d attempts", st.Attempts))
		}
		if st.Items > 0 {
			details = append(details, fmt.Sprintf("%d items", st.Items))
		}
		if st.ExitCode != nil {
			details = append(details, fmt.Sprintf("exit %d", *st.ExitCode))
		}
		if st.DurationMS > 0 {
			details = append(details, (time.Duration(st.DurationMS) * time.Millisecond).Round(100*time.Millisecond).String())
		}
		if len(details) > 0 {
			fmt.Fprintf(&b, " %s", strings.Join(details, ", "))
		}
		b.WriteString("\n")
		if st.Reason != "" && st.State != StepSucceeded {
			fmt.Fprintf(&b, "    %s\n", st.Reason)
		}
		if st.Summary != "" {
			for l := range strings.SplitSeq(strings.TrimRight(st.Summary, "\n"), "\n") {
				fmt.Fprintf(&b, "    │ %s\n", l)
			}
		}
		out := st.Output
		if len(out) > lines {
			out = out[len(out)-lines:]
		}
		for _, l := range out {
			fmt.Fprintf(&b, "    > %s\n", l)
		}
		for _, k := range sortedKeys(st.Outputs) {
			fmt.Fprintf(&b, "    output %s = %q\n", k, st.Outputs[k])
		}
	}
	if len(r.Outputs) > 0 {
		b.WriteString("\n")
		for _, k := range sortedKeys(r.Outputs) {
			fmt.Fprintf(&b, "output %s = %q\n", k, r.Outputs[k])
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func stepMark(s StepState) string {
	switch s {
	case StepSucceeded:
		return "✓"
	case StepFailed:
		return "✗"
	case StepSkipped:
		return "-"
	case StepCancelled:
		return "⊘"
	case StepRunning, StepRetrying:
		return "…"
	}
	return "·"
}
