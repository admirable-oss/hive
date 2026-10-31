package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/presence"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/internal/terminal"
)

// Roles (RFC 0007): what a member may call, and in which environments.
// Every control method is in memberRules, with the lowest role that may
// call it and the parameters that name what it acts on, or in
// memberClosed, for the runtime's own user alone. A method in neither is
// refused, and a test fails until it is classified.

// targetKind is what a parameter names; each resolves to an environment.
type targetKind int

const (
	targetEnv     targetKind = iota + 1 // an environment's ID
	targetEnvs                          // a list of environment IDs
	targetPane                          // a pane's ID
	targetTab                           // a tab's ID
	targetAgent                         // an agent's ID, or its pane's
	targetProcess                       // a process's ID
	targetPlan                          // a plan's ID: every environment its tasks run in
	targetBranch                        // a branch reference
)

type ruleParam struct {
	name string
	kind targetKind
}

// roleRule is what one method needs from a member.
type roleRule struct {
	role access.Role
	// params name the method's target. Without any, the method acts in no
	// environment; with params all empty, in every environment (unless
	// filtered).
	params []ruleParam
	// across: the method acts across environments, so it needs the role
	// in all of them (a grant for all environments).
	across bool
	// filtered: without a target, the method reads everything, and its
	// result is filtered to the member's environments.
	filtered bool
	// agentOnly: below owner, the target must be a running agent: an
	// operator types into agents, never into a shell.
	agentOnly bool
	// view: a terminal view, which drops what the member types unless they
	// may type into it.
	view bool
	// by: the method records who acted (its "by" parameter), which the
	// daemon fills with the member's name.
	by bool
	// ownFocus: a member's focus is their own, kept in presence, never the
	// session's.
	ownFocus bool
}

func p(name string, kind targetKind) ruleParam { return ruleParam{name, kind} }

func free(r access.Role) roleRule { return roleRule{role: r} }

func across(r access.Role) roleRule { return roleRule{role: r, across: true} }

func on(r access.Role, params ...ruleParam) roleRule { return roleRule{role: r, params: params} }

func filtered(r access.Role, params ...ruleParam) roleRule {
	return roleRule{role: r, params: params, filtered: true}
}

func typing(r roleRule) roleRule { r.agentOnly = true; return r }

func signed(r roleRule) roleRule { r.by = true; return r }

func ownFocus(r roleRule) roleRule { r.ownFocus = true; return r }

const (
	roleObserver = access.RoleObserver
	roleOperator = access.RoleOperator
	roleOwner    = access.RoleOwner
)

