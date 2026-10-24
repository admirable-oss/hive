package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Workflow files are TOML:
//
//	version = 1
//	name = "review-push"
//	timeout = "2h"
//
//	[inputs.branch]
//	default = "main"
//
//	[[on]]
//	git = "push"
//	branches = ["main"]
//	[on.inputs]
//	branch = "${{ trigger.branch }}"
//
//	[[step]]
//	id = "review"
//	agent = ["claude", "codex"]
//	prompt = "Review ${{ inputs.branch }}."
//
//	[[step]]
//	id = "test"
//	run = ["go", "test", "./..."]
//
// They are checked strictly: an unknown key is an error, since a mistyped
// `needs` or `if` must not run a step it was meant to hold back.

type fileWorkflow struct {
	Version     int                  `toml:"version"`
	Name        string               `toml:"name"`
	Description string               `toml:"description"`
	Timeout     string               `toml:"timeout"`
	Concurrency string               `toml:"concurrency"`
	Environment string               `toml:"environment"`
	Inputs      map[string]fileInput `toml:"inputs"`
	On          []fileTrigger        `toml:"on"`
	Steps       []fileStep           `toml:"step"`
	Outputs     map[string]string    `toml:"outputs"`
}

type fileInput struct {
	Description string `toml:"description"`
	Default     string `toml:"default"`
	Required    bool   `toml:"required"`
}

type fileTrigger struct {
	Event    string            `toml:"event"`
	Match    map[string]string `toml:"match"`
	Schedule string            `toml:"schedule"`
	Every    string            `toml:"every"`
	Git      string            `toml:"git"`
	Repo     string            `toml:"repo"`
	Branches []string          `toml:"branches"`
	Remote   string            `toml:"remote"`
	Webhook  string            `toml:"webhook"`
	Inputs   map[string]string `toml:"inputs"`
}

type fileStep struct {
	ID    string    `toml:"id"`
	Name  string    `toml:"name"`
	Needs *[]string `toml:"needs"`
	If    string    `toml:"if"`

	Agent       any      `toml:"agent"`
	Prompt      string   `toml:"prompt"`
	Environment string   `toml:"environment"`
	Worktree    string   `toml:"worktree"`
	Isolate     bool     `toml:"isolate"`
	Base        string   `toml:"base"`
	Until       string   `toml:"until"`
	Match       string   `toml:"match"`
	Check       []string `toml:"check"`
	Machine     string   `toml:"machine"`
	Labels      []string `toml:"labels"`
	KeepAgent   bool     `toml:"keep_agent"`
	NoSummary   bool     `toml:"no_summary"`

	Run any               `toml:"run"`
	Dir string            `toml:"dir"`
	Env map[string]string `toml:"env"`

	Action string            `toml:"action"`
	With   map[string]string `toml:"with"`

	Timeout   string `toml:"timeout"`
	Retries   int    `toml:"retries"`
	Backoff   string `toml:"backoff"`
	OnFailure string `toml:"on_failure"`

	Foreach any         `toml:"foreach"`
	Repeat  *fileRepeat `toml:"repeat"`
}

type fileRepeat struct {
	Max   int    `toml:"max"`
	Until string `toml:"until"`
}

// idPattern is what workflow names and step IDs look like.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// inputPattern is what input and output names look like: expressions
// read them as names.
var inputPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

// ValidName reports whether name can name a workflow.
func ValidName(name string) bool { return idPattern.MatchString(name) }

// agentAliases are event names that stand for agent.state in one state.
var agentAliases = map[string]string{
	"agent.done": "done", "agent.blocked": "blocked", "agent.idle": "idle", "agent.exited": "exited",
}

// untils are the agent step conditions (package task's).
var untils = []string{"done", "idle", "match", "command"}

// Parse reads a workflow file. source names it in errors, and its base
// name (without .toml) is the workflow's name when the file gives none.
// The workflow is checked as Validate checks it, so one that parses runs.
func Parse(data []byte, source string) (Spec, error) {
	if len(data) > MaxFileBytes {
		return Spec{}, fmt.Errorf("%w: %s: larger than %d bytes", ErrInvalid, source, MaxFileBytes)
	}
	var f fileWorkflow
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		if se, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return Spec{}, fmt.Errorf("%w: %s: %s", ErrInvalid, source, strings.TrimSpace(se.String()))
		}
		if de, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, col := de.Position()
			return Spec{}, fmt.Errorf("%w: %s:%d:%d: %s", ErrInvalid, source, row, col, de.Error())
		}
		return Spec{}, fmt.Errorf("%w: %s: %w", ErrInvalid, source, err)
	}
	spec, err := f.spec(source)
	if err != nil {
		return Spec{}, fmt.Errorf("%s: %w", source, err)
	}
	if err := Validate(&spec); err != nil {
		return Spec{}, fmt.Errorf("%s: %w", source, err)
	}
	return spec, nil
}

