package task

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/admirable-oss/hive/internal/git"
)

// Plan files are TOML:
//
//	name = "session refactor"
//	max_parallel = 4          # tasks of this plan running at once
//	timeout = "2h"            # the plan's budget
//
//	[kinds]                   # per agent kind, tasks running at once
//	claude = 2
//
//	[defaults]                # anything a [[task]] may say
//	kind = "claude"
//	environment = "api"
//	isolate = true
//	timeout = "30m"
//
//	[[task]]
//	id = "schema"
//	prompt = "Design the new session schema in docs/session.md."
//
//	[[task]]
//	id = "api"
//	needs = ["schema"]
//	approve = true
//	prompt = "Implement docs/session.md."
//	until = "command"
//	command = ["go", "test", "./..."]
//
// A boolean in [defaults] that is true holds for every task.

// fileTask is a [[task]] table: a Spec whose timeout is a duration.
type fileTask struct {
	Spec
	Timeout string `toml:"timeout"`
}

type planFile struct {
	Name        string         `toml:"name"`
	MaxParallel int            `toml:"max_parallel"`
	Timeout     string         `toml:"timeout"`
	Kinds       map[string]int `toml:"kinds"`
	Defaults    *fileTask      `toml:"defaults"`
	Tasks       []fileTask     `toml:"task"`
}

// MaxPlanBytes bounds a plan file.
const MaxPlanBytes = 4 << 20

// ParsePlan reads a plan file. source names it in errors. The plan is
// checked as Validate checks it, so a plan that parses also runs.
func ParsePlan(data []byte, source string) (PlanSpec, error) {
	if len(data) > MaxPlanBytes {
		return PlanSpec{}, fmt.Errorf("%w: %s: larger than %d bytes", ErrInvalid, source, MaxPlanBytes)
	}
	var f planFile
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		if se, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return PlanSpec{}, fmt.Errorf("%w: %s: %s", ErrInvalid, source, strings.TrimSpace(se.String()))
		}
		if de, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, col := de.Position()
			return PlanSpec{}, fmt.Errorf("%w: %s:%d:%d: %s", ErrInvalid, source, row, col, de.Error())
		}
		return PlanSpec{}, fmt.Errorf("%w: %s: %w", ErrInvalid, source, err)
	}
	spec := PlanSpec{Name: f.Name, MaxParallel: f.MaxParallel, Kinds: f.Kinds}
	var err error
	if spec.TimeoutMS, err = durationMS(f.Timeout, "timeout"); err != nil {
		return PlanSpec{}, fmt.Errorf("%s: %w", source, err)
	}
	if f.Defaults != nil {
		d, err := f.Defaults.spec("[defaults]")
		if err != nil {
			return PlanSpec{}, fmt.Errorf("%s: %w", source, err)
		}
		spec.Defaults = &d
	}
	for i := range f.Tasks {
		s, err := f.Tasks[i].spec(fmt.Sprintf("task %d (%s)", i+1, f.Tasks[i].ID))
		if err != nil {
			return PlanSpec{}, fmt.Errorf("%s: %w", source, err)
		}
		spec.Tasks = append(spec.Tasks, s)
	}
	if _, err := Validate(spec); err != nil {
		return PlanSpec{}, fmt.Errorf("%s: %w", source, err)
	}
	return spec, nil
}

func (t fileTask) spec(where string) (Spec, error) {
	s := t.Spec
	var err error
	if s.TimeoutMS, err = durationMS(t.Timeout, where+" timeout"); err != nil {
		return Spec{}, err
	}
	return s, nil
}

func durationMS(s, what string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%w: %s %q is not a duration (30m, 2h)", ErrInvalid, what, s)
	}
	return d.Milliseconds(), nil
}