var memberRules = map[string]roleRule{
	// Being connected, and what the daemon is.
	"runtime.ping": free(roleObserver), "runtime.status": free(roleObserver), "client.report": free(roleObserver),
	"access.whoami": free(roleObserver), "api.schema": free(roleObserver), "agent.manifests": free(roleObserver),
	"events.subscribe": filtered(roleObserver),

	// Reading, in the member's environments.
	"environment.list": filtered(roleObserver), "environment.get": on(roleObserver, p("id", targetEnv)),
	"tab.list":         filtered(roleObserver, p("environment_id", targetEnv), p("tab_id", targetTab)),
	"tab.get":          on(roleObserver, p("id", targetTab)),
	"pane.list":        filtered(roleObserver, p("environment_id", targetEnv), p("tab_id", targetTab)),
	"pane.get":         on(roleObserver, p("id", targetPane)),
	"pane.read":        on(roleObserver, p("id", targetPane)),
	"pane.wait_output": on(roleObserver, p("id", targetPane)),
	"layout.export":    on(roleObserver, p("environments", targetEnvs)),
	"agent.list":       filtered(roleObserver), "api.snapshot": filtered(roleObserver), "metadata.list": filtered(roleObserver),
	"agent.get":         on(roleObserver, p("id", targetAgent)),
	"agent.explain":     on(roleObserver, p("id", targetAgent)),
	"agent.read":        on(roleObserver, p("id", targetAgent)),
	"agent.wait":        on(roleObserver, p("id", targetAgent)),
	"process.list":      filtered(roleObserver, p("environment_id", targetEnv)),
	"process.get":       on(roleObserver, p("id", targetProcess)),
	"process.logs":      on(roleObserver, p("id", targetProcess)),
	"terminal.snapshot": on(roleObserver, p("process_id", targetProcess)),
	"terminal.graphics": on(roleObserver, p("process_id", targetProcess)),
	"terminal.frames":   {role: roleObserver, params: []ruleParam{p("process_id", targetProcess)}, view: true},
	"task.list":         filtered(roleObserver),
	"task.get":          on(roleObserver, p("id", targetPlan)),
	"task.wait":         on(roleObserver, p("id", targetPlan)),
	"branch.list":       filtered(roleObserver),
	"branch.get":        on(roleObserver, p("ref", targetBranch)),
	"branch.check":      on(roleObserver, p("ref", targetBranch)),
	"branch.diff":       on(roleObserver, p("ref", targetBranch)),
	"branch.conflicts":  on(roleObserver, p("ref", targetBranch)),

	// Where the member looks: their own focus, and their presence.
	"pane.focus":    ownFocus(on(roleObserver, p("id", targetPane))),
	"tab.focus":     ownFocus(on(roleObserver, p("id", targetTab))),
	"agent.focus":   ownFocus(on(roleObserver, p("id", targetAgent))),
	"presence.set":  filtered(roleObserver, p("environment", targetEnv), p("tab", targetTab), p("pane", targetPane)),
	"presence.list": filtered(roleObserver),

	// Reading what spans environments.
	"worktree.list": across(roleObserver), "observer.activity": across(roleObserver),
	"insights.summary": across(roleObserver), "insights.asks": across(roleObserver),
	"insights.budgets": across(roleObserver), "insights.export": across(roleObserver),
	"workflow.list": across(roleObserver), "workflow.get": across(roleObserver), "workflow.runs": across(roleObserver),
	"workflow.status": across(roleObserver), "workflow.wait": across(roleObserver), "workflow.validate": across(roleObserver),
	"issue.get": across(roleObserver), "issue.search": across(roleObserver),
	"plugin.list": across(roleObserver), "plugin.contributions": across(roleObserver),
	"plugin.views": across(roleObserver), "plugin.capabilities": across(roleObserver),

	// Operating: prompting agents, approving, commenting. Nothing starts.
	"agent.prompt":    typing(on(roleOperator, p("id", targetAgent))),
	"agent.send_keys": typing(on(roleOperator, p("id", targetAgent))),
	"agent.seen":      on(roleOperator, p("id", targetAgent)),
	"pane.input":      typing(on(roleOperator, p("id", targetPane))),
	"pane.send_keys":  typing(on(roleOperator, p("id", targetPane))),
	"pane.send_text":  typing(on(roleOperator, p("id", targetPane))),
	"terminal.attach": typing(on(roleOperator, p("process_id", targetProcess))),
	"terminal.input":  typing(on(roleOperator, p("process_id", targetProcess))),
	"terminal.image":  typing(on(roleOperator, p("process_id", targetProcess))),
	"terminal.resize": typing(on(roleOperator, p("process_id", targetProcess))),
	"pane.claim":      typing(on(roleOperator, p("pane", targetPane))),
	"pane.release":    on(roleOperator, p("pane", targetPane)),
	"task.approve":    signed(on(roleOperator, p("id", targetPlan))),
	"agent.handover":  on(roleOperator, p("id", targetAgent)),
	"task.assign":     on(roleOperator, p("id", targetPlan)),
	"branch.comment":  signed(on(roleOperator, p("ref", targetBranch))),
	"branch.send":     on(roleOperator, p("ref", targetBranch)),
	"issue.comment":   across(roleOperator),

	// Owning an environment: starting, stopping and arranging.
	"environment.update": on(roleOwner, p("id", targetEnv)),
	"environment.create": across(roleOwner), "environment.remove": across(roleOwner),
	"tab.create":      on(roleOwner, p("environment_id", targetEnv)),
	"tab.close":       on(roleOwner, p("id", targetTab)),
	"tab.rename":      on(roleOwner, p("id", targetTab)),
	"tab.resize":      on(roleOwner, p("id", targetTab)),
	"pane.close":      on(roleOwner, p("id", targetPane)),
	"pane.rename":     on(roleOwner, p("id", targetPane)),
	"pane.resize":     on(roleOwner, p("id", targetPane)),
	"pane.zoom":       on(roleOwner, p("id", targetPane)),
	"pane.run":        on(roleOwner, p("id", targetPane)),
	"pane.split":      on(roleOwner, p("pane", targetPane), p("tab_id", targetTab)),
	"pane.popup":      on(roleOwner, p("tab_id", targetTab)),
	"pane.move":       on(roleOwner, p("pane", targetPane), p("environment_id", targetEnv), p("tab_id", targetTab), p("target", targetPane)),
	"pane.swap":       on(roleOwner, p("id", targetPane), p("with", targetPane)),
	"layout.apply":    across(roleOwner),
	"agent.start":     on(roleOwner, p("environment_id", targetEnv)),
	"agent.stop":      on(roleOwner, p("id", targetAgent)),
	"agent.rename":    on(roleOwner, p("id", targetAgent)),
	"process.start":   on(roleOwner, p("environment_id", targetEnv)),
	"process.stop":    on(roleOwner, p("id", targetProcess)),
	"metadata.report": on(roleOwner, p("environment", targetEnv), p("pane", targetPane)),
	"task.run":        across(roleOwner),
	"task.cancel":     on(roleOwner, p("id", targetPlan)),
	"task.retry":      on(roleOwner, p("id", targetPlan)),
	"branch.ready":    on(roleOwner, p("ref", targetBranch)),
	"branch.update":   on(roleOwner, p("ref", targetBranch)),
	"branch.clean":    on(roleOwner, p("ref", targetBranch)),
	"branch.push":     on(roleOwner, p("ref", targetBranch)),
	"branch.pr":       on(roleOwner, p("ref", targetBranch)),
	"worktree.create": across(roleOwner), "worktree.open": across(roleOwner), "worktree.remove": across(roleOwner),
	"workflow.run": signed(across(roleOwner)), "workflow.rerun": signed(across(roleOwner)),
	"workflow.cancel": signed(across(roleOwner)), "workflow.fire": across(roleOwner),
	"plugin.view.click": across(roleOwner),
}

