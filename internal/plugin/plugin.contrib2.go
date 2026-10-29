package plugin

// Plugin API 2 contributions: pane views, forges, issue trackers, workflow
// steps and workflow triggers. Views draw from documents the plugin sets;
// forges, trackers and steps are commands Hive calls with one JSON
// document on stdin, reading one from stdout (a typed call).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Plugin API 2 contribution kinds.
const (
	ContribView    = "view"
	ContribForge   = "forge"
	ContribTracker = "tracker"
	ContribStep    = "step"
	ContribTrigger = "trigger"
)

// ContributionKindsV2 are plugin API 2's contribution kinds.
var ContributionKindsV2 = append(slices.Clone(ContributionKinds), ContribView, ContribForge, ContribTracker, ContribStep, ContribTrigger)

// ContributionKindsFor are the contribution kinds of a plugin API version.
func ContributionKindsFor(api int) []string {
	if api >= 2 {
		return ContributionKindsV2
	}
	return ContributionKinds
}

// Bounds on plugin API 2 contributions.
const (
	MaxViews    = 8
	MaxForges   = 4
	MaxTrackers = 4
	MaxSteps    = 32
	MaxTriggers = 16
	MaxParams   = 32
	// MaxCallOutput bounds what a typed call writes to stdout.
	MaxCallOutput = 4 << 20
)

// ViewDecl is a pane view a plugin draws ([[contributes.views]]).
type ViewDecl struct {
	ID    string `toml:"id" json:"id"`
	Title string `toml:"title" json:"title"`
	// Command, when set, is run to draw the view when a pane opens it.
	Command []string `toml:"command" json:"command,omitempty"`
}

// ForgeDecl is a forge a plugin provides ([[contributes.forges]]): the
// branch.Forge operations, as a command.
type ForgeDecl struct {
	Name    string   `toml:"name" json:"name"`
	Hosts   []string `toml:"hosts" json:"hosts"`
	Command []string `toml:"command" json:"command"`
}

// TrackerDecl is an issue tracker a plugin provides
// ([[contributes.trackers]]).
type TrackerDecl struct {
	Name    string   `toml:"name" json:"name"`
	Command []string `toml:"command" json:"command"`
}

// ParamSpec is a step's typed input or output.
type ParamSpec struct {
	Type        string `toml:"type" json:"type"` // string, boolean, integer, number
	Required    bool   `toml:"required" json:"required,omitempty"`
	Default     any    `toml:"default" json:"default,omitempty"`
	Description string `toml:"description" json:"description,omitempty"`
}

// StepDecl is a workflow step a plugin provides ([[contributes.steps]]).
type StepDecl struct {
	Name        string               `toml:"name" json:"name"`
	Description string               `toml:"description" json:"description,omitempty"`
	Command     []string             `toml:"command" json:"command"`
	Timeout     string               `toml:"timeout" json:"timeout,omitempty"`
	Inputs      map[string]ParamSpec `toml:"inputs" json:"inputs,omitempty"`
	Outputs     map[string]ParamSpec `toml:"outputs" json:"outputs,omitempty"`
}

// StepTimeout is the step's timeout (default 10 minutes).
func (s StepDecl) StepTimeout() time.Duration {
	if d, err := time.ParseDuration(s.Timeout); err == nil && d > 0 {
		return d
	}
	return 10 * time.Minute
}

// TriggerDecl is a workflow trigger a plugin fires
// ([[contributes.triggers]]).
type TriggerDecl struct {
	Name        string `toml:"name" json:"name"`
	Description string `toml:"description" json:"description,omitempty"`
}

// hasV2Contributions reports any plugin API 2 contribution.
func (c Contributes) hasV2Contributions() bool {
	return len(c.Views)+len(c.Forges)+len(c.Trackers)+len(c.Steps)+len(c.Triggers) > 0
}

// runsCommands reports a plugin API 2 contribution Hive runs a command
// for: a forge, a tracker, a step, or a view drawn on open.
func (c Contributes) runsCommands() bool {
	return len(c.Forges)+len(c.Trackers)+len(c.Steps) > 0 ||
		slices.ContainsFunc(c.Views, func(v ViewDecl) bool { return len(v.Command) > 0 })
}

