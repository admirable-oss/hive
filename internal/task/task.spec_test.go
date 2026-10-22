package task_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/task"
)

const examplePlan = `
name = "session refactor"
max_parallel = 3
timeout = "2h"

[kinds]
claude = 2

[defaults]
kind = "claude"
environment = "api"
isolate = true
timeout = "30m"
env = { LEVEL = "default", KEEP = "yes" }

[[task]]
id = "api"
needs = ["schema"]
approve = true
prompt = "Implement docs/session.md."
until = "command"
command = ["go", "test", "./..."]
env = { LEVEL = "api" }

[[task]]
id = "schema"
title = "Design the schema"
prompt = "Design the new session schema in docs/session.md."
timeout = "10m"

[[task]]
id = "docs"
kind = "codex"
needs = ["schema"]
prompt = "Document it."
machine = "build-box"
labels = ["linux"]
`

func TestParsePlanFillsInDefaultsAndOrdersByNeeds(t *testing.T) {
	spec, err := task.ParsePlan([]byte(examplePlan), "plan.toml")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "session refactor" || spec.MaxParallel != 3 || spec.TimeoutMS != 2*3600*1000 || spec.Kinds["claude"] != 2 {
		t.Fatalf("plan: %+v", spec)
	}
	tasks, err := task.Validate(spec)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range tasks {
		ids = append(ids, x.ID)
	}
	if got := strings.Join(ids, ","); got != "schema,api,docs" {
		t.Fatalf("order %s: a task comes after what it needs, else as written", got)
	}
	schema, api, docs := tasks[0], tasks[1], tasks[2]
	if schema.TimeoutMS != 10*60*1000 || api.TimeoutMS != 30*60*1000 {
		t.Fatalf("timeouts: a task's own wins over the default: %d %d", schema.TimeoutMS, api.TimeoutMS)
	}
	if !api.Isolate || api.EnvironmentID != "api" || api.Kind != "claude" || docs.Kind != "codex" {
		t.Fatalf("defaults: %+v %+v", api, docs)
	}
	if api.Env["LEVEL"] != "api" || api.Env["KEEP"] != "yes" || schema.Env["LEVEL"] != "default" {
		t.Fatalf("env maps merge, the task winning: %v %v", api.Env, schema.Env)
	}
	if api.Branch("p1") != "hive/p1/api" {
		t.Fatalf("an isolated task's branch: %q", api.Branch("p1"))
	}
}

func TestParsePlanRejectsBadPlans(t *testing.T) {
	head := "[defaults]\nkind = \"claude\"\nenvironment = \"e\"\n"
	cases := map[string]struct{ plan, want string }{
		"an unknown key":        {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nbogus = 1\n", "bogus"},
		"a syntax error":        {"[[task]\n", "plan.toml:1"},
		"no tasks":              {head, "no tasks"},
		"a missing prompt":      {head + "[[task]]\nid = \"a\"\n", "no prompt"},
		"a bad ID":              {head + "[[task]]\nid = \"a b\"\nprompt = \"x\"\n", "task ID"},
		"two tasks with one ID": {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\n[[task]]\nid = \"a\"\nprompt = \"y\"\n", "two tasks"},
		"an unknown need":       {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nneeds = [\"b\"]\n", "not in the plan"},
		"a task needing itself": {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nneeds = [\"a\"]\n", "needs itself"},
		"a cycle": {head + `[[task]]
id = "a"
prompt = "x"
needs = ["c"]
[[task]]
id = "b"
prompt = "x"
needs = ["a"]
[[task]]
id = "c"
prompt = "x"
needs = ["b"]
[[task]]
id = "free"
prompt = "x"
`, "cycle: a → c → b → a"},
		"match without a pattern":   {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nuntil = \"match\"\n", "needs match"},
		"a bad pattern":             {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nuntil = \"match\"\nmatch = \"(\"\n", "match:"},
		"command without a command": {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nuntil = \"command\"\n", "needs command"},
		"an unknown condition":      {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nuntil = \"soon\"\n", "until"},
		"a bad duration":            {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\ntimeout = \"ages\"\n", "not a duration"},
		"no kind":                   {"[[task]]\nid = \"a\"\nprompt = \"x\"\nenvironment = \"e\"\n", "no agent kind"},
		"no environment":            {"[[task]]\nid = \"a\"\nprompt = \"x\"\nkind = \"k\"\n", "no environment"},
		"a bad branch":              {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nworktree = \"-x\"\n", "worktree"},
		"too many retries":          {head + "[[task]]\nid = \"a\"\nprompt = \"x\"\nretries = 99\n", "retries"},
		"a zero kind cap":           {head + "[kinds]\nclaude = 0\n[[task]]\nid = \"a\"\nprompt = \"x\"\n", "kinds.claude"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := task.ParsePlan([]byte(c.plan), "plan.toml")
			if !errors.Is(err, task.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an invalid plan saying %q", err, c.want)
			}
		})
	}
}

func TestValidateKeepsTheWrittenOrderAmongReadyTasks(t *testing.T) {
	spec := task.PlanSpec{
		Defaults: &task.Spec{Kind: "k", EnvironmentID: "e"},
		Tasks: []task.Spec{
			{ID: "d", Prompt: "x", Needs: []string{"a", "b"}},
			{ID: "a", Prompt: "x"},
			{ID: "c", Prompt: "x", Needs: []string{"a"}},
			{ID: "b", Prompt: "x"},
		},
	}
	tasks, err := task.Validate(spec)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range tasks {
		ids = append(ids, x.ID)
	}
	if got := strings.Join(ids, ","); got != "a,c,b,d" {
		t.Fatalf("order %s", got)
	}
}

func FuzzParsePlan(f *testing.F) {
	f.Add([]byte(examplePlan))
	f.Add([]byte("[[task]]\nid='a'\nprompt='x'\nkind='k'\nenvironment='e'\nneeds=['a']\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		spec, err := task.ParsePlan(data, "fuzz")
		if err != nil {
			return
		}
		// What parses also validates, in an order where needs come first.
		tasks, err := task.Validate(spec)
		if err != nil {
			t.Fatalf("parsed but invalid: %v", err)
		}
		seen := map[string]bool{}
		for _, x := range tasks {
			for _, n := range x.Needs {
				if !seen[n] {
					t.Fatalf("%s comes before %s, which it needs", x.ID, n)
				}
			}
			seen[x.ID] = true
		}
	})
}