// memberClosed are the methods no member may call, whatever its role:
// managing access, observers, plugins, the configuration and the daemon,
// and what agents and plugins report about themselves.
var memberClosed = []string{
	"access.audit", "access.doctor", "access.get", "access.grant", "access.invite", "access.list",
	"access.renew", "access.revoke",
	"observer.audit", "observer.create", "observer.get", "observer.list", "observer.revoke", "observer.validate",
	"debug.pprof", "debug.stats", "runtime.gc", "runtime.shutdown", "session.save", "session.restore",
	"process.logs.stream",
	"agent.reload", "agent.report", "insights.usage", "insights.resume", "issue.close",
	"workflow.reload", "workflow.trust", "workflow.trigger",
	"plugin.cancel", "plugin.card", "plugin.config", "plugin.config.set", "plugin.ready", "plugin.reload",
	"plugin.request", "plugin.run", "plugin.status", "plugin.view.set", "plugin.view.clear",
}

// bestRole is the highest role m holds anywhere.
func bestRole(m access.Member) access.Role {
	best := access.RoleNone
	for _, g := range m.Grants {
		if g.Role.AtLeast(best) {
			best = g.Role
		}
	}
	return best
}

// memberAllowed says whether m may call method at all, somewhere: what
// the member socket checks first, and lists in the welcome.
func memberAllowed(m access.Member, method string) bool {
	r, ok := memberRules[method]
	return ok && bestRole(m).AtLeast(r.role)
}

