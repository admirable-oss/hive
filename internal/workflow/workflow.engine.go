package workflow

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/task"
)

// launch is an attempt to start: what it runs, resolved under the lock.
type launch struct {
	ctx      context.Context
	run      string
	workflow string
	step     int
	spec     StepSpec
	resolved Resolved
	dir      string // the run's directory
	defEnv   string // the workflow's environment, resolved
	plan     string // an agent step's plan, when it resumes
	attempt  int
}

// outcome is how an attempt ended, before it is recorded.
type outcome struct {
	res      Result
	outcome  string
	stopping bool // the daemon stops: record nothing, resume later
}

// tick starts queued runs, advances running ones and launches their
// steps. It returns when it next needs to run (zero: on a change).
func (s *Service) tick(ctx context.Context) time.Time {
	now := s.cfg.Now()
	var (
		launches []launch
		next     time.Time
		gone     []string
	)
	soonest := func(t time.Time) {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	s.mu.Lock()
	running, per := s.runningLocked()
	for _, r := range slices.Clone(s.order) {
		switch r.State {
		case StateQueued:
			if r.Cancelled {
				s.finishRunLocked(r, now)
				continue
			}
			if running >= s.cfg.MaxRuns {
				continue
			}
			if r.Spec.Concurrency != ConcurrencyParallel && per[r.Workflow] > 0 {
				continue
			}
			r.State, r.StartedAt = StateRunning, now
			running++
			per[r.Workflow]++
			s.log.Info("workflow run started", "run", r.ID, "workflow", r.Workflow)
			s.commitLocked(r)
			fallthrough
		case StateRunning:
			l, wake := s.advanceLocked(ctx, r, now)
			launches = append(launches, l...)
			soonest(wake)
			if r.State.Finished() {
				// Its slot is free for the runs after it.
				running--
				per[r.Workflow]--
			}
		}
	}
	gone = s.pruneLocked(now)
	s.mu.Unlock()
	s.forget(ctx, gone)
	for _, l := range launches {
		s.wg.Go(func() { s.attempt(l.ctx, l) })
	}
	return next
}

// advanceLocked settles r's steps: it starts those whose needs finished
// (or skips them), retries those whose backoff ended, and finishes the run
// when every step has. It returns the attempts to launch and when it
// needs to look again.
func (s *Service) advanceLocked(ctx context.Context, r *Run, now time.Time) ([]launch, time.Time) {
	var (
		out  []launch
		next time.Time
	)
	if b := r.Spec.Timeout(); b > 0 && !r.Cancelled {
		deadline := r.StartedAt.Add(b)
		if !now.Before(deadline) {
			s.cancelLocked(r, "its budget of "+b.String()+" ran out")
		} else {
			next = deadline
		}
	}
	for i := range r.Steps {
		st, spec := &r.Steps[i], r.Spec.Steps[i]
		switch st.State {
		case StepWaiting:
			if r.Cancelled {
				s.endStepLocked(r, i, StepCancelled, "its run was cancelled", now)
				continue
			}
			if !s.needsFinishedLocked(r, spec) {
				continue
			}
			ok, err := s.conditionLocked(r, i)
			switch {
			case err != nil:
				s.endStepLocked(r, i, StepFailed, err.Error(), now)
				continue
			case !ok:
				why := "its condition is false"
				if spec.If == "" {
					why = "a step it needs did not succeed"
				}
				s.endStepLocked(r, i, StepSkipped, why, now)
				continue
			}
			st.StartedAt = now
			if spec.ForeachExpr != "" || spec.Foreach != nil {
				items, err := s.itemsLocked(r, i)
				if err != nil {
					s.endStepLocked(r, i, StepFailed, err.Error(), now)
					continue
				}
				st.Items = items
				if len(items) == 0 {
					s.endStepLocked(r, i, StepSucceeded, "no items", now)
					continue
				}
			}
			if l, ok := s.launchLocked(ctx, r, i, now); ok {
				out = append(out, l)
			}
		case StepRetrying:
			if r.Cancelled {
				s.endStepLocked(r, i, StepCancelled, "its run was cancelled", now)
				continue
			}
			if now.Before(st.NextAt) {
				if next.IsZero() || st.NextAt.Before(next) {
					next = st.NextAt
				}
				continue
			}
			if l, ok := s.launchLocked(ctx, r, i, now); ok {
				out = append(out, l)
			}
		case StepRunning:
			if s.live[liveKey{r.ID, i}] == nil {
				// Resumed: an agent step whose plan a previous daemon
				// started.
				if l, ok := s.resumeAttemptLocked(ctx, r, i); ok {
					out = append(out, l)
				}
			}
		}
	}
	if !slices.ContainsFunc(r.Steps, func(st StepRun) bool { return !st.State.Finished() }) {
		s.finishRunLocked(r, now)
	}
	return out, next
}

func (s *Service) needsFinishedLocked(r *Run, spec StepSpec) bool {
	for _, n := range spec.Needs {
		if st := r.Step(n); st == nil || !st.State.Finished() {
			return false
		}
	}
	return true
}

// conditionLocked decides whether step i runs: its if, by default
// success().
func (s *Service) conditionLocked(r *Run, i int) (bool, error) {
	spec := r.Spec.Steps[i]
	ctx := s.exprContextLocked(r, i, nil)
	if spec.If == "" {
		return ctx.Success, nil
	}
	e, err := ParseExpr(spec.If)
	if err != nil {
		return false, err
	}
	v, err := e.Eval(ctx)
	if err != nil {
		return false, err
	}
	return Truthy(v), nil
}

// itemsLocked resolves foreach's items.
func (s *Service) itemsLocked(r *Run, i int) ([]string, error) {
	spec := r.Spec.Steps[i]
	if spec.ForeachExpr == "" {
		return slices.Clone(spec.Foreach), nil
	}
	e, err := ParseExpr(spec.ForeachExpr)
	if err != nil {
		return nil, err
	}
	v, err := e.Eval(s.exprContextLocked(r, i, nil))
	if err != nil {
		return nil, err
	}
	var items []string
	switch x := v.(type) {
	case nil:
	case []any:
		for _, e := range x {
			items = append(items, Text(e))
		}
	case string:
		// Text: one item per line, as a command's output gives them.
		for line := range strings.SplitSeq(x, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				items = append(items, line)
			}
		}
	default:
		return nil, fmt.Errorf("foreach: %s is not a list", Text(v))
	}
	if len(items) > MaxForeach {
		return nil, fmt.Errorf("foreach gives %d items (at most %d)", len(items), MaxForeach)
	}
	return items, nil
}