// checkV2Contributions checks views, forges, trackers, steps and
// triggers: plugin API 2 only, bounded, named, and each with what it
// needs.
func (m *Manifest) checkV2Contributions(requested map[string]bool) error {
	c := &m.Contributes
	if !c.hasV2Contributions() {
		return nil
	}
	if m.EffectiveAPI() < 2 {
		return errors.New("[[contributes.views]], forges, trackers, steps and triggers need api = 2")
	}
	switch {
	case len(c.Views) > MaxViews:
		return fmt.Errorf("contributes.views: at most %d", MaxViews)
	case len(c.Forges) > MaxForges:
		return fmt.Errorf("contributes.forges: at most %d", MaxForges)
	case len(c.Trackers) > MaxTrackers:
		return fmt.Errorf("contributes.trackers: at most %d", MaxTrackers)
	case len(c.Steps) > MaxSteps:
		return fmt.Errorf("contributes.steps: at most %d", MaxSteps)
	case len(c.Triggers) > MaxTriggers:
		return fmt.Errorf("contributes.triggers: at most %d", MaxTriggers)
	}
	names := map[string]bool{}
	name := func(kind, n string) error {
		if !idPattern.MatchString(n) {
			return fmt.Errorf("contributes.%s: %q: lower-case letters, digits and dashes", kind, n)
		}
		if names[kind+"/"+n] {
			return fmt.Errorf("contributes.%s: %q twice", kind, n)
		}
		names[kind+"/"+n] = true
		return nil
	}
	command := func(kind, n string, argv []string, required bool) error {
		if len(argv) == 0 || argv[0] == "" {
			if required {
				return fmt.Errorf("contributes.%s %s: command is empty", kind, n)
			}
			return nil
		}
		if m.Wasm() != IsWasmCommand(argv) {
			return fmt.Errorf("contributes.%s %s: a wasm plugin's commands are its modules, and only a wasm plugin's", kind, n)
		}
		return nil
	}
	for i := range c.Views {
		v := &c.Views[i]
		if err := name("views", v.ID); err != nil {
			return err
		}
		if v.Title == "" {
			v.Title = v.ID
		}
		if utf8.RuneCountInString(v.Title) > MaxTitle {
			return fmt.Errorf("contributes.views %s: title of at most %d characters", v.ID, MaxTitle)
		}
		if err := command("views", v.ID, v.Command, false); err != nil {
			return err
		}
	}
	needNetwork := func(kind, n string) error {
		if m.Wasm() {
			return fmt.Errorf("contributes.%s %s: a wasm plugin has no network", kind, n)
		}
		if !requested[CapNetwork] {
			return fmt.Errorf("contributes.%s %s: request network, to reach it", kind, n)
		}
		return nil
	}
	for _, f := range c.Forges {
		if err := name("forges", f.Name); err != nil {
			return err
		}
		if err := command("forges", f.Name, f.Command, true); err != nil {
			return err
		}
		if len(f.Hosts) == 0 {
			return fmt.Errorf("contributes.forges %s: list the hosts it serves", f.Name)
		}
		for _, h := range f.Hosts {
			if _, _, err := ParseHostPattern(h + ":443"); err != nil || strings.Contains(h, ":") {
				return fmt.Errorf("contributes.forges %s: host %q: a host name, or *.domain", f.Name, h)
			}
		}
		if err := needNetwork("forges", f.Name); err != nil {
			return err
		}
		if err := m.checkForgeHosts(f); err != nil {
			return err
		}
	}
	for _, t := range c.Trackers {
		if err := name("trackers", t.Name); err != nil {
			return err
		}
		if t.Name == "github" {
			return errors.New("contributes.trackers: github is built in")
		}
		if err := command("trackers", t.Name, t.Command, true); err != nil {
			return err
		}
		if err := needNetwork("trackers", t.Name); err != nil {
			return err
		}
	}
	for _, s := range c.Steps {
		if err := name("steps", s.Name); err != nil {
			return err
		}
		if err := command("steps", s.Name, s.Command, true); err != nil {
			return err
		}
		if s.Timeout != "" {
			if d, err := time.ParseDuration(s.Timeout); err != nil || d <= 0 || d > 24*time.Hour {
				return fmt.Errorf("contributes.steps %s: timeout %q: up to 24h", s.Name, s.Timeout)
			}
		}
		for kind, params := range map[string]map[string]ParamSpec{"inputs": s.Inputs, "outputs": s.Outputs} {
			if len(params) > MaxParams {
				return fmt.Errorf("contributes.steps %s: at most %d %s", s.Name, MaxParams, kind)
			}
			for p, spec := range params {
				if !settingNamePattern.MatchString(p) {
					return fmt.Errorf("contributes.steps %s: %s %q: lower-case letters, digits and underscores", s.Name, kind, p)
				}
				if !slices.Contains(paramTypes, spec.Type) {
					return fmt.Errorf("contributes.steps %s: %s %s: type %q (want %s)", s.Name, kind, p, spec.Type, strings.Join(paramTypes, ", "))
				}
				if spec.Default != nil && !validSettingValue(spec.Type, spec.Default) {
					return fmt.Errorf("contributes.steps %s: %s %s: default is not a %s", s.Name, kind, p, spec.Type)
				}
			}
		}
	}
	for _, t := range c.Triggers {
		if err := name("triggers", t.Name); err != nil {
			return err
		}
	}
	return nil
}