// NameFromSource is the name a workflow file without one gets.
func NameFromSource(source string) string {
	return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func (f fileWorkflow) spec(source string) (Spec, error) {
	switch {
	case f.Version == 0:
		return Spec{}, invalid("version = %d is required (the workflow format this file is written for)", Version)
	case f.Version > Version:
		return Spec{}, invalid("written for workflow format %d; this hive reads format %d (update hive)", f.Version, Version)
	case f.Version < 0:
		return Spec{}, invalid("version %d", f.Version)
	}
	s := Spec{
		Version: f.Version, Name: cmpStr(f.Name, NameFromSource(source)), Description: f.Description,
		Concurrency: Concurrency(f.Concurrency), Environment: f.Environment, Outputs: f.Outputs,
	}
	var err error
	if s.TimeoutMS, err = durationMS(f.Timeout, "timeout"); err != nil {
		return Spec{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(f.Inputs)) {
		in := f.Inputs[name]
		s.Inputs = append(s.Inputs, InputSpec{Name: name, Description: in.Description, Default: in.Default, Required: in.Required})
	}
	for i, t := range f.On {
		ts, err := t.spec()
		if err != nil {
			return Spec{}, fmt.Errorf("trigger %d: %w", i+1, err)
		}
		s.Triggers = append(s.Triggers, ts)
	}
	for i, st := range f.Steps {
		where := fmt.Sprintf("step %d", i+1)
		if st.ID != "" {
			where += " (" + st.ID + ")"
		}
		ss, err := st.spec()
		if err != nil {
			return Spec{}, fmt.Errorf("%s: %w", where, err)
		}
		if st.Needs == nil {
			ss.Needs = nil // filled in by Validate
			if i > 0 {
				ss.Needs = []string{f.Steps[i-1].ID}
			}
		}
		s.Steps = append(s.Steps, ss)
	}
	return s, nil
}

func (t fileTrigger) spec() (TriggerSpec, error) {
	ts := TriggerSpec{
		Event: t.Event, Match: t.Match, Schedule: t.Schedule, Git: t.Git, Repo: t.Repo,
		Branches: t.Branches, Remote: t.Remote, Webhook: t.Webhook, Inputs: t.Inputs,
	}
	if t.Every != "" {
		d, err := time.ParseDuration(t.Every)
		if err != nil || d < MinEvery {
			return TriggerSpec{}, invalid("every %q is not a duration of at least %s", t.Every, MinEvery)
		}
		ts.EveryMS = d.Milliseconds()
	}
	if state, ok := agentAliases[t.Event]; ok {
		ts.Event = "agent.state"
		if ts.Match == nil {
			ts.Match = map[string]string{}
		} else {
			ts.Match = maps.Clone(ts.Match)
		}
		if v, set := ts.Match["state"]; set && v != state {
			return TriggerSpec{}, invalid("event %s matches state %q", t.Event, v)
		}
		ts.Match["state"] = state
	}
	return ts, nil
}

func (st fileStep) spec() (StepSpec, error) {
	s := StepSpec{
		ID: st.ID, Name: st.Name, If: st.If,
		Prompt: st.Prompt, Environment: st.Environment, Worktree: st.Worktree, Isolate: st.Isolate, Base: st.Base,
		Until: st.Until, Match: st.Match, Check: st.Check, Machine: st.Machine, Labels: st.Labels,
		KeepAgent: st.KeepAgent, NoSummary: st.NoSummary,
		Dir: st.Dir, Env: st.Env, Action: st.Action, With: st.With,
		Retries: st.Retries, OnFailure: st.OnFailure,
	}
	if st.Needs != nil {
		s.Needs = slices.Clone(*st.Needs)
		if s.Needs == nil {
			s.Needs = []string{}
		}
	}
	var err error
	if s.Agent, err = stringList(st.Agent, "agent"); err != nil {
		return StepSpec{}, err
	}
	switch v := st.Run.(type) {
	case nil:
	case string:
		if strings.TrimSpace(v) == "" {
			return StepSpec{}, invalid("run is empty")
		}
		s.Shell = v
	default:
		if s.Run, err = stringList(v, "run"); err != nil {
			return StepSpec{}, err
		}
		if len(s.Run) == 0 {
			return StepSpec{}, invalid("run is empty")
		}
	}
	switch v := st.Foreach.(type) {
	case nil:
	case string:
		s.ForeachExpr = v
	default:
		if s.Foreach, err = stringList(v, "foreach"); err != nil {
			return StepSpec{}, err
		}
		if s.Foreach == nil {
			s.Foreach = []string{}
		}
	}
	if st.Repeat != nil {
		s.Repeat = &Repeat{Max: st.Repeat.Max, Until: st.Repeat.Until}
	}
	if s.TimeoutMS, err = durationMS(st.Timeout, "timeout"); err != nil {
		return StepSpec{}, err
	}
	if s.BackoffMS, err = durationMS(st.Backoff, "backoff"); err != nil {
		return StepSpec{}, err
	}
	return s, nil
}

// stringList reads a string or a list of strings.
func stringList(v any, what string) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{x}, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, invalid("%s: %v is not a string", what, e)
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return x, nil
	}
	return nil, invalid("%s must be a string or a list of strings", what)
}

