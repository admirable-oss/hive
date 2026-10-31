package runtime

import (
	"bufio"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/apisurface"
	"github.com/admirable-oss/hive/internal/event"
)

// frozenMethods reads the wire API's methods, and every field name of its
// types, from api/v1/api.txt.
func frozenMethods(t *testing.T) (methods []string, fields map[string][]string) {
	t.Helper()
	f, err := os.Open("../../api/v1/api.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fields = map[string][]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m, ok := strings.CutPrefix(strings.TrimPrefix(line, "pipe "), "method "); ok {
			methods = append(methods, m)
			continue
		}
		// "type pkg.Type.field: type"
		if rest, ok := strings.CutPrefix(line, "type "); ok {
			name, _, _ := strings.Cut(rest, ":")
			if parts := strings.Split(name, "."); len(parts) == 3 {
				fields[parts[2]] = append(fields[parts[2]], parts[0]+"."+parts[1])
			}
		}
	}
	slices.Sort(methods)
	return slices.Compact(methods), fields
}

// TestMemberRulesClassifyEveryMethod: every method is either open to
// members with a role, or closed to them, never both; a new method is
// closed until it is classified here.
func TestMemberRulesClassifyEveryMethod(t *testing.T) {
	methods, _ := frozenMethods(t)
	for _, m := range methods {
		_, open := memberRules[m]
		closed := slices.Contains(memberClosed, m)
		switch {
		case open && closed:
			t.Errorf("method %s is both open to members and closed", m)
		case !open && !closed:
			t.Errorf("method %s is not classified for members (memberRules or memberClosed in runtime.roles.go)", m)
		}
	}
	for m := range memberRules {
		if !slices.Contains(methods, m) {
			t.Errorf("member rule for %s, which does not exist", m)
		}
	}
	for _, m := range memberClosed {
		if !slices.Contains(methods, m) {
			t.Errorf("closed method %s does not exist", m)
		}
	}
	for _, m := range access.BaseMethods {
		if r, ok := memberRules[m]; !ok || r.role != access.RoleObserver {
			t.Errorf("base method %s is not open to every member", m)
		}
	}
}

// TestMemberRolesFrozen holds what each role may call to api/v1/roles.txt,
// so a role that widens or narrows is reviewed.
func TestMemberRolesFrozen(t *testing.T) {
	var lines []string
	for m, r := range memberRules {
		scope := "environment"
		switch {
		case r.across:
			scope = "all environments"
		case len(r.params) == 0 && r.filtered:
			scope = "filtered"
		case len(r.params) == 0:
			scope = "none"
		case r.filtered:
			scope = "environment or filtered"
		}
		extra := ""
		if r.agentOnly {
			extra += ", agents only below owner"
		}
		if r.view {
			extra += ", input only for operators of agents"
		}
		if r.by {
			extra += ", by the member"
		}
		if r.ownFocus {
			extra += ", the member's own focus"
		}
		lines = append(lines, "method "+m+": "+string(r.role)+" in "+scope+extra)
	}
	for _, m := range memberClosed {
		lines = append(lines, "method "+m+": closed")
	}
	slices.Sort(lines)
	apisurface.Check(t, "../../api/v1/roles.txt", lines)
}

// TestRedactionCoversSecrets: every field in the API whose name says it
// can carry a secret or a private path is left out below owner.
func TestRedactionCoversSecrets(t *testing.T) {
	_, fields := frozenMethods(t)
	suspicious := []string{"env", "args", "cwd", "working_dir", "pid", "session_id", "log_dir", "token", "secret", "password", "key"}
	for name, types := range fields {
		for _, s := range suspicious {
			if name != s && !strings.HasSuffix(name, "_"+s) && !strings.HasPrefix(name, s+"_") {
				continue
			}
			if slices.Contains(redactedKeys, name) || reviewedFields[name] {
				continue
			}
			t.Errorf("field %s (in %s) may carry a secret: add it to redactedKeys, or to reviewedFields with why it is safe", name, strings.Join(types, ", "))
		}
	}
}

// reviewedFields look like secrets by name and are not.
var reviewedFields = map[string]bool{
	"key":        true, // public keys and configuration keys
	"token":      true, // observer tokens, only in observer.* methods, which are closed to members
	"args_regex": true, // agent detection patterns from manifests
}

func member(grants ...access.Grant) access.Member {
	return access.Member{ID: "m_0123456789", Name: "alice", Grants: grants}
}