// memberGuard serves members: it checks each request's targets against
// the member's grants, and shapes what it returns to what the member may
// see.
type memberGuard struct {
	router   protocol.Handler
	input    *inputGuard
	envs     environment.Service
	panes    *pane.Service
	agents   *agent.Service
	procs    process.Service
	tasks    *task.Service
	branches *branch.Service
	// audit records what members change; nil records nothing.
	audit func() *access.Audit
}

var errOutOfScope = errors.New("not found")

func (g *memberGuard) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	c, ok := access.CallerOf(ctx)
	if !ok {
		return g.router.Handle(ctx, req)
	}
	m := c.Member
	rule, ok := memberRules[req.Method]
	if !ok {
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodePermissionDenied, fmt.Errorf("member %s may not call %s", m.Name, req.Method)))
	}
	envs, err := g.check(ctx, m, rule, req)
	if err != nil {
		g.record(c, req.Method, envs, err)
		return protocol.Fail(req, err)
	}
	// What the member starts is theirs, and the events it causes name them.
	ctx = presence.WithActor(ctx, presence.Person{ID: m.ID, Name: m.Name})
	if rule.by {
		req.Params = setParam(req.Params, "by", m.Name)
	}
	if req.Method == "events.subscribe" {
		ctx = event.WithFilter(ctx, g.eventFilter(ctx, m))
	}
	if rule.view && !g.mayType(ctx, m, rule, req, envs) {
		ctx = terminal.ReadOnly(ctx)
	}
	var resp protocol.Response
	if rule.ownFocus {
		resp = g.memberFocus(ctx, req)
	} else {
		resp = g.router.Handle(ctx, req)
	}
	if resp.Error == nil && resp.Pipe == nil && len(resp.Result) > 0 {
		if shaped, err := g.shape(ctx, m, req.Method, resp.Result); err == nil {
			resp.Result = shaped
		} else {
			resp = protocol.Fail(req, err)
		}
	}
	if rule.role != roleObserver {
		var rerr error
		if resp.Error != nil {
			rerr = resp.Error
		}
		g.record(c, req.Method, envs, rerr)
	}
	return resp
}

// check resolves the request's targets and checks m's role in each. It
// returns the environments the request acts in.
func (g *memberGuard) check(ctx context.Context, m access.Member, rule roleRule, req protocol.Request) ([]string, error) {
	deny := func(why string) error {
		return protocol.NewError(protocol.ErrorCodePermissionDenied, fmt.Errorf("member %s may not call %s %s", m.Name, req.Method, why))
	}
	if rule.across {
		if !m.RoleIn(access.AllEnvironments).AtLeast(rule.role) {
			return nil, deny(fmt.Sprintf("(it needs the %s role in every environment)", rule.role))
		}
		return nil, nil
	}
	if len(rule.params) == 0 {
		if !bestRole(m).AtLeast(rule.role) {
			return nil, deny(fmt.Sprintf("(it needs the %s role)", rule.role))
		}
		return nil, nil
	}
	params := map[string]json.RawMessage{}
	if len(req.Params) > 0 && !bytes.Equal(bytes.TrimSpace(req.Params), []byte("null")) {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, protocol.NewError(protocol.ErrorCodeInvalidParams, err)
		}
	}
	var envs []string
	named := false
	for _, rp := range rule.params {
		got, err := g.resolve(ctx, rp.kind, params[rp.name])
		if err != nil {
			return nil, protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf("%s %w", rp.name, errOutOfScope))
		}
		if got != nil {
			named = true
			envs = append(envs, got...)
		}
	}
	if !named && !rule.filtered {
		// Nothing named: the method acts everywhere.
		if !m.RoleIn(access.AllEnvironments).AtLeast(rule.role) {
			return nil, deny(fmt.Sprintf("without naming an environment (it needs the %s role in every environment)", rule.role))
		}
		return nil, nil
	}
	if !named && !bestRole(m).AtLeast(rule.role) {
		return nil, deny(fmt.Sprintf("(it needs the %s role)", rule.role))
	}
	slices.Sort(envs)
	envs = slices.Compact(envs)
	for _, env := range envs {
		r := m.RoleIn(env)
		switch {
		case r == access.RoleNone:
			return envs, protocol.NewError(protocol.ErrorCodeNotFound, errOutOfScope)
		case !r.AtLeast(rule.role):
			return envs, deny(fmt.Sprintf("in environment %s (it needs the %s role; %s's is %s)", env, rule.role, m.Name, r))
		case rule.agentOnly && r != roleOwner && !g.isAgent(ctx, rule, req):
			return envs, deny("there: an operator types only into panes running an agent, never into a shell")
		}
	}
	return envs, nil
}

