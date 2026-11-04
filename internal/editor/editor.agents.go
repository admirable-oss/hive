package editor

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// stateOrder puts the agents that need a person first.
var stateOrder = map[string]int{"blocked": 0, "working": 1, "done": 2, "idle": 3, "unknown": 4, "exited": 5}

func (s *Server) registerAgents(m func(string, protocol.Handler)) {
	m("agent.list", protocol.Method(s.listAgents))
	m("agent.kinds", protocol.Method(s.kinds))
	m("agent.start", protocol.Method(s.start))
	m("agent.prompt", protocol.Method(s.prompt))
	m("agent.keys", protocol.Method(func(ctx context.Context, p KeysParams) (Empty, error) {
		if len(p.Keys) == 0 {
			return Empty{}, invalid("no keys to send")
		}
		return Empty{}, s.hive.SendKeys(ctx, p.ID, p.Keys...)
	}))
	m("agent.stop", protocol.Method(func(ctx context.Context, p IDParams) (Empty, error) {
		_, err := s.hive.StopAgent(ctx, p.ID)
		return Empty{}, err
	}))
	m("agent.read", protocol.Method(s.read))
	m("task.list", protocol.Method(s.listTasks))
	m("task.approve", protocol.Method(func(ctx context.Context, p ApproveParams) (Empty, error) {
		s.mu.Lock()
		by := s.client.Name
		s.mu.Unlock()
		_, err := s.hive.ApproveTask(ctx, p.Plan, p.Task, hiveapi.ApproveOptions{Deny: p.Deny, Note: p.Note, By: by})
		return Empty{}, err
	}))
}

// view describes an agent for the editor.
func (c *catalog) agent(a hiveapi.Agent) Agent {
	v := Agent{
		ID: a.ID, PaneID: a.PaneID, Name: cmp.Or(a.Name, a.Kind), Kind: a.Kind, State: string(a.State),
		Since: a.Since, Reason: a.Reason, Attention: a.State == hiveapi.StateBlocked, EnvironmentID: a.EnvironmentID,
	}
	if v.State == "" {
		v.State = "unknown"
	}
	if e, ok := c.envs[a.EnvironmentID]; ok {
		v.Dir = e.Path
		if e.Worktree != nil {
			v.Branch, v.Repo = e.Worktree.Branch, e.Worktree.Repo
		}
	}
	if b, ok := c.byEnv[a.EnvironmentID]; ok {
		v.Branch, v.Repo = b.Name, b.Repo
	}
	if t, ok := c.tasks[a.ID]; ok {
		v.Task = &t
	}
	return v
}

// concerns reports whether an agent works in one of the folders: in it,
// above it, or in a worktree of its repository.
func concerns(a Agent, folders []string) bool {
	for _, f := range folders {
		if within(a.Dir, f) || within(f, a.Dir) || within(a.Repo, f) || (a.Repo != "" && within(f, a.Repo)) {
			return true
		}
	}
	return false
}

func sortAgents(list []Agent) {
	slices.SortStableFunc(list, func(a, b Agent) int {
		return cmp.Or(
			cmp.Compare(rank(a.State), rank(b.State)),
			b.Since.Compare(a.Since),
			strings.Compare(a.Name, b.Name),
			strings.Compare(a.ID, b.ID),
		)
	})
}

func rank(state string) int {
	if r, ok := stateOrder[state]; ok {
		return r
	}
	return len(stateOrder)
}

func (s *Server) listAgents(ctx context.Context, p AgentListParams) (AgentList, error) {
	list, err := s.hive.Agents(ctx, false)
	if err != nil {
		return AgentList{}, unavailable(err)
	}
	c := s.catalog(ctx)
	folders := s.foldersFor(p.Folder)
	out := []Agent{}
	for _, a := range list {
		v := c.agent(a)
		if p.All || len(folders) == 0 || concerns(v, folders) {
			out = append(out, v)
		}
	}
	sortAgents(out)
	return AgentList{Agents: out}, nil
}