func shapeJSON(t *testing.T, m access.Member, raw string, key string) string {
	t.Helper()
	v, err := decodeAny(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	s := shaper{m: m, tabEnv: func(id string) string { return map[string]string{"t1": "dev", "t2": "ops"}[id] }}
	out, err := json.Marshal(s.walk(v, "", key))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestShapeFiltersAndRedacts(t *testing.T) {
	op := member(access.Grant{Environment: "dev", Role: access.RoleOperator})
	procs := `[
		{"id":"p1","environment_id":"dev","command":"/usr/local/bin/claude","args":["--api-key","sk-1"],"env":{"TOKEN":"x"},"working_dir":"/home/me/secret","pid":42,"status":"running"},
		{"id":"p2","environment_id":"ops","command":"psql","status":"running"}
	]`
	got := shapeJSON(t, op, procs, "")
	if want := `[{"command":"claude","environment_id":"dev","id":"p1","status":"running"}]`; got != want {
		t.Fatalf("processes for an operator:\n got %s\nwant %s", got, want)
	}

	// An owner of the environment sees everything in it, and nothing else.
	own := member(access.Grant{Environment: "dev", Role: access.RoleOwner})
	got = shapeJSON(t, own, procs, "")
	if !strings.Contains(got, "sk-1") || !strings.Contains(got, `"pid":42`) || strings.Contains(got, "psql") {
		t.Fatalf("processes for an owner of dev: %s", got)
	}

	// Environments are filtered by their own ID, nested records by theirs.
	snap := `{"environments":[{"id":"dev","env":{"A":"1"},"path":"/w/dev"},{"id":"ops","path":"/w/ops"}],
		"panes":[{"id":"x","environment_id":"dev","process":{"id":"p1","environment_id":"dev","env":{"B":"2"},"command":"sh"}},{"id":"y","environment_id":"ops"}],
		"rollups":{"session":"blocked","environments":{"dev":"idle","ops":"blocked"},"tabs":{"t1":"idle","t2":"blocked"}}}`
	got = shapeJSON(t, op, snap, "")
	for _, bad := range []string{`"ops"`, `"y"`, `"A"`, `"B"`, "session", "t2"} {
		if strings.Contains(got, bad) {
			t.Errorf("snapshot for an operator of dev has %s: %s", bad, got)
		}
	}
	for _, want := range []string{`"path":"/w/dev"`, `"dev":"idle"`, `"t1":"idle"`, `"command":"sh"`} {
		if !strings.Contains(got, want) {
			t.Errorf("snapshot for an operator of dev lacks %s: %s", want, got)
		}
	}

	// A member who sees everything keeps the session's state.
	all := member(access.Grant{Environment: access.AllEnvironments, Role: access.RoleObserver})
	if got := shapeJSON(t, all, snap, ""); !strings.Contains(got, `"session":"blocked"`) || strings.Contains(got, `"A"`) {
		t.Fatalf("snapshot for an observer of all: %s", got)
	}
}

func TestEventFilter(t *testing.T) {
	g := &memberGuard{}
	op := member(access.Grant{Environment: "dev", Role: access.RoleOperator})
	s := shaper{m: op, tabEnv: func(string) string { return "" }}
	filter := func(ev event.Event) (event.Event, bool) {
		// as eventFilter, without the tab lookup the test has no panes for
		f := g.eventFilterWith(op, s)
		return f(ev)
	}
	ev, ok := filter(event.Event{Type: event.ProcessStarted, Data: json.RawMessage(`{"id":"p1","environment_id":"dev","env":{"T":"x"},"command":"/bin/claude"}`)})
	if !ok || strings.Contains(string(ev.Data), `"T"`) || !strings.Contains(string(ev.Data), `"command":"claude"`) {
		t.Fatalf("an event in dev: %v %s", ok, ev.Data)
	}
	if _, ok := filter(event.Event{Type: event.ProcessStarted, Data: json.RawMessage(`{"id":"p2","environment_id":"ops"}`)}); ok {
		t.Fatal("an event in another environment reached the member")
	}
	if _, ok := filter(event.Event{Type: "environment.created", Data: json.RawMessage(`{"id":"ops"}`)}); ok {
		t.Fatal("another environment's creation reached the member")
	}
	if _, ok := filter(event.Event{Type: "observer.activity", Data: json.RawMessage(`{"observers":[]}`)}); ok {
		t.Fatal("an event in no environment reached a member of one")
	}
	if _, ok := filter(event.Event{Type: EventAccessChanged, Data: json.RawMessage(`{"action":"invited"}`)}); ok {
		t.Fatal("an access change reached a member")
	}
}

func TestMemberAllowed(t *testing.T) {
	obs := member(access.Grant{Environment: "dev", Role: access.RoleObserver})
	if !memberAllowed(obs, "agent.read") || memberAllowed(obs, "agent.prompt") || memberAllowed(obs, "access.list") || memberAllowed(obs, "nope") {
		t.Fatal("an observer's methods")
	}
	op := member(access.Grant{Environment: "dev", Role: access.RoleOperator})
	if !memberAllowed(op, "agent.prompt") || !memberAllowed(op, "task.approve") || memberAllowed(op, "agent.start") || memberAllowed(op, "pane.split") {
		t.Fatal("an operator's methods")
	}
}

// TestSignedMethodsActAsTheMember: who approved, commented or ran is the
// member's name, whatever the member's side said.
func TestSignedMethodsActAsTheMember(t *testing.T) {
	got := setParam(json.RawMessage(`{"id":"plan1","by":"the-owner","note":"ok"}`), "by", "alice")
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil || m["by"] != "alice" || m["id"] != "plan1" || m["note"] != "ok" {
		t.Fatalf("params = %s (%v)", got, err)
	}
	if got := setParam(nil, "by", "alice"); string(got) != `{"by":"alice"}` {
		t.Fatalf("no params: %s", got)
	}
	for _, method := range []string{"task.approve", "branch.comment", "workflow.run", "workflow.rerun", "workflow.cancel"} {
		if !memberRules[method].by {
			t.Errorf("%s does not record the member as who acted", method)
		}
	}
}