// resolve finds the environments raw (a parameter of kind k) is in; nil
// for a parameter not given.
func (g *memberGuard) resolve(ctx context.Context, k targetKind, raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if k == targetEnvs {
		var ids []string
		if err := json.Unmarshal(raw, &ids); err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return nil, nil
		}
		for _, id := range ids {
			if _, err := g.envs.Get(ctx, id); err != nil {
				return nil, err
			}
		}
		return ids, nil
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, nil
	}
	one := func(env string, err error) ([]string, error) {
		if err != nil {
			return nil, err
		}
		if env == "" {
			return nil, errOutOfScope // in no environment: not a member's
		}
		return []string{env}, nil
	}
	switch k {
	case targetEnv:
		e, err := g.envs.Get(ctx, id)
		return one(e.ID, err)
	case targetPane:
		pn, err := g.panes.Pane(ctx, id)
		return one(pn.EnvironmentID, err)
	case targetTab:
		t, err := g.panes.Tab(ctx, id)
		return one(t.EnvironmentID, err)
	case targetAgent:
		a, err := g.agents.Get(ctx, id)
		return one(a.EnvironmentID, err)
	case targetProcess:
		pr, err := g.procs.Get(ctx, id)
		return one(pr.EnvironmentID, err)
	case targetBranch:
		b, err := g.branches.Get(ctx, id)
		return one(b.EnvironmentID, err)
	case targetPlan:
		pl, err := g.tasks.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return planEnvironments(pl)
	}
	return nil, fmt.Errorf("unknown target kind %d", k)
}

// planEnvironments lists the environments a plan's tasks run in; a task
// that names none (not placed yet) makes the plan span every environment.
func planEnvironments(pl task.Plan) ([]string, error) {
	var out []string
	for _, t := range pl.Tasks {
		env := taskEnvironment(t)
		if env == "" {
			return nil, errOutOfScope
		}
		out = append(out, env)
	}
	if len(out) == 0 {
		return nil, errOutOfScope
	}
	return out, nil
}

// taskEnvironment is where a task runs, or, before it starts (waiting for
// approval, say), the environment its spec names.
func taskEnvironment(t task.Task) string {
	if t.EnvironmentID != "" {
		return t.EnvironmentID
	}
	return t.Spec.EnvironmentID
}

// isAgent reports whether the request's first target runs an agent that
// has not exited.
func (g *memberGuard) isAgent(ctx context.Context, rule roleRule, req protocol.Request) bool {
	var params map[string]json.RawMessage
	if json.Unmarshal(req.Params, &params) != nil || len(rule.params) == 0 {
		return false
	}
	var id string
	if json.Unmarshal(params[rule.params[0].name], &id) != nil || id == "" {
		return false
	}
	a, err := g.agents.Get(ctx, id) // a process's ID, or a pane's
	return err == nil && a.Kind != "" && a.State != agent.StateExited
}