var paramTypes = []string{"string", "boolean", "integer", "number"}

// CheckParams checks values against typed params: required ones present,
// no unknown ones, each of its type; defaults fill what is missing.
func CheckParams(specs map[string]ParamSpec, values map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range values {
		spec, ok := specs[k]
		if !ok {
			return nil, fmt.Errorf("unknown %q (have %s)", k, strings.Join(sortedKeys(specs), ", "))
		}
		if !validSettingValue(spec.Type, v) {
			return nil, fmt.Errorf("%s: want a %s", k, spec.Type)
		}
		out[k] = v
	}
	for k, spec := range specs {
		if _, ok := out[k]; ok {
			continue
		}
		switch {
		case spec.Default != nil:
			out[k] = spec.Default
		case spec.Required:
			return nil, fmt.Errorf("%s is required", k)
		}
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ErrCall is a typed call that failed: the command failed or answered
// something that is not one JSON document.
var ErrCall = errors.New("plugin call failed")

// Invoke runs one of a plugin's commands as a typed call: input as JSON on
// stdin, one JSON document read from stdout. Its stderr goes to the
// plugin's log. kind names the call in the context (forge, tracker,
// step, view).
func (h *Host) Invoke(ctx context.Context, id, kind string, argv []string, input any, timeout time.Duration) (json.RawMessage, error) {
	h.mu.Lock()
	l := h.plugins[id]
	h.mu.Unlock()
	switch {
	case l == nil:
		return nil, fmt.Errorf("%w %q", ErrNotFound, id)
	case l.problem != "":
		return nil, fmt.Errorf("plugin %s cannot run: %s", id, l.problem)
	}
	in, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stdout bytes.Buffer
	out := &limitedWriter{w: &stdout, limit: MaxCallOutput}
	tail := &tailBuffer{}
	code, err := h.runCommand(cctx, l, argv, Context{Trigger: kind}, runOptions{stdin: in, stdout: out, also: tail})
	switch {
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("%w: %s %s timed out after %s", ErrCall, id, kind, timeout)
	case out.over:
		return nil, fmt.Errorf("%w: %s %s wrote more than %d bytes", ErrCall, id, kind, MaxCallOutput)
	case err != nil || code != 0:
		msg := strings.TrimSpace(lastLine(tail.String()))
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%w: %s %s: %s", ErrCall, id, kind, msg)
	}
	raw := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%w: %s %s did not answer one JSON document", ErrCall, id, kind)
	}
	return json.RawMessage(raw), nil
}

// limitedWriter keeps at most limit bytes, and says when more came.
type limitedWriter struct {
	w     io.Writer
	limit int
	n     int
	over  bool
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n+len(p) > l.limit {
		l.over = true
		return len(p), nil
	}
	l.n += len(p)
	_, _ = l.w.Write(p)
	return len(p), nil
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// StepCall is a workflow step a plugin runs: where it comes from and its
// inputs, as the workflow resolved them.
type StepCall struct {
	Plugin, Step string
	// With are the resolved inputs, as text; RunStep reads them back by
	// the step's types.
	With                  map[string]string
	Workflow, Run, StepID string
	Environment           string
}

// stepInput is what a step's command reads on stdin.
type stepInput struct {
	Step        string         `json:"step"`
	Inputs      map[string]any `json:"inputs"`
	Workflow    stepWhere      `json:"workflow"`
	Environment string         `json:"environment,omitempty"`
}

type stepWhere struct {
	Workflow string `json:"workflow"`
	Run      string `json:"run"`
	Step     string `json:"step"`
}

// stepOutput is what a step's command answers on stdout.
type stepOutput struct {
	Outputs map[string]any `json:"outputs"`
	Summary string         `json:"summary,omitempty"`
}

// Step returns a loaded plugin's step declaration.
func (h *Host) Step(plugin, step string) (StepDecl, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.plugins[plugin]
	if l == nil || l.Manifest == nil {
		return StepDecl{}, false
	}
	i := slices.IndexFunc(l.Manifest.Contributes.Steps, func(s StepDecl) bool { return s.Name == step })
	if i < 0 {
		return StepDecl{}, false
	}
	return l.Manifest.Contributes.Steps[i], true
}

// DeclaresTrigger reports whether a loaded plugin declares a workflow
// trigger.
func (h *Host) DeclaresTrigger(plugin, trigger string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.plugins[plugin]
	return l != nil && l.Manifest != nil && slices.ContainsFunc(l.Manifest.Contributes.Triggers, func(t TriggerDecl) bool { return t.Name == trigger })
}

// typedInputs reads a step's inputs, given as text, by their declared
// types and checks them; a value that is still a template (`${{ … }}`)
// is only checked for its name.
func typedInputs(decl StepDecl, plugin string, with map[string]string, templates bool) (map[string]any, error) {
	typed := make(map[string]any, len(with))
	for k, v := range with {
		spec, ok := decl.Inputs[k]
		switch {
		case !ok:
			typed[k] = v // CheckParams names it
			continue
		case templates && strings.Contains(v, "${{"):
			typed[k] = zeroOf(spec.Type) // its value is known when it runs
			continue
		}
		val, err := ParseSetting(spec.Type, v)
		if err != nil {
			return nil, fmt.Errorf("step %s/%s: input %s: %w", plugin, decl.Name, k, err)
		}
		typed[k] = val
	}
	inputs, err := CheckParams(decl.Inputs, typed)
	if err != nil {
		return nil, fmt.Errorf("step %s/%s: %w", plugin, decl.Name, err)
	}
	return inputs, nil
}

func zeroOf(typ string) any {
	switch typ {
	case "boolean":
		return false
	case "integer":
		return int64(0)
	case "number":
		return 0.0
	}
	return ""
}

// CheckStep checks a workflow's use of a plugin's step before it runs:
// the plugin is loaded, declares the step, and the inputs fit their
// types (those that are not templates).
func (h *Host) CheckStep(plugin, step string, with map[string]string) error {
	decl, ok := h.Step(plugin, step)
	if !ok {
		return fmt.Errorf("%w: no plugin step %s/%s (an installed plugin's [[contributes.steps]])", ErrNotFound, plugin, step)
	}
	_, err := typedInputs(decl, plugin, with, true)
	return err
}

// RunStep runs a plugin's workflow step: its inputs checked against their
// types, its outputs checked against theirs and returned as text, with
// its summary.
func (h *Host) RunStep(ctx context.Context, c StepCall) (map[string]string, string, error) {
	decl, ok := h.Step(c.Plugin, c.Step)
	if !ok {
		return nil, "", fmt.Errorf("%w: plugin %s has no step %q ([[contributes.steps]])", ErrNotFound, c.Plugin, c.Step)
	}
	inputs, err := typedInputs(decl, c.Plugin, c.With, false)
	if err != nil {
		return nil, "", err
	}
	raw, err := h.Invoke(ctx, c.Plugin, ContribStep, decl.Command, stepInput{
		Step: c.Step, Inputs: inputs, Environment: c.Environment,
		Workflow: stepWhere{Workflow: c.Workflow, Run: c.Run, Step: c.StepID},
	}, decl.StepTimeout())
	if err != nil {
		return nil, "", err
	}
	var out stepOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("%w: step %s/%s answered %v, not {\"outputs\": {…}}", ErrCall, c.Plugin, c.Step, err)
	}
	text := make(map[string]string, len(out.Outputs))
	for k, v := range out.Outputs {
		spec, ok := decl.Outputs[k]
		if !ok {
			return nil, out.Summary, fmt.Errorf("%w: step %s/%s answered output %q, which it does not declare", ErrCall, c.Plugin, c.Step, k)
		}
		if !validSettingValue(spec.Type, v) {
			return nil, out.Summary, fmt.Errorf("%w: step %s/%s output %s is not a %s", ErrCall, c.Plugin, c.Step, k, spec.Type)
		}
		if s, ok := v.(string); ok {
			text[k] = s
		} else {
			b, _ := json.Marshal(v)
			text[k] = string(b)
		}
	}
	return text, out.Summary, nil
}
