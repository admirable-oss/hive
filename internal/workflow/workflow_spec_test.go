package workflow_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/workflow"
)

// rfcExample is RFC 0004's example workflow.
const rfcExample = `
version = 1
name = "review-push"
description = "Review what was pushed, test it, and fix it if the tests fail."
timeout = "2h"
concurrency = "queue"
environment = "api"

[inputs.branch]
description = "the branch to review"
default = "main"

[[on]]
git = "push"
repo = "~/src/api"
branches = ["main", "release/*"]
[on.inputs]
branch = "${{ trigger.branch }}"

[[step]]
id = "review"
agent = ["claude", "codex"]
prompt = "Review the changes on ${{ inputs.branch }}. Write your findings to the summary."
isolate = true
timeout = "30m"

[[step]]
id = "test"
run = ["go", "test", "./..."]
timeout = "20m"
retries = 1
backoff = "10s"
on_failure = "continue"

[[step]]
id = "fix"
if = "steps.test.outcome == 'failure'"
agent = "any"
prompt = """
The tests fail:
${{ steps.test.output }}
Fix them."""

[[step]]
id = "retest"
if = "steps.fix.outcome == 'success'"
run = "go test ./..."

[outputs]
review = "${{ steps.review.summary }}"
`

func TestParseRFCExample(t *testing.T) {
	s, err := workflow.Parse([]byte(rfcExample), "review.toml")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "review-push" || s.TimeoutMS != 7_200_000 || s.Environment != "api" || len(s.Inputs) != 1 || s.Inputs[0].Default != "main" {
		t.Fatalf("the workflow: %+v", s)
	}
	if len(s.Triggers) != 1 || s.Triggers[0].Type() != workflow.TriggerGit || s.Triggers[0].Inputs["branch"] != "${{ trigger.branch }}" {
		t.Fatalf("its trigger: %+v", s.Triggers)
	}
	review, test, fix, retest := s.Steps[0], s.Steps[1], s.Steps[2], s.Steps[3]
	switch {
	case review.Kind() != workflow.KindAgent || !slices.Equal(review.Agent, []string{"claude", "codex"}) || !review.Isolate || len(review.Needs) != 0:
		t.Fatalf("review: %+v", review)
	case test.Kind() != workflow.KindCommand || !slices.Equal(test.Needs, []string{"review"}) || test.Retries != 1 || test.BackoffMS != 10_000 || !test.Continue():
		t.Fatalf("test: %+v", test)
	case fix.Kind() != workflow.KindAgent || fix.Agent[0] != workflow.AnyAgent || !slices.Equal(fix.Needs, []string{"test"}):
		t.Fatalf("fix: %+v", fix)
	case retest.Shell != "go test ./..." || len(retest.Run) != 0:
		t.Fatalf("retest runs a shell line: %+v", retest)
	}
}

func TestParseDefaults(t *testing.T) {
	s, err := workflow.Parse([]byte(`
version = 1
[[step]]
id = "a"
run = ["ok"]
[[step]]
id = "b"
needs = []
run = ["ok"]
[[step]]
id = "c"
needs = ["a", "b"]
run = ["ok"]
[[step]]
id = "d"
agent = "codex"
prompt = "go"
check = ["make", "test"]
[[on]]
event = "agent.blocked"
[[on]]
every = "15m"
`), "dir/nightly.toml")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "nightly" || s.Concurrency != workflow.ConcurrencyQueue {
		t.Fatalf("named after its file, queueing: %+v", s)
	}
	if s.Steps[1].Needs == nil || len(s.Steps[1].Needs) != 0 || !slices.Equal(s.Steps[3].Needs, []string{"c"}) {
		t.Fatalf("needs = [] starts at once; no needs follows the step before: %+v", s.Steps)
	}
	if s.Steps[3].Until != "command" {
		t.Fatalf("a check makes until = command: %+v", s.Steps[3])
	}
	if tr := s.Triggers[0]; tr.Event != "agent.state" || tr.Match["state"] != "blocked" {
		t.Fatalf("agent.blocked is agent.state in state blocked: %+v", tr)
	}
	if s.Triggers[1].EveryMS != 900_000 {
		t.Fatalf("every: %+v", s.Triggers[1])
	}
}