// mayType says whether a terminal view opened by m takes its input.
func (g *memberGuard) mayType(ctx context.Context, m access.Member, rule roleRule, req protocol.Request, envs []string) bool {
	if len(envs) == 0 {
		return false
	}
	for _, env := range envs {
		switch r := m.RoleIn(env); {
		case r == roleOwner:
		case r == roleOperator && g.isAgent(ctx, rule, req):
		default:
			return false
		}
	}
	return true
}

func (g *memberGuard) record(c access.Caller, method string, envs []string, err error) {
	if g.audit == nil {
		return
	}
	outcome := "ok"
	if pe, ok := errors.AsType[*protocol.Error](err); ok {
		outcome = pe.Code
	} else if err != nil {
		outcome = protocol.ErrorCodeInternal
	}
	action := access.AuditAction
	if outcome == protocol.ErrorCodePermissionDenied || outcome == protocol.ErrorCodeNotFound {
		action = access.AuditDenied
	}
	g.audit().Record(access.AuditEntry{
		Action: action, Member: c.Member.ID, Name: c.Member.Name, From: c.From, Method: method,
		Environment: strings.Join(envs, ","), Outcome: outcome,
	})
}

// setParam returns params with key set to value.
func setParam(params json.RawMessage, key, value string) json.RawMessage {
	m := map[string]any{}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	m[key] = value
	out, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return out
}

// Shaping results: what a member receives is what the daemon sent,
// without what is in environments it cannot see, and below owner without
// what can carry secrets, as for observers: environment variables,
// command arguments and working directories, process and session IDs. A
// command is shown by its program's name.

// redactedKeys are left out of every object below owner.
var redactedKeys = []string{"env", "args", "working_dir", "cwd", "pid", "session_id", "log_dir"}

// shape filters and redacts one result for m.
func (g *memberGuard) shape(ctx context.Context, m access.Member, method string, raw json.RawMessage) (json.RawMessage, error) {
	v, err := decodeAny(raw)
	if err != nil {
		return nil, err
	}
	if method == "task.list" {
		v = g.visiblePlans(ctx, m, v)
	}
	s := shaper{m: m, tabEnv: g.tabEnvironment(ctx)}
	top := ""
	if strings.HasPrefix(method, "environment.") {
		top = "environments" // its records are environments
	}
	return json.Marshal(s.walk(v, "", top))
}

func decodeAny(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// visiblePlans keeps the plans of task.list whose every task is in an
// environment m sees.
func (g *memberGuard) visiblePlans(ctx context.Context, m access.Member, v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := []any{}
	for _, item := range list {
		obj, _ := item.(map[string]any)
		id, _ := obj["id"].(string)
		pl, err := g.tasks.Get(ctx, id)
		if err != nil {
			continue
		}
		envs, err := planEnvironments(pl)
		if err != nil && m.RoleIn(access.AllEnvironments) == access.RoleNone {
			continue
		}
		if !slices.ContainsFunc(envs, func(e string) bool { return m.RoleIn(e) == access.RoleNone }) {
			out = append(out, item)
		}
	}
	return out
}

// tabEnvironment returns a lookup of a tab's environment, read once.
func (g *memberGuard) tabEnvironment(ctx context.Context) func(string) string {
	var tabs map[string]string
	return func(id string) string {
		if tabs == nil {
			tabs = map[string]string{}
			if list, err := g.panes.Tabs(ctx, ""); err == nil {
				for _, t := range list {
					tabs[t.ID] = t.EnvironmentID
				}
			}
		}
		return tabs[id]
	}
}

type shaper struct {
	m      access.Member
	tabEnv func(string) string
}

func (s shaper) sees(env string) bool { return s.m.RoleIn(env) != access.RoleNone }

// envOf is the environment obj belongs to, if it says; in a list of
// environments (key "environments"), its own ID.
func envOf(obj map[string]any, key string) (string, bool) {
	if key == "environments" {
		if id, ok := obj["id"].(string); ok && id != "" {
			return id, true
		}
	}
	for _, k := range []string{"environment_id", "environment"} {
		if id, ok := obj[k].(string); ok && id != "" {
			return id, true
		}
	}
	return "", false
}

// walk shapes v, found under key in environment env ("" for none).
func (s shaper) walk(v any, env, key string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			if obj, ok := item.(map[string]any); ok {
				if e, ok := envOf(obj, key); ok && !s.sees(e) {
					continue
				}
			}
			out = append(out, s.walk(item, env, key))
		}
		return out
	case map[string]any:
		if e, ok := envOf(x, key); ok {
			env = e
		}
		if key == "rollups" {
			s.rollups(x)
		}
		if s.m.RoleIn(env) != roleOwner {
			for _, k := range redactedKeys {
				delete(x, k)
			}
			if c, ok := x["command"]; ok {
				x["command"] = programOnly(c)
			}
		}
		for k, child := range x {
			x[k] = s.walk(child, env, k)
		}
		return x
	}
	return v
}