func durationMS(s, what string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, invalid("%s %q is not a duration (30s, 20m, 2h)", what, s)
	}
	return d.Milliseconds(), nil
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Validate checks a workflow: names, steps, triggers, expressions, and
// that needs form a DAG. Every step's needs must already be explicit.
func Validate(s *Spec) error {
	switch {
	case s.Version != Version:
		return invalid("version %d (this hive reads %d)", s.Version, Version)
	case !idPattern.MatchString(s.Name):
		return invalid("name %q: use letters, digits, '.', '_' and '-' (at most 64)", s.Name)
	case len(s.Steps) == 0:
		return invalid("no [[step]]")
	case len(s.Steps) > MaxSteps:
		return invalid("%d steps (at most %d)", len(s.Steps), MaxSteps)
	case len(s.Triggers) > MaxTriggers:
		return invalid("%d triggers (at most %d)", len(s.Triggers), MaxTriggers)
	case len(s.Inputs) > MaxInputs:
		return invalid("%d inputs (at most %d)", len(s.Inputs), MaxInputs)
	case s.TimeoutMS < 0:
		return invalid("negative timeout")
	}
	if s.Concurrency == "" {
		s.Concurrency = ConcurrencyQueue
	}
	if !slices.Contains(Concurrencies, s.Concurrency) {
		return invalid("concurrency %q (want queue, parallel or skip)", s.Concurrency)
	}
	inputs := map[string]bool{}
	for _, in := range s.Inputs {
		if !inputPattern.MatchString(in.Name) {
			return invalid("input %q: use letters, digits, '_' and '-'", in.Name)
		}
		if len(in.Default) > MaxInputBytes {
			return invalid("input %s: its default is longer than %d bytes", in.Name, MaxInputBytes)
		}
		inputs[in.Name] = true
	}
	steps := map[string]*StepSpec{}
	for i := range s.Steps {
		st := &s.Steps[i]
		if !idPattern.MatchString(st.ID) {
			return invalid("step %d: id %q: use letters, digits, '.', '_' and '-' (at most 64)", i+1, st.ID)
		}
		if steps[st.ID] != nil {
			return invalid("two steps are called %q", st.ID)
		}
		steps[st.ID] = st
	}
	check := &refChecker{steps: steps, inputs: inputs}
	if err := check.template(s.Environment, "environment", ""); err != nil {
		return err
	}
	for i := range s.Triggers {
		if err := validateTrigger(&s.Triggers[i], check); err != nil {
			return fmt.Errorf("trigger %d: %w", i+1, err)
		}
	}
	for i := range s.Steps {
		if err := validateStep(&s.Steps[i], check); err != nil {
			return fmt.Errorf("step %s: %w", s.Steps[i].ID, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.Outputs)) {
		if !inputPattern.MatchString(name) {
			return invalid("output %q: use letters, digits, '_' and '-'", name)
		}
		if err := check.template(s.Outputs[name], "output "+name, ""); err != nil {
			return err
		}
	}
	return cycle(s.Steps)
}

func validateTrigger(t *TriggerSpec, check *refChecker) error {
	set := 0
	for _, v := range []bool{t.Event != "", t.Schedule != "", t.EveryMS > 0, t.Git != "", t.Webhook != ""} {
		if v {
			set++
		}
	}
	if set != 1 {
		return invalid("say exactly one of event, schedule, every, git and webhook")
	}
	switch t.Type() {
	case TriggerEvent:
		if strings.ContainsAny(t.Event, " \t*") {
			return invalid("event %q: a type (agent.state) or a prefix (branch.)", t.Event)
		}
		for k, v := range t.Match {
			if k == "" || strings.Contains(k, "..") {
				return invalid("match key %q", k)
			}
			if _, err := path.Match(v, ""); err != nil {
				return invalid("match %s: %q is not a pattern", k, v)
			}
		}
	case TriggerSchedule:
		if _, err := ParseSchedule(t.Schedule); err != nil {
			return invalid("%v", err)
		}
	case TriggerGit:
		if t.Git != GitPush && t.Git != GitBranch {
			return invalid("git %q (want push or branch)", t.Git)
		}
		if t.Remote != "" && t.Git != GitPush {
			return invalid("remote is for git = \"push\"")
		}
		for _, b := range t.Branches {
			if _, err := path.Match(b, ""); err != nil || b == "" {
				return invalid("branches: %q is not a pattern", b)
			}
		}
	case TriggerWebhook:
		if !idPattern.MatchString(t.Webhook) {
			return invalid("webhook %q: use letters, digits, '.', '_' and '-'", t.Webhook)
		}
	}
	if t.Type() != TriggerEvent && len(t.Match) > 0 {
		return invalid("match is for event triggers")
	}
	if t.Type() != TriggerGit && (t.Repo != "" || len(t.Branches) > 0) {
		return invalid("repo and branches are for git triggers")
	}
	for _, name := range slices.Sorted(maps.Keys(t.Inputs)) {
		if !check.inputs[name] {
			return invalid("inputs: the workflow has no input %q (declare it under [inputs.%s])", name, name)
		}
		if err := check.template(t.Inputs[name], "input "+name, ""); err != nil {
			return err
		}
	}
	return nil
}

func validateStep(st *StepSpec, check *refChecker) error {
	kinds := 0
	for _, v := range []bool{len(st.Agent) > 0, len(st.Run) > 0 || st.Shell != "", st.Action != ""} {
		if v {
			kinds++
		}
	}
	if kinds != 1 {
		return invalid("say exactly one of agent, run and action")
	}
	switch {
	case st.Retries < 0 || st.Retries > MaxRetries:
		return invalid("retries %d (0 to %d)", st.Retries, MaxRetries)
	case st.TimeoutMS < 0 || st.BackoffMS < 0:
		return invalid("negative duration")
	case st.OnFailure != "" && st.OnFailure != OnFailureFail && st.OnFailure != OnFailureContinue:
		return invalid("on_failure %q (want fail or continue)", st.OnFailure)
	case st.Repeat != nil && (st.Foreach != nil || st.ForeachExpr != ""):
		return invalid("say foreach or repeat, not both")
	case len(st.Foreach) > MaxForeach:
		return invalid("foreach has %d items (at most %d)", len(st.Foreach), MaxForeach)
	}
	agentOnly := st.Prompt != "" || st.Worktree != "" || st.Isolate || st.Base != "" || st.Until != "" ||
		st.Match != "" || len(st.Check) > 0 || st.Machine != "" || len(st.Labels) > 0 || st.KeepAgent || st.NoSummary
	switch st.Kind() {
	case KindAgent:
		for _, k := range st.Agent {
			if k == AnyAgent && len(st.Agent) > 1 {
				return invalid("agent \"any\" stands alone")
			}
			if !idPattern.MatchString(k) {
				return invalid("agent %q is not a kind", k)
			}
		}
		if strings.TrimSpace(st.Prompt) == "" {
			return invalid("an agent step needs a prompt")
		}
		if len(st.Prompt) > MaxPromptBytes {
			return invalid("the prompt is longer than %d bytes", MaxPromptBytes)
		}
		if len(st.Check) > 0 && st.Until == "" {
			st.Until = "command"
		}
		if st.Until != "" && !slices.Contains(untils, st.Until) {
			return invalid("until %q (want done, idle, match or command)", st.Until)
		}
		if st.Until == "match" {
			if _, err := regexp.Compile(st.Match); err != nil || st.Match == "" {
				return invalid("until = \"match\" needs a match that compiles")
			}
		}
		if st.Until == "command" && len(st.Check) == 0 {
			return invalid("until = \"command\" needs check")
		}
		if len(st.Run) > 0 || st.Dir != "" || len(st.Env) > 0 || len(st.With) > 0 {
			return invalid("run, dir, env and with are not for agent steps")
		}
	case KindCommand:
		if agentOnly || len(st.With) > 0 {
			return invalid("an agent's settings (prompt, isolate, until, …) and with are not for commands")
		}
	case KindAction:
		plugin, action, ok := strings.Cut(st.Action, "/")
		if !ok || plugin == "" || action == "" {
			return invalid("action %q: write plugin/action", st.Action)
		}
		if agentOnly || st.Dir != "" || len(st.Env) > 0 {
			return invalid("an agent's settings, dir and env are not for actions")
		}
	}
	if st.If != "" {
		if err := check.expr(st.If, "if", st.ID); err != nil {
			return err
		}
	}
	if st.ForeachExpr != "" {
		if err := check.expr(st.ForeachExpr, "foreach", st.ID); err != nil {
			return err
		}
	}
	if st.Repeat != nil {
		if st.Repeat.Max < 1 || st.Repeat.Max > MaxRepeat {
			return invalid("repeat max %d (1 to %d)", st.Repeat.Max, MaxRepeat)
		}
		if st.Repeat.Until == "" {
			return invalid("repeat needs until")
		}
		if err := check.expr(st.Repeat.Until, "repeat until", ""); err != nil {
			return err
		}
	}
	texts := map[string]string{
		"prompt": st.Prompt, "environment": st.Environment, "worktree": st.Worktree, "base": st.Base,
		"shell": st.Shell, "dir": st.Dir,
	}
	for i, a := range st.Run {
		texts[fmt.Sprintf("run[%d]", i)] = a
	}
	for k, v := range st.Env {
		if !inputPattern.MatchString(k) {
			return invalid("env %q is not a variable name", k)
		}
		texts["env "+k] = v
	}
	for k, v := range st.With {
		texts["with "+k] = v
	}
	for _, what := range slices.Sorted(maps.Keys(texts)) {
		if err := check.template(texts[what], what, st.ID); err != nil {
			return err
		}
	}
	for _, n := range st.Needs {
		if check.steps[n] == nil {
			return invalid("needs %q, which is not a step", n)
		}
		if n == st.ID {
			return invalid("needs itself")
		}
	}
	return nil
}

// refChecker checks the steps and inputs expressions name.
type refChecker struct {
	steps  map[string]*StepSpec
	inputs map[string]bool
}

func (c *refChecker) expr(src, what, self string) error {
	e, err := ParseExpr(src)
	if err != nil {
		return invalid("%s: %v", what, err)
	}
	return c.refs(e, what, self)
}

func (c *refChecker) template(src, what, self string) error {
	if !strings.Contains(src, "${{") {
		return nil
	}
	t, err := ParseTemplate(src)
	if err != nil {
		return invalid("%s: %v", what, err)
	}
	for _, p := range t.parts {
		if p.expr != nil {
			if err := c.refs(p.expr, what, self); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *refChecker) refs(e *Expr, what, self string) error {
	steps, inputs := e.refs()
	for _, s := range steps {
		if c.steps[s] == nil {
			return invalid("%s: no step %q", what, s)
		}
		if s == self && what != "repeat until" {
			return invalid("%s: a step cannot read its own results (repeat's until can)", what)
		}
	}
	for _, in := range inputs {
		if !c.inputs[in] {
			return invalid("%s: no input %q (declare it under [inputs.%s])", what, in, in)
		}
	}
	return nil
}

// cycle reports a cycle in steps' needs, with its path.
func cycle(steps []StepSpec) error {
	byID := map[string]*StepSpec{}
	for i := range steps {
		byID[steps[i].ID] = &steps[i]
	}
	const (
		unseen = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case visiting:
			i := slices.Index(stack, id)
			return invalid("a cycle: %s", strings.Join(append(slices.Clone(stack[i:]), id), " → "))
		case done:
			return nil
		}
		state[id] = visiting
		stack = append(stack, id)
		for _, n := range byID[id].Needs {
			if err := visit(n); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		return nil
	}
	for _, s := range steps {
		if err := visit(s.ID); err != nil {
			return err
		}
	}
	return nil
}

// describe says what a trigger fires on, in a few words.
func describe(t TriggerSpec) TriggerInfo {
	info := TriggerInfo{Type: t.Type()}
	switch info.Type {
	case TriggerEvent:
		d := t.Event
		if len(t.Match) > 0 {
			var parts []string
			for _, k := range slices.Sorted(maps.Keys(t.Match)) {
				parts = append(parts, k+"="+t.Match[k])
			}
			d += " where " + strings.Join(parts, ", ")
		}
		info.Description = d
	case TriggerSchedule:
		info.Description = t.Schedule
	case TriggerEvery:
		info.Description = "every " + t.Every().String()
	case TriggerGit:
		d := "git " + t.Git
		if len(t.Branches) > 0 {
			d += " to " + strings.Join(t.Branches, ", ")
		}
		if t.Repo != "" {
			d += " in " + t.Repo
		}
		info.Description = d
	case TriggerWebhook:
		info.Description = "webhook " + t.Webhook
	}
	return info
}