// launchLocked starts the next attempt of step i: its expressions are
// resolved now, from what the run has recorded.
func (s *Service) launchLocked(ctx context.Context, r *Run, i int, now time.Time) (launch, bool) {
	st, spec := &r.Steps[i], r.Spec.Steps[i]
	item, index := "", st.Iteration
	if st.Items != nil {
		item = st.Items[st.Iteration]
	}
	ectx := s.exprContextLocked(r, i, &iteration{item: item, index: index, loop: st.Items != nil || spec.Repeat != nil})
	res, err := s.resolveStepLocked(r, spec, ectx)
	if err != nil {
		st.Attempt++
		st.Attempts++
		st.Current = &Result{Attempt: st.Attempt, Item: item, Index: index, StartedAt: now, EndedAt: now, Reason: err.Error(), Outcome: OutcomeFailure}
		s.endStepLocked(r, i, StepFailed, err.Error(), now)
		return launch{}, false
	}
	if spec.Kind() == KindAgent {
		kind, err := s.chooseKind(spec.Agent)
		if err != nil {
			st.Attempt++
			st.Attempts++
			st.Current = &Result{Attempt: st.Attempt, Item: item, Index: index, StartedAt: now, EndedAt: now, Reason: err.Error(), Outcome: OutcomeFailure}
			s.endStepLocked(r, i, StepFailed, err.Error(), now)
			return launch{}, false
		}
		res.Kind = kind
	}
	defEnv, err := render(r.Spec.Environment, ectx)
	if err != nil {
		s.endStepLocked(r, i, StepFailed, err.Error(), now)
		return launch{}, false
	}
	st.State, st.NextAt, st.Reason = StepRunning, time.Time{}, ""
	st.Attempt++
	st.Attempts++
	st.Current = &Result{Attempt: st.Attempt, Item: item, Index: index, Resolved: res, Kind: res.Kind, StartedAt: now}
	msg := fmt.Sprintf("attempt %d", st.Attempt)
	if st.Items != nil {
		msg += fmt.Sprintf(" of item %d (%s)", index+1, item)
	} else if spec.Repeat != nil {
		msg += fmt.Sprintf(" of round %d", index+1)
	}
	if res.Kind != "" {
		msg += " with " + res.Kind
	}
	logStep(st, now, msg)
	s.commitLocked(r, i)
	actx, cancel := context.WithCancelCause(ctx)
	s.live[liveKey{r.ID, i}] = &liveAttempt{cancel: cancel}
	return launch{
		ctx: actx, run: r.ID, workflow: r.Workflow, step: i, spec: spec, resolved: res,
		dir: r.Dir, defEnv: defEnv, attempt: st.Attempt,
	}, true
}