// rollups keeps the states of the environments and tabs m sees; the
// session's state sums up what it may not see, so it is kept only for a
// member who sees everything.
func (s shaper) rollups(r map[string]any) {
	if envs, ok := r["environments"].(map[string]any); ok {
		for id := range envs {
			if !s.sees(id) {
				delete(envs, id)
			}
		}
	}
	if tabs, ok := r["tabs"].(map[string]any); ok {
		for id := range tabs {
			if !s.sees(s.tabEnv(id)) {
				delete(tabs, id)
			}
		}
	}
	if s.m.RoleIn(access.AllEnvironments) == access.RoleNone {
		delete(r, "session")
	}
}

// programOnly reduces a command (a string, or a list of words) to its
// program's base name.
func programOnly(c any) any {
	switch x := c.(type) {
	case string:
		if f := strings.Fields(x); len(f) > 0 {
			return filepath.Base(filepath.FromSlash(f[0]))
		}
	case []any:
		if len(x) > 0 {
			if s, ok := x[0].(string); ok {
				return []any{filepath.Base(filepath.FromSlash(s))}
			}
		}
		return []any{}
	}
	return c
}

// eventFilter shapes the events m receives: those of environments it
// sees, redacted as results are. Access changes are the user's alone.
func (g *memberGuard) eventFilter(ctx context.Context, m access.Member) event.Filter {
	return g.eventFilterWith(m, shaper{m: m, tabEnv: g.tabEnvironment(context.WithoutCancel(ctx))})
}

func (g *memberGuard) eventFilterWith(m access.Member, s shaper) event.Filter {
	return func(ev event.Event) (event.Event, bool) {
		if strings.HasPrefix(ev.Type, "access.") {
			return ev, false
		}
		if len(ev.Data) == 0 {
			return ev, m.RoleIn(access.AllEnvironments) != access.RoleNone
		}
		v, err := decodeAny(ev.Data)
		if err != nil {
			return ev, false
		}
		if ev.Type == EventPresenceChanged {
			// Everyone's presence: the walk leaves out who is elsewhere.
			data, err := json.Marshal(s.walk(v, "", ""))
			if err != nil {
				return ev, false
			}
			ev.Data = data
			return ev, true
		}
		env := ""
		if obj, ok := v.(map[string]any); ok {
			key := ""
			if strings.HasPrefix(ev.Type, "environment.") {
				key = "environments"
			}
			env, _ = envOf(obj, key)
		}
		if env == "" && m.RoleIn(access.AllEnvironments) == access.RoleNone {
			return ev, false // in no environment it names: only for who sees all
		}
		if env != "" && !s.sees(env) {
			return ev, false
		}
		key := ""
		if strings.HasPrefix(ev.Type, "environment.") {
			key = "environments"
		}
		data, err := json.Marshal(s.walk(v, env, key))
		if err != nil {
			return ev, false
		}
		ev.Data = data
		return ev, true
	}
}
