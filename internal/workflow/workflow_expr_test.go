package workflow_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/workflow"
)

func exprCtx() workflow.Context {
	return workflow.Context{
		Vars: map[string]any{
			"inputs": map[string]any{"branch": "main", "count": "3", "empty": ""},
			"steps": map[string]any{
				"test":    map[string]any{"outcome": "failure", "ok": false, "exit_code": 1.0, "outputs": map[string]any{"cover": "81.5"}},
				"my-step": map[string]any{"outcome": "success", "ok": true},
			},
			"trigger": map[string]any{"type": "git", "commits": []any{"a1", "b2"}, "data": map[string]any{"n": 2.0}},
			"list":    []any{"x", "y"},
		},
		Success: true,
	}
}

func TestExpressions(t *testing.T) {
	ctx := exprCtx()
	for src, want := range map[string]any{
		`'a'`:                                     "a",
		`"b\"c"`:                                  `b"c`,
		`42`:                                      42.0,
		`-1.5`:                                    -1.5,
		`true`:                                    true,
		`null`:                                    nil,
		`inputs.branch`:                           "main",
		`inputs.nope`:                             nil,
		`nope.at.all`:                             nil,
		`steps.test.outcome == 'failure'`:         true,
		`steps.test.outcome != 'failure'`:         false,
		`steps['my-step'].ok`:                     true,
		`steps.my-step.ok`:                        true,
		`steps.test.outputs.cover >= 80`:          true,
		`steps.test.outputs.cover < '9'`:          false, // numbers compare as numbers
		`inputs.count == 3`:                       true,  // inputs are text
		`trigger.commits[1]`:                      "b2",
		`trigger.commits[5]`:                      nil,
		`trigger.data.n > 1 && !steps.test.ok`:    true,
		`inputs.empty || 'default'`:               "default",
		`inputs.branch && 'yes'`:                  "yes",
		`!(inputs.branch == 'main') || false`:     false,
		`success()`:                               true,
		`failure()`:                               false,
		`always()`:                                true,
		`contains(inputs.branch, 'AI')`:           true,
		`contains(list, 'y')`:                     true,
		`contains(list, 'z')`:                     false,
		`startsWith('release/1', 'release/')`:     true,
		`endsWith(inputs.branch, 'in')`:           true,
		`join(list, '+')`:                         "x+y",
		`join(trigger.commits)`:                   "a1,b2",
		`len(list)`:                               2.0,
		`len(inputs.branch)`:                      4.0,
		`format('{0}-{1}', inputs.branch, 7)`:     "main-7",
		`format('{{x}}')`:                         "{x}",
		`toJSON(list)`:                            `["x","y"]`,
		`fromJSON('{"a": [1, 2]}').a[1]`:          2.0,
		`steps.test.exit_code == 1`:               true,
		`inputs.branch == 'main' && inputs.count`: "3",
	} {
		e, err := workflow.ParseExpr(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		got, err := e.Eval(ctx)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if workflow.Text(got) != workflow.Text(want) || (got == nil) != (want == nil) {
			t.Errorf("%s = %#v, want %#v", src, got, want)
		}
	}
}

func TestExpressionErrors(t *testing.T) {
	for _, src := range []string{
		``, `'open`, `a ==`, `(a`, `a b`, `nope()`, `success(1)`, `contains('a')`, `a.`, `a[1`, `@`, `a = b`,
	} {
		if _, err := workflow.ParseExpr(src); !errors.Is(err, workflow.ErrExpr) {
			t.Errorf("%q parsed: %v", src, err)
		}
	}
	e, err := workflow.ParseExpr(`fromJSON('nope')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Eval(exprCtx()); !errors.Is(err, workflow.ErrExpr) || !strings.Contains(err.Error(), "fromJSON") {
		t.Fatalf("an evaluation error names its function: %v", err)
	}
	if _, err := workflow.ParseExpr(strings.Repeat("a", 5000)); err == nil {
		t.Fatal("a huge expression parsed")
	}
}

func TestTruthiness(t *testing.T) {
	for v, want := range map[any]bool{"": false, "x": true, 0.0: false, 2.0: true, false: false, true: true} {
		if workflow.Truthy(v) != want {
			t.Errorf("Truthy(%#v) != %v", v, want)
		}
	}
	if workflow.Truthy(nil) || !workflow.Truthy([]any{}) {
		t.Error("null is false, a list is true")
	}
}

func TestTemplates(t *testing.T) {
	ctx := exprCtx()
	for src, want := range map[string]string{
		"plain":                                       "plain",
		"on ${{ inputs.branch }}!":                    "on main!",
		"${{inputs.branch}}${{ inputs.count }}":       "main3",
		"${{ list }}":                                 `["x","y"]`,
		"${{ inputs.nope }}.":                         ".",
		"${{ format('{0}}', 'a') }}":                  "a}",
		"${{ '}}' }} and ${{ steps.test.exit_code }}": "}} and 1",
	} {
		tp, err := workflow.ParseTemplate(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		got, err := tp.Render(ctx)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", src, got, err, want)
		}
	}
	if _, err := workflow.ParseTemplate("${{ open"); err == nil {
		t.Error("an unclosed ${{ parsed")
	}
	tp, _ := workflow.ParseTemplate("${{ list }}")
	if v, _ := tp.Value(ctx); len(v.([]any)) != 2 {
		t.Errorf("an expression alone keeps its type: %#v", v)
	}
}

func FuzzExpressions(f *testing.F) {
	for _, s := range []string{`a.b == 'c'`, `contains(x, "y") || !z`, `f(1, 2)[0].x`, `"\\`, `${{ x }}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		e, err := workflow.ParseExpr(src)
		if err != nil {
			return
		}
		_, _ = e.Eval(exprCtx())
		_, _ = workflow.ParseTemplate("x ${{ " + src + " }} y")
	})
}

func TestSchedules(t *testing.T) {
	loc := time.UTC
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	for _, c := range []struct{ line, from, want string }{
		{"*/15 * * * *", "2026-10-24 10:07", "2026-10-24 10:15"},
		{"0 3 * * 1-5", "2026-10-24 10:07", "2026-10-26 03:00"}, // a Saturday: next Monday
		{"30 9 1,15 * *", "2026-10-15 09:30", "2026-11-01 09:30"},
		{"0 0 * jan,jul sun", "2026-10-24 10:07", "2027-01-03 00:00"},
		{"@daily", "2026-12-31 23:59", "2027-01-01 00:00"},
		{"0 12 29 2 *", "2026-03-01 00:00", "2028-02-29 12:00"},
		{"5-10/5 * * * *", "2026-10-24 10:07", "2026-10-24 10:10"},
		{"0 0 13 * 5", "2026-10-24 00:00", "2026-10-30 00:00"}, // the 13th or a Friday
		{"0 0 * * 7", "2026-10-24 00:00", "2026-10-25 00:00"},  // 7 is Sunday
	} {
		s, err := workflow.ParseSchedule(c.line)
		if err != nil {
			t.Errorf("%s: %v", c.line, err)
			continue
		}
		if got := s.Next(at(c.from)); !got.Equal(at(c.want)) {
			t.Errorf("%s after %s = %s, want %s", c.line, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "*/0 * * * *", "5-1 * * * *", "* * * * fun"} {
		if _, err := workflow.ParseSchedule(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	never, _ := workflow.ParseSchedule("0 0 30 2 *")
	if !never.Next(at("2026-01-01 00:00")).IsZero() {
		t.Error("February 30th came")
	}
}