// resumeAttemptLocked waits again on an agent step's plan that a previous
// daemon started.
func (s *Service) resumeAttemptLocked(ctx context.Context, r *Run, i int) (launch, bool) {
	st := &r.Steps[i]
	if st.Current == nil {
		return launch{}, false
	}
	actx, cancel := context.WithCancelCause(ctx)
	s.live[liveKey{r.ID, i}] = &liveAttempt{cancel: cancel}
	if r.Cancelled {
		cancel(ErrCancelled) // cancelled before it resumed: its task is cancelled
	}
	return launch{
		ctx: actx, run: r.ID, workflow: r.Workflow, step: i, spec: r.Spec.Steps[i], resolved: st.Current.Resolved,
		dir: r.Dir, plan: st.Current.Plan, attempt: st.Attempt,
	}, true
}

// resolveStepLocked replaces a step's expressions.
func (s *Service) resolveStepLocked(r *Run, spec StepSpec, ctx Context) (Resolved, error) {
	var res Resolved
	var err error
	str := func(dst *string, src, what string) {
		if err != nil {
			return
		}
		var e error
		if *dst, e = render(src, ctx); e != nil {
			err = fmt.Errorf("%s: %w", what, e)
		}
	}
	str(&res.Prompt, spec.Prompt, "prompt")
	str(&res.EnvironmentID, spec.Environment, "environment")
	str(&res.Worktree, spec.Worktree, "worktree")
	str(&res.Shell, spec.Shell, "run")
	str(&res.Dir, spec.Dir, "dir")
	if spec.Action != "" {
		res.Action = spec.Action
	}
	res.Uses = spec.Uses
	for i, a := range spec.Run {
		var v string
		str(&v, a, fmt.Sprintf("run[%d]", i))
		res.Run = append(res.Run, v)
	}
	if len(spec.Env) > 0 {
		res.Env = map[string]string{}
		for _, k := range sortedKeys(spec.Env) {
			var v string
			str(&v, spec.Env[k], "env "+k)
			res.Env[k] = v
		}
	}
	if len(spec.With) > 0 {
		res.With = map[string]string{}
		for _, k := range sortedKeys(spec.With) {
			var v string
			str(&v, spec.With[k], "with "+k)
			res.With[k] = v
		}
	}
	if err == nil && len(res.Prompt) > MaxPromptBytes {
		err = fmt.Errorf("the prompt is %d bytes once resolved (at most %d)", len(res.Prompt), MaxPromptBytes)
	}
	return res, err
}