func (s *Server) kinds(ctx context.Context, _ Empty) (KindList, error) {
	ms, err := s.hive.Manifests(ctx)
	if err != nil {
		return KindList{}, unavailable(err)
	}
	out := []Kind{}
	for _, m := range ms {
		k := Kind{Kind: m.ID, Name: cmp.Or(m.Name, m.ID)}
		if len(m.Start.Command) > 0 {
			_, err := s.cfg.LookPath(m.Start.Command[0])
			k.Installed = err == nil
		}
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b Kind) int {
		if a.Installed != b.Installed {
			if a.Installed {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	return KindList{Kinds: out}, nil
}

// envIDPattern is what an environment ID may contain.
var envIDPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// environmentFor finds the environment of a folder, or makes one: the one
// whose directory it is (a person's own, before a worktree), else the
// nearest one above it.
func (s *Server) environmentFor(ctx context.Context, folder string) (hiveapi.Environment, error) {
	dir := cleanPath(folder)
	if dir == "" {
		return hiveapi.Environment{}, invalid("folder %q is not an absolute path", folder)
	}
	envs, err := s.hive.Environments(ctx)
	if err != nil {
		return hiveapi.Environment{}, unavailable(err)
	}
	// The folder's own environment (a person's rather than a worktree's),
	// else the nearest one above it.
	var exact, near *hiveapi.Environment
	for i := range envs {
		e := &envs[i]
		p := cleanPath(e.Path)
		switch {
		case p == dir:
			if exact == nil || (exact.Worktree != nil && e.Worktree == nil) {
				exact = e
			}
		case e.Worktree == nil && within(dir, p):
			if near == nil || len(p) > len(cleanPath(near.Path)) {
				near = e
			}
		}
	}
	if exact != nil {
		return *exact, nil
	}
	if near != nil {
		return *near, nil
	}
	taken := map[string]bool{}
	for _, e := range envs {
		taken[e.ID] = true
	}
	base := strings.Trim(envIDPattern.ReplaceAllString(filepath.Base(dir), "-"), "-._")
	if base == "" {
		base = "editor"
	}
	if len(base) > 50 {
		base = base[:50]
	}
	id := base
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	s.forget()
	return s.hive.CreateEnvironment(ctx, id, dir, nil)
}

// promptReady bounds how long agent.start waits for a new agent to take
// its first prompt.
const promptReady = 2 * time.Minute

func (s *Server) start(ctx context.Context, p StartParams) (Started, error) {
	if p.Kind == "" {
		return Started{}, invalid("name the agent kind")
	}
	env, err := s.environmentFor(ctx, p.Folder)
	if err != nil {
		return Started{}, err
	}
	started, err := s.hive.StartAgent(ctx, hiveapi.StartAgent{
		Kind: p.Kind, EnvironmentID: env.ID, Worktree: p.Worktree, Isolate: p.Isolate && p.Worktree == "", Base: p.Base, Name: p.Name,
	})
	if err != nil {
		return Started{}, err
	}
	s.forget()
	out := Started{Agent: s.catalog(ctx).agent(started.Agent)}
	if p.Prompt != nil && (p.Prompt.Text != "" || len(p.Prompt.Attachments) > 0) {
		text := composePrompt(*p.Prompt, out.Agent)
		res, err := s.hive.Prompt(ctx, started.Agent.ID, text, hiveapi.PromptOptions{StartTimeout: promptReady})
		if err != nil {
			return out, fmt.Errorf("agent %s started, but its prompt was not sent: %w", started.Agent.ID, err)
		}
		out.Outcome = string(res.Outcome)
	}
	return out, nil
}

func (s *Server) prompt(ctx context.Context, p PromptParams) (PromptResult, error) {
	if p.Prompt.Text == "" && len(p.Prompt.Attachments) == 0 {
		return PromptResult{}, invalid("the prompt is empty")
	}
	a, err := s.hive.Agent(ctx, p.ID)
	if err != nil {
		return PromptResult{}, err
	}
	text := composePrompt(p.Prompt, s.catalog(ctx).agent(a))
	res, err := s.hive.Prompt(ctx, a.ID, text, hiveapi.PromptOptions{StartTimeout: promptReady})
	if err != nil {
		return PromptResult{}, err
	}
	return PromptResult{Outcome: string(res.Outcome), Text: text}, nil
}

func (s *Server) read(ctx context.Context, p ReadParams) (ReadResult, error) {
	a, err := s.hive.Agent(ctx, p.ID)
	if err != nil {
		return ReadResult{}, err
	}
	lines, err := s.hive.Read(ctx, a.ID, hiveapi.ReadOptions{Lines: p.Lines})
	if err != nil {
		return ReadResult{}, err
	}
	r := newResolver(s.catalog(ctx).agent(a).Dir)
	var links []Link
	for y, l := range lines {
		links = append(links, r.find(y, l)...)
	}
	return ReadResult{Lines: lines, Links: links}, nil
}

func (s *Server) listTasks(ctx context.Context, _ TaskListParams) (TaskList, error) {
	plans, err := s.hive.Plans(ctx)
	if err != nil {
		return TaskList{}, unavailable(err)
	}
	slices.SortFunc(plans, func(a, b hiveapi.PlanSummary) int {
		return cmp.Or(
			cmp.Compare(planRank(a), planRank(b)),
			b.CreatedAt.Compare(a.CreatedAt),
		)
	})
	out := []PlanView{}
	for i, p := range plans {
		if i >= maxPlans && p.State != hiveapi.PlanRunning {
			break
		}
		full, err := s.hive.Plan(ctx, p.ID)
		if err != nil {
			continue
		}
		out = append(out, planView(full))
	}
	return TaskList{Plans: out}, nil
}

// maxPlans bounds the finished plans task.list returns.
const maxPlans = 20

func planRank(p hiveapi.PlanSummary) int {
	switch {
	case p.State == hiveapi.PlanRunning && len(p.Attention) > 0:
		return 0
	case p.State == hiveapi.PlanRunning:
		return 1
	}
	return 2
}

func planView(p hiveapi.Plan) PlanView {
	v := PlanView{ID: p.ID, Name: p.Name, State: string(p.State), CreatedAt: p.CreatedAt, Tasks: []TaskView{}}
	for _, t := range p.Tasks {
		tv := TaskView{
			ID: t.Spec.ID, Title: t.Spec.Title, Kind: t.Spec.Kind, State: string(t.State),
			AgentID: t.AgentID, PaneID: t.PaneID, Environment: t.EnvironmentID, Reason: t.Reason,
			Attention: t.State == hiveapi.TaskApproval || t.State == hiveapi.TaskBlocked,
		}
		v.Attention = v.Attention || tv.Attention
		v.Tasks = append(v.Tasks, tv)
	}
	slices.SortStableFunc(v.Tasks, func(a, b TaskView) int {
		if a.Attention != b.Attention {
			if a.Attention {
				return -1
			}
			return 1
		}
		return 0
	})
	return v
}