// idPattern is what task IDs, and plan IDs, look like: they become parts of
// branch names and paths.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// labelPattern is what machine labels look like.
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,63}$`)

// MaxRetries bounds a task's automatic retries.
const MaxRetries = 10

// Validate fills in each task from the plan's defaults and checks the
// plan: task IDs, prompts, conditions, and that needs form a DAG. It
// returns the filled-in tasks in an order where every task comes after
// what it needs (ties keep the written order).
func Validate(p PlanSpec) ([]Spec, error) {
	switch {
	case len(p.Tasks) == 0:
		return nil, fmt.Errorf("%w: the plan has no tasks", ErrInvalid)
	case len(p.Tasks) > MaxTasks:
		return nil, fmt.Errorf("%w: the plan has %d tasks, more than %d", ErrInvalid, len(p.Tasks), MaxTasks)
	case p.MaxParallel < 0:
		return nil, fmt.Errorf("%w: max_parallel is negative", ErrInvalid)
	case p.TimeoutMS < 0:
		return nil, fmt.Errorf("%w: timeout is negative", ErrInvalid)
	}
	for kind, n := range p.Kinds {
		if n < 1 {
			return nil, fmt.Errorf("%w: kinds.%s is %d; give at least 1", ErrInvalid, kind, n)
		}
	}
	tasks := make([]Spec, len(p.Tasks))
	index := make(map[string]int, len(p.Tasks))
	for i, t := range p.Tasks {
		if p.Defaults != nil {
			t = withDefaults(t, *p.Defaults)
		}
		if err := check(t); err != nil {
			return nil, err
		}
		if _, dup := index[t.ID]; dup {
			return nil, fmt.Errorf("%w: two tasks are called %q", ErrInvalid, t.ID)
		}
		index[t.ID] = i
		tasks[i] = t
	}
	for _, t := range tasks {
		for _, n := range t.Needs {
			if _, ok := index[n]; !ok {
				return nil, fmt.Errorf("%w: task %q needs %q, which is not in the plan", ErrInvalid, t.ID, n)
			}
			if n == t.ID {
				return nil, fmt.Errorf("%w: task %q needs itself", ErrInvalid, t.ID)
			}
		}
	}
	order, err := topoOrder(tasks, index)
	if err != nil {
		return nil, err
	}
	out := make([]Spec, len(order))
	for i, j := range order {
		out[i] = tasks[j]
	}
	return out, nil
}

// withDefaults fills in what t leaves out from d. A task's own ID, title,
// prompt and needs are never defaulted; env maps merge, the task winning.
func withDefaults(t, d Spec) Spec {
	str := func(v *string, def string) {
		if *v == "" {
			*v = def
		}
	}
	str(&t.Kind, d.Kind)
	str(&t.EnvironmentID, d.EnvironmentID)
	str(&t.Worktree, d.Worktree)
	str(&t.Base, d.Base)
	str(&t.Match, d.Match)
	str(&t.Machine, d.Machine)
	str((*string)(&t.Until), string(d.Until))
	if t.Args == nil {
		t.Args = slices.Clone(d.Args)
	}
	if t.Command == nil {
		t.Command = slices.Clone(d.Command)
	}
	if t.Labels == nil {
		t.Labels = slices.Clone(d.Labels)
	}
	if len(d.Env) > 0 {
		env := maps.Clone(d.Env)
		maps.Copy(env, t.Env)
		t.Env = env
	}
	if t.TimeoutMS == 0 {
		t.TimeoutMS = d.TimeoutMS
	}
	if t.Retries == 0 {
		t.Retries = d.Retries
	}
	t.Isolate = t.Isolate || d.Isolate
	t.Approve = t.Approve || d.Approve
	t.NoSummary = t.NoSummary || d.NoSummary
	t.KeepAgent = t.KeepAgent || d.KeepAgent
	return t
}

// check validates one filled-in task.
func check(t Spec) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: task %q: %s", ErrInvalid, t.ID, fmt.Sprintf(format, args...))
	}
	switch {
	case !idPattern.MatchString(t.ID):
		return fmt.Errorf("%w: task ID %q (letters, digits, '.', '_' and '-', up to 64 characters)", ErrInvalid, t.ID)
	case strings.TrimSpace(t.Prompt) == "":
		return bad("it has no prompt")
	case len(t.Prompt) > MaxPromptBytes:
		return bad("its prompt is longer than %d bytes", MaxPromptBytes)
	case t.Kind == "":
		return bad("it names no agent kind (kind = \"claude\")")
	case t.EnvironmentID == "":
		return bad("it names no environment (environment = \"…\")")
	case t.TimeoutMS < 0:
		return bad("its timeout is negative")
	case t.Retries < 0 || t.Retries > MaxRetries:
		return bad("retries must be 0 to %d", MaxRetries)
	case t.Until != "" && !slices.Contains(Untils, t.Until):
		return bad("until %q (want one of %v)", t.Until, Untils)
	case t.Until == UntilMatch && t.Match == "":
		return bad("until = \"match\" needs match = \"<regular expression>\"")
	case t.Until == UntilCommand && len(t.Command) == 0:
		return bad("until = \"command\" needs command = [\"…\"]")
	case t.Machine != "" && !idPattern.MatchString(t.Machine):
		return bad("machine %q", t.Machine)
	}
	if t.Match != "" {
		if _, err := regexp.Compile(t.Match); err != nil {
			return bad("match: %v", err)
		}
	}
	if t.Worktree != "" {
		if err := git.ValidateBranch(t.Worktree); err != nil {
			return bad("worktree: %v", err)
		}
	}
	for _, l := range t.Labels {
		if !labelPattern.MatchString(l) {
			return bad("label %q", l)
		}
	}
	return nil
}

// topoOrder orders tasks so each comes after what it needs, keeping the
// written order among tasks that are ready together (Kahn's algorithm over
// the written order). A cycle is reported with its path.
func topoOrder(tasks []Spec, index map[string]int) ([]int, error) {
	indegree := make([]int, len(tasks))
	dependents := make([][]int, len(tasks))
	for i, t := range tasks {
		for _, n := range t.Needs {
			j := index[n]
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}
	order := make([]int, 0, len(tasks))
	ready := make([]int, 0, len(tasks))
	for i := range tasks {
		if indegree[i] == 0 {
			ready = append(ready, i)
		}
	}
	for len(ready) > 0 {
		// The lowest index first: the written order.
		k := slices.Index(ready, slices.Min(ready))
		i := ready[k]
		ready = slices.Delete(ready, k, k+1)
		order = append(order, i)
		for _, d := range dependents[i] {
			if indegree[d]--; indegree[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	if len(order) == len(tasks) {
		return order, nil
	}
	return nil, fmt.Errorf("%w: needs form a cycle: %s", ErrInvalid, strings.Join(cycle(tasks, index, indegree), " → "))
}

// cycle finds one cycle among the tasks Kahn's algorithm left with
// dependencies, as task IDs from a task back to itself.
func cycle(tasks []Spec, index map[string]int, indegree []int) []string {
	start := slices.IndexFunc(indegree, func(n int) bool { return n > 0 })
	// Walk needs from a stuck task: every stuck task needs a stuck task, so
	// the walk revisits one, and that stretch is a cycle.
	seen := map[int]int{}
	var path []int
	for i := start; ; {
		if at, ok := seen[i]; ok {
			ids := make([]string, 0, len(path)-at+1)
			for _, j := range path[at:] {
				ids = append(ids, tasks[j].ID)
			}
			return append(ids, tasks[i].ID)
		}
		seen[i] = len(path)
		path = append(path, i)
		for _, n := range tasks[i].Needs {
			if j := index[n]; indegree[j] > 0 {
				i = j
				break
			}
		}
	}
}
