package workflow

import (
	"strings"
	"time"
)

// iteration is the item or round a step's attempt is on.
type iteration struct {
	item  string
	index int
	loop  bool
}

// exprContextLocked is what step i's expressions read (i < 0: the run's
// outputs and repeat's until, which see every step): the run's inputs,
// trigger and steps, and success() and failure() over the step's needs.
func (s *Service) exprContextLocked(r *Run, i int, it *iteration) Context {
	steps := make(map[string]any, len(r.Steps))
	for j := range r.Steps {
		steps[r.Steps[j].ID] = stepView(&r.Steps[j])
	}
	inputs := make(map[string]any, len(r.Inputs))
	for k, v := range r.Inputs {
		inputs[k] = v
	}
	trigger := map[string]any{}
	for k, v := range r.Trigger.Data {
		trigger[k] = v
	}
	trigger["type"] = r.Trigger.Type
	if _, ok := trigger["time"]; !ok && !r.Trigger.Time.IsZero() {
		trigger["time"] = r.Trigger.Time.Format(time.RFC3339)
	}
	vars := map[string]any{
		"inputs": inputs, "trigger": trigger, "steps": steps,
		"run": map[string]any{"id": r.ID, "workflow": r.Workflow, "dir": r.Dir},
	}
	if it != nil && it.loop {
		vars["item"], vars["index"] = it.item, float64(it.index)
	}
	ctx := Context{Vars: vars, Cancelled: r.Cancelled, Success: true}
	var needs []string
	if i >= 0 {
		needs = r.Spec.Steps[i].Needs
	} else {
		for _, st := range r.Steps {
			needs = append(needs, st.ID)
		}
	}
	for j, n := range needs {
		st := r.Step(n)
		if st == nil {
			continue
		}
		cont := i >= 0 && r.Spec.Step(n) != nil && r.Spec.Step(n).Continue()
		if i < 0 {
			cont = r.Spec.Steps[j].Continue()
		}
		switch st.State {
		case StepSucceeded:
		case StepFailed:
			ctx.Failure = true
			if !cont {
				ctx.Success = false
			}
		case StepSkipped, StepCancelled:
			ctx.Success = false
			if st.State == StepCancelled {
				ctx.Failure = true
			}
		default:
			if i >= 0 {
				ctx.Success = false
			}
		}
	}
	return ctx
}

// stepView is a step as expressions read it: its outcome, and its latest
// result's fields.
func stepView(st *StepRun) map[string]any {
	outcome := st.State.Outcome()
	last := st.Last()
	if outcome == "" && last != nil {
		outcome = last.Outcome // a repeat between rounds
	}
	v := map[string]any{
		"outcome": nilIfEmpty(outcome), "ok": outcome == OutcomeSuccess, "state": string(st.State),
		"attempts": float64(st.Attempts), "reason": st.Reason,
	}
	if !st.StartedAt.IsZero() {
		v["started_at"] = st.StartedAt.Format(time.RFC3339)
	}
	if !st.EndedAt.IsZero() {
		v["ended_at"] = st.EndedAt.Format(time.RFC3339)
	}
	if last != nil {
		for k, x := range resultView(last) {
			if k != "outcome" && k != "reason" {
				v[k] = x
			}
		}
		if st.Reason == "" {
			v["reason"] = last.Reason
		}
	}
	results := make([]any, len(st.Results))
	for i := range st.Results {
		results[i] = resultView(&st.Results[i])
	}
	v["results"] = results
	return v
}

func resultView(r *Result) map[string]any {
	v := map[string]any{
		"outcome": r.Outcome, "ok": r.Outcome == OutcomeSuccess, "attempt": float64(r.Attempt),
		"item": r.Item, "index": float64(r.Index), "reason": r.Reason,
		"output":  strings.Join(r.Output, "\n"),
		"summary": r.Summary, "screen": strings.Join(r.Screen, "\n"),
	}
	if r.ExitCode != nil {
		v["exit_code"] = float64(*r.ExitCode)
	} else {
		v["exit_code"] = nil
	}
	outputs := map[string]any{}
	for k, x := range r.Outputs {
		outputs[k] = x
	}
	v["outputs"] = outputs
	changed := make([]any, len(r.Changed))
	for i, c := range r.Changed {
		changed[i] = c
	}
	v["changed"] = changed
	if r.Kind != "" {
		v["kind"] = r.Kind
	}
	if r.Plan != "" {
		v["plan"] = r.Plan
	}
	return v
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