func TestParseRejects(t *testing.T) {
	step := "\n[[step]]\nid = \"a\"\nrun = [\"ok\"]\n"
	for want, text := range map[string]string{
		"version = 1 is required":         `name = "x"` + step,
		"format 2":                        "version = 2" + step,
		"no [[step]]":                     "version = 1",
		"unknown":                         "version = 1\nneedz = 1" + step,
		"two steps":                       "version = 1" + step + step,
		"exactly one of agent, run":       "version = 1\n[[step]]\nid = \"a\"\nrun = [\"ok\"]\naction = \"p/a\"",
		"needs a prompt":                  "version = 1\n[[step]]\nid = \"a\"\nagent = \"codex\"",
		"not a step":                      "version = 1\n[[step]]\nid = \"a\"\nneeds = [\"b\"]\nrun = [\"ok\"]",
		"a cycle: a → b → a":              "version = 1\n[[step]]\nid = \"a\"\nneeds = [\"b\"]\nrun = [\"ok\"]\n[[step]]\nid = \"b\"\nneeds = [\"a\"]\nrun = [\"ok\"]",
		"needs itself":                    "version = 1\n[[step]]\nid = \"a\"\nneeds = [\"a\"]\nrun = [\"ok\"]",
		"no step \"zz\"":                  "version = 1\n[[step]]\nid = \"a\"\nif = \"steps.zz.ok\"\nrun = [\"ok\"]",
		"no input \"who\"":                "version = 1\n[[step]]\nid = \"a\"\nrun = [\"echo\", \"${{ inputs.who }}\"]",
		"its own results":                 "version = 1\n[[step]]\nid = \"a\"\nrun = [\"echo\", \"${{ steps.a.output }}\"]",
		"is not a duration":               "version = 1\ntimeout = \"soon\"" + step,
		"want 5 fields":                   "version = 1\n[[on]]\nschedule = \"* *\"" + step,
		"exactly one of event, schedule":  "version = 1\n[[on]]\nevent = \"x\"\nwebhook = \"y\"" + step,
		"every \"10s\"":                   "version = 1\n[[on]]\nevery = \"10s\"" + step,
		"git \"pull\"":                    "version = 1\n[[on]]\ngit = \"pull\"" + step,
		"the workflow has no input \"b\"": "version = 1\n[[on]]\nwebhook = \"w\"\n[on.inputs]\nb = \"x\"" + step,
		"write plugin/action":             "version = 1\n[[step]]\nid = \"a\"\naction = \"noslash\"",
		"retries 11":                      "version = 1\n[[step]]\nid = \"a\"\nrun = [\"ok\"]\nretries = 11",
		"repeat max 0":                    "version = 1\n[[step]]\nid = \"a\"\nrun = [\"ok\"]\nrepeat = { max = 0, until = \"true\" }",
		"not both":                        "version = 1\n[[step]]\nid = \"a\"\nrun = [\"ok\"]\nforeach = [\"x\"]\nrepeat = { max = 2, until = \"true\" }",
		"concurrency \"later\"":           "version = 1\nconcurrency = \"later\"" + step,
		"are not for agent steps":         "version = 1\n[[step]]\nid = \"a\"\nagent = \"codex\"\nprompt = \"p\"\ndir = \"x\"",
		"not for commands":                "version = 1\n[[step]]\nid = \"a\"\nrun = [\"ok\"]\nisolate = true",
		"stands alone":                    "version = 1\n[[step]]\nid = \"a\"\nagent = [\"any\", \"codex\"]\nprompt = \"p\"",
		"needs a match":                   "version = 1\n[[step]]\nid = \"a\"\nagent = \"codex\"\nprompt = \"p\"\nuntil = \"match\"\nmatch = \"(\"",
		"if: expression":                  "version = 1\n[[step]]\nid = \"a\"\nif = \"a ==\"\nrun = [\"ok\"]",
	} {
		_, err := workflow.Parse([]byte(text), "w.toml")
		if !errors.Is(err, workflow.ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v\n%s", want, err, text)
		}
	}
}

func TestParseRepeatReadsItself(t *testing.T) {
	_, err := workflow.Parse([]byte(`
version = 1
[[step]]
id = "fix"
agent = "codex"
prompt = "fix it"
repeat = { max = 3, until = "steps.fix.summary != ''" }
`), "w.toml")
	if err != nil {
		t.Fatalf("repeat's until reads its own step: %v", err)
	}
}

func FuzzParseWorkflow(f *testing.F) {
	f.Add([]byte(rfcExample))
	f.Add([]byte("version = 1\n[[step]]\nid = \"a\"\nrun = [\"ok\"]\nforeach = \"fromJSON(inputs.x)\"\n[inputs.x]\n"))
	f.Add([]byte("version = 1\n[[on]]\nschedule = \"*/5 1-3 * jan mon\"\n[[step]]\nid = \"a\"\nagent = \"any\"\nprompt = \"${{ trigger.time }}\"\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := workflow.Parse(data, "fuzz.toml")
		if err != nil {
			if !errors.Is(err, workflow.ErrInvalid) {
				t.Fatalf("a parse error that is not ErrInvalid: %v", err)
			}
			return
		}
		// What parses validates again unchanged.
		if err := workflow.Validate(&s); err != nil {
			t.Fatalf("a parsed workflow does not validate: %v", err)
		}
	})
}