// chooseKind picks the first kind of prefs this machine can run.
func (s *Service) chooseKind(prefs []string) (string, error) {
	var installed []string
	if s.ports.Agents != nil {
		installed = s.ports.Agents.Installed()
	}
	for _, k := range prefs {
		if k == AnyAgent {
			if len(installed) > 0 {
				return installed[0], nil
			}
			continue
		}
		if slices.Contains(installed, k) {
			return k, nil
		}
	}
	return "", fmt.Errorf("no agent of kind %s is installed here (installed: %s)", strings.Join(prefs, ", "), cmpStr(strings.Join(installed, ", "), "none"))
}

// endStepLocked finishes step i in state.
func (s *Service) endStepLocked(r *Run, i int, state StepState, reason string, now time.Time) {
	st := &r.Steps[i]
	st.State, st.Reason, st.NextAt = state, reason, time.Time{}
	if st.EndedAt.IsZero() {
		st.EndedAt = now
	}
	if reason != "" {
		logStep(st, now, string(state)+": "+reason)
	}
	s.commitLocked(r, i)
}

// finishRunLocked ends r once every step has, and evaluates its outputs.
func (s *Service) finishRunLocked(r *Run, now time.Time) {
	if r.State.Finished() {
		return
	}
	var failed []string
	for i, st := range r.Steps {
		switch {
		case st.State == StepCancelled && !r.Cancelled,
			st.State == StepFailed && !r.Spec.Steps[i].Continue():
			failed = append(failed, st.ID)
		case st.State == StepWaiting || st.State == StepRetrying:
			// A queued run cancelled before it started.
			r.Steps[i].State = StepCancelled
			r.Steps[i].Reason = "its run was cancelled"
		}
	}
	switch {
	case r.Cancelled:
		r.State = StateCancelled
	case len(failed) > 0:
		r.State = StateFailed
		st := r.Step(failed[0])
		r.Reason = "step " + st.ID + " " + string(st.State)
		if st.Reason != "" {
			r.Reason += ": " + st.Reason
		}
	default:
		r.State = StateSucceeded
		r.Reason = ""
	}
	if len(r.Spec.Outputs) > 0 {
		ctx := s.exprContextLocked(r, -1, nil)
		r.Outputs = map[string]string{}
		for _, name := range sortedKeys(r.Spec.Outputs) {
			v, err := render(r.Spec.Outputs[name], ctx)
			if err != nil {
				v = ""
				r.Reason = strings.TrimSpace(r.Reason + "; output " + name + ": " + err.Error())
			}
			r.Outputs[name] = v
		}
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = now
	}
	r.EndedAt = now
	s.log.Info("workflow run finished", "run", r.ID, "workflow", r.Workflow, "state", r.State, "reason", r.Reason)
	s.commitLocked(r)
}

// --- Attempts ---

// attempt runs one attempt, under ctx (its launch's), and records how
// it ended.
func (s *Service) attempt(ctx context.Context, l launch) {
	var out outcome
	switch l.spec.Kind() {
	case KindAgent:
		out = s.runAgent(ctx, l)
	case KindAction:
		out = s.runAction(ctx, l)
	case KindStep:
		out = s.runStep(ctx, l)
	default:
		out = s.runCommand(ctx, l)
	}
	s.finishAttempt(l, out)
}

// stopped reports how ctx ended: cancelled by a person (or a budget), or
// stopped with the daemon.
func stopped(ctx context.Context) (cancelled, stopping bool) {
	if ctx.Err() == nil {
		return false, false
	}
	if errors.Is(context.Cause(ctx), ErrCancelled) {
		return true, false
	}
	return false, true
}

func (s *Service) runAgent(ctx context.Context, l launch) outcome {
	res := Result{Resolved: l.resolved, Kind: l.resolved.Kind, Plan: l.plan}
	if l.plan == "" {
		env, err := s.environment(ctx, l)
		if err != nil {
			res.Reason = err.Error()
			return outcome{res: res, outcome: OutcomeFailure}
		}
		res.Resolved.EnvironmentID = env
		ts := task.Spec{
			ID: l.spec.ID, Title: cmpStr(l.spec.Name, l.spec.ID), Prompt: l.resolved.Prompt, Kind: l.resolved.Kind,
			EnvironmentID: env, Worktree: l.resolved.Worktree, Isolate: l.spec.Isolate, Base: l.spec.Base,
			Until: task.Until(l.spec.Until), Match: l.spec.Match, Command: l.spec.Check, TimeoutMS: l.spec.TimeoutMS,
			Machine: l.spec.Machine, Labels: l.spec.Labels, KeepAgent: l.spec.KeepAgent, NoSummary: l.spec.NoSummary,
			Env: map[string]string{EnvWorkflow: l.workflow, EnvRun: l.run, EnvStep: l.spec.ID},
		}
		p, err := s.ports.Tasks.Submit(ctx, task.PlanSpec{Name: "workflow " + l.workflow + " " + l.run + " " + l.spec.ID, Tasks: []task.Spec{ts}})
		if err != nil {
			if c, st := stopped(ctx); st {
				return outcome{stopping: true}
			} else if c {
				res.Reason = "cancelled"
				return outcome{res: res, outcome: OutcomeCancelled}
			}
			res.Reason = "starting its task: " + err.Error()
			return outcome{res: res, outcome: OutcomeFailure}
		}
		res.Plan = p.ID
		s.recordPlan(ctx, l, p.ID, env)
	}
	wr, err := s.ports.Tasks.Wait(ctx, task.WaitRequest{ID: res.Plan, Until: task.WaitFinished})
	if err != nil {
		cancelled, stopping := stopped(ctx)
		switch {
		case stopping:
			return outcome{stopping: true}
		case cancelled:
			// Cancel the task, and keep what it did.
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if _, err := s.ports.Tasks.Cancel(cctx, res.Plan, ""); err != nil && !errors.Is(err, task.ErrState) {
				s.log.Warn("cancelling a workflow step's task", "plan", res.Plan, "err", err)
			}
			if wr, err := s.ports.Tasks.Wait(cctx, task.WaitRequest{ID: res.Plan, Until: task.WaitFinished, Timeout: 10 * time.Second}); err == nil {
				fromTask(&res, wr.Plan)
			}
			res.Reason = "cancelled"
			return outcome{res: res, outcome: OutcomeCancelled}
		case errors.Is(err, task.ErrNotFound):
			res.Reason = "its plan " + res.Plan + " is gone"
			return outcome{res: res, outcome: OutcomeFailure}
		}
		res.Reason = "waiting for its task: " + err.Error()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	ok := fromTask(&res, wr.Plan)
	if ok {
		return outcome{res: res, outcome: OutcomeSuccess}
	}
	return outcome{res: res, outcome: OutcomeFailure}
}

// fromTask fills res from a plan of one task, and reports whether it
// succeeded.
func fromTask(res *Result, p task.Plan) bool {
	if len(p.Tasks) == 0 {
		res.Reason = "its plan has no task"
		return false
	}
	t := p.Tasks[0]
	res.Reason = t.Reason
	if t.Result != nil {
		res.Summary, res.Changed, res.ChangedMore, res.Screen = t.Result.Summary, t.Result.Changed, t.Result.ChangedMore, t.Result.Screen
		res.ExitCode, res.Output = t.Result.ExitCode, t.Result.Output
		if res.Reason == "" && t.State != task.StateSucceeded {
			res.Reason = t.Result.Outcome
		}
	}
	if res.Reason == "" && t.State != task.StateSucceeded {
		res.Reason = "its task " + string(t.State)
	}
	return t.State == task.StateSucceeded
}

// recordPlan saves the plan an agent step's attempt runs as, at once: a
// daemon that stops now waits on it again rather than starting another.
func (s *Service) recordPlan(ctx context.Context, l launch, plan, env string) {
	s.mu.Lock()
	r := s.runs[l.run]
	if r == nil || r.Steps[l.step].Current == nil {
		s.mu.Unlock()
		return
	}
	st := &r.Steps[l.step]
	st.Current.Plan = plan
	st.Current.Resolved.EnvironmentID = env
	logStep(st, s.cfg.Now(), "its task runs as plan "+plan)
	s.commitLocked(r, l.step)
	snap := r.clone()
	s.mu.Unlock()
	if err := s.saveRun(context.WithoutCancel(ctx), snap); err != nil {
		s.log.Warn("saving a workflow run", "run", l.run, "err", err)
	}
}

// environment is where an agent step (or an action) runs: its own
// environment, the workflow's, or one for the run's directory.
func (s *Service) environment(ctx context.Context, l launch) (string, error) {
	if l.resolved.EnvironmentID != "" {
		return l.resolved.EnvironmentID, nil
	}
	if l.defEnv != "" {
		return l.defEnv, nil
	}
	if l.dir == "" {
		return "", errors.New("no environment to run in: give the step or the workflow an environment, or run it from a directory")
	}
	return s.ports.Workspace.EnvironmentFor(ctx, l.dir)
}

// directory is where a command runs: its environment's directory (or the
// run's), with its dir under it.
func (s *Service) directory(ctx context.Context, l launch) (string, error) {
	base := l.dir
	env := cmpStr(l.resolved.EnvironmentID, l.defEnv)
	if env != "" {
		d, err := s.ports.Workspace.EnvironmentDir(ctx, env)
		if err != nil {
			return "", fmt.Errorf("environment %s: %w", env, err)
		}
		base = d
	}
	dir := l.resolved.Dir
	switch {
	case dir != "" && filepath.IsAbs(dir):
		return dir, nil
	case base == "":
		return "", errors.New("no directory to run in: give the step or the workflow an environment, or run it from a directory")
	case dir != "":
		return filepath.Join(base, dir), nil
	}
	return base, nil
}

func (s *Service) runCommand(actx context.Context, l launch) outcome {
	res := Result{Resolved: l.resolved}
	dir, err := s.directory(actx, l)
	if err != nil {
		res.Reason = err.Error()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	argv := l.resolved.Run
	if l.resolved.Shell != "" {
		argv = shellArgv(l.resolved.Shell)
	}
	outFile, err := s.store.OutputFile(fmt.Sprintf("%s-%s-%d", l.run, l.spec.ID, l.attempt))
	if err != nil {
		res.Reason = "making its output file: " + err.Error()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	defer os.Remove(outFile)
	env := maps.Clone(l.resolved.Env)
	if env == nil {
		env = map[string]string{}
	}
	env[EnvOutput], env[EnvWorkflow], env[EnvRun], env[EnvStep] = outFile, l.workflow, l.run, l.spec.ID
	ctx := actx
	if t := l.spec.Timeout(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	code, lines, err := s.ports.Commands.Run(ctx, dir, argv, env)
	res.Output = lines
	res.Outputs, _ = readOutputs(outFile)
	if c, st := stopped(actx); st {
		return outcome{stopping: true}
	} else if c {
		res.Reason = "cancelled"
		return outcome{res: res, outcome: OutcomeCancelled}
	}
	if ctx.Err() != nil {
		res.Reason = "timed out after " + l.spec.Timeout().String()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	if err != nil {
		res.Reason = err.Error()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	res.ExitCode = &code
	if code != 0 {
		res.Reason = fmt.Sprintf("exit status %d", code)
		return outcome{res: res, outcome: OutcomeFailure}
	}
	return outcome{res: res, outcome: OutcomeSuccess}
}

// shellArgv runs a shell line: sh, or cmd where there is none.
func shellArgv(line string) []string {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			return []string{"cmd", "/C", line}
		}
	}
	return []string{"sh", "-c", line}
}

func (s *Service) runAction(actx context.Context, l launch) outcome {
	res := Result{Resolved: l.resolved}
	if s.ports.Actions == nil {
		res.Reason = "plugins do not run here"
		return outcome{res: res, outcome: OutcomeFailure}
	}
	plugin, action, _ := strings.Cut(l.resolved.Action, "/")
	outFile, err := s.store.OutputFile(fmt.Sprintf("%s-%s-%d", l.run, l.spec.ID, l.attempt))
	if err != nil {
		res.Reason = "making its output file: " + err.Error()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	defer os.Remove(outFile)
	ctx := actx
	if t := l.spec.Timeout(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	ar, err := s.ports.Actions.Action(ctx, ActionRequest{
		Plugin: plugin, Action: action, With: l.resolved.With, Output: outFile,
		Workflow: l.workflow, Run: l.run, Step: l.spec.ID, Environment: cmpStr(l.resolved.EnvironmentID, l.defEnv),
	})
	res.Output = tailLines(ar.Output, MaxOutputLines)
	res.Outputs, _ = readOutputs(outFile)
	if c, st := stopped(actx); st {
		return outcome{stopping: true}
	} else if c {
		res.Reason = "cancelled"
		return outcome{res: res, outcome: OutcomeCancelled}
	}
	if ctx.Err() != nil {
		res.Reason = "timed out after " + l.spec.Timeout().String()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	if err != nil {
		res.Reason = err.Error()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	code := ar.ExitCode
	res.ExitCode = &code
	if code != 0 {
		res.Reason = fmt.Sprintf("exit status %d", code)
		return outcome{res: res, outcome: OutcomeFailure}
	}
	return outcome{res: res, outcome: OutcomeSuccess}
}

// runStep runs a plugin's step: its resolved inputs to the plugin, its
// typed outputs back as the step's outputs.
func (s *Service) runStep(actx context.Context, l launch) outcome {
	res := Result{Resolved: l.resolved}
	if s.ports.Steps == nil {
		res.Reason = "plugins do not run here"
		return outcome{res: res, outcome: OutcomeFailure}
	}
	plugin, step, _ := strings.Cut(l.resolved.Uses, "/")
	ctx := actx
	if t := l.spec.Timeout(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	sr, err := s.ports.Steps.Step(ctx, StepRequest{
		Plugin: plugin, Step: step, With: l.resolved.With,
		Workflow: l.workflow, Run: l.run, ID: l.spec.ID, Environment: cmpStr(l.resolved.EnvironmentID, l.defEnv),
	})
	res.Output = tailLines(sr.Output, MaxOutputLines)
	res.Outputs = sr.Outputs
	if c, st := stopped(actx); st {
		return outcome{stopping: true}
	} else if c {
		res.Reason = "cancelled"
		return outcome{res: res, outcome: OutcomeCancelled}
	}
	if ctx.Err() != nil {
		res.Reason = "timed out after " + l.spec.Timeout().String()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	code := 0
	if err != nil {
		code = 1
		res.ExitCode, res.Reason = &code, err.Error()
		return outcome{res: res, outcome: OutcomeFailure}
	}
	res.ExitCode = &code
	return outcome{res: res, outcome: OutcomeSuccess}
}

// tailLines is text's last n non-empty-tailed lines.
func tailLines(text string, n int) []string {
	text = strings.TrimRight(strings.ToValidUTF8(text, "�"), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		if len(l) > maxLineBytes {
			lines[i] = l[:maxLineBytes]
		}
	}
	return lines
}

// readOutputs reads a $HIVE_OUTPUT file: name=value lines, and
// name<<END … END blocks, at most MaxOutputsBytes.
func readOutputs(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	size := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), MaxOutputsBytes+1)
	var (
		block, end string
		lines      []string
	)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if block != "" {
			if line == end {
				v := strings.Join(lines, "\n")
				if size += len(v); size > MaxOutputsBytes {
					break
				}
				out[block] = v
				block, lines = "", nil
				continue
			}
			lines = append(lines, line)
			continue
		}
		if name, delim, ok := strings.Cut(line, "<<"); ok && inputPattern.MatchString(name) && delim != "" {
			block, end = name, delim
			continue
		}
		name, v, ok := strings.Cut(line, "=")
		if !ok || !inputPattern.MatchString(name) {
			continue
		}
		if size += len(v); size > MaxOutputsBytes {
			break
		}
		out[name] = v
	}
	if len(out) == 0 {
		return nil, sc.Err()
	}
	return out, sc.Err()
}

// finishAttempt records how an attempt ended: the step succeeds, retries,
// goes on to its next item or round, or fails.
func (s *Service) finishAttempt(l launch, out outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := liveKey{l.run, l.step}
	if live := s.live[key]; live != nil {
		live.cancel(nil)
		delete(s.live, key)
	}
	defer signal(s.wake)
	if out.stopping {
		return // the next daemon resumes it
	}
	r := s.runs[l.run]
	if r == nil {
		return
	}
	st, spec := &r.Steps[l.step], r.Spec.Steps[l.step]
	now := s.cfg.Now()
	cur := st.Current
	if cur == nil {
		cur = &Result{Attempt: st.Attempt}
	}
	started, item, index := cur.StartedAt, cur.Item, cur.Index
	*cur = out.res
	cur.Attempt, cur.StartedAt, cur.Item, cur.Index = st.Attempt, started, item, index
	if cur.Resolved.Kind == "" {
		cur.Resolved.Kind = cur.Kind
	}
	cur.Outcome, cur.EndedAt = out.outcome, now
	st.Current = cur
	if out.outcome != OutcomeSuccess {
		logStep(st, now, fmt.Sprintf("attempt %d %s: %s", st.Attempt, out.outcome, cmpStr(cur.Reason, "no reason")))
	}
	switch {
	case out.outcome == OutcomeCancelled:
		s.keepResultLocked(st, *cur)
		s.endStepLocked(r, l.step, StepCancelled, cmpStr(r.Reason, "cancelled"), now)
		return
	case out.outcome == OutcomeFailure && st.Attempt <= spec.Retries && !r.Cancelled:
		wait := spec.Backoff() << (st.Attempt - 1)
		if wait > MaxBackoff || wait <= 0 {
			wait = MaxBackoff
		}
		st.State, st.NextAt, st.Reason = StepRetrying, now.Add(wait), cur.Reason
		logStep(st, now, "retrying in "+wait.String())
		s.commitLocked(r, l.step)
		return
	}
	// The iteration is over.
	s.keepResultLocked(st, *cur)
	switch {
	case spec.Repeat != nil:
		done, err := s.untilLocked(r, l.step)
		switch {
		case err != nil:
			s.endStepLocked(r, l.step, StepFailed, err.Error(), now)
		case done:
			s.endStepLocked(r, l.step, StepSucceeded, "", now)
		case st.Iteration+1 >= spec.Repeat.Max:
			s.endStepLocked(r, l.step, StepFailed, fmt.Sprintf("its until never held in %d rounds", spec.Repeat.Max), now)
		default:
			s.nextIterationLocked(r, l.step, now)
		}
	case out.outcome == OutcomeFailure:
		s.endStepLocked(r, l.step, StepFailed, cur.Reason, now)
	case st.Items != nil && st.Iteration+1 < len(st.Items):
		s.nextIterationLocked(r, l.step, now)
	default:
		s.endStepLocked(r, l.step, StepSucceeded, "", now)
	}
}

func (s *Service) nextIterationLocked(r *Run, i int, now time.Time) {
	st := &r.Steps[i]
	st.Iteration++
	st.Attempt = 0
	st.State, st.NextAt = StepRetrying, now
	s.commitLocked(r, i)
}

// keepResultLocked adds an iteration's result, keeping only the latest
// results' output lines.
func (s *Service) keepResultLocked(st *StepRun, res Result) {
	st.Results = append(st.Results, res)
	if n := len(st.Results); n > keptResults {
		old := &st.Results[n-keptResults-1]
		old.Output, old.Screen = nil, nil
	}
}

// untilLocked evaluates repeat's until with the step's latest round.
func (s *Service) untilLocked(r *Run, i int) (bool, error) {
	e, err := ParseExpr(r.Spec.Steps[i].Repeat.Until)
	if err != nil {
		return false, err
	}
	v, err := e.Eval(s.exprContextLocked(r, -1, nil))
	if err != nil {
		return false, err
	}
	return Truthy(v), nil
}
