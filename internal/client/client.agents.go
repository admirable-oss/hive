package client

import (
	"context"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
)

// Agents is the typed agent API (agent.*) over any Client.
type Agents struct {
	c Client
}

// NewAgents wraps c.
func NewAgents(c Client) Agents { return Agents{c: c} }

// AgentReport is what an agent says about itself; see AgentReport.
type AgentReport struct {
	State     agent.State
	TTL       time.Duration // 0: the daemon's default
	SessionID string
	Message   string
}

func agentCall[R any](ctx context.Context, a Agents, method string, params any) (R, error) {
	var r R
	err := a.c.Call(ctx, method, params, &r)
	return r, err
}

// List returns the agents and their rollups; all includes terminal
// processes no manifest recognises.
func (a Agents) List(ctx context.Context, all bool) (agent.ListResult, error) {
	return agentCall[agent.ListResult](ctx, a, "agent.list", map[string]bool{"all": all})
}

// Get returns one agent, by pane or process ID.
func (a Agents) Get(ctx context.Context, id string) (agent.Agent, error) {
	return agentCall[agent.Agent](ctx, a, "agent.get", map[string]string{"id": id})
}

// Explain shows the evidence behind an agent's state.
func (a Agents) Explain(ctx context.Context, id string) (agent.Explanation, error) {
	return agentCall[agent.Explanation](ctx, a, "agent.explain", map[string]string{"id": id})
}

// Report records what the agent in pane or process id says about itself.
func (a Agents) Report(ctx context.Context, id string, r AgentReport) (agent.Agent, error) {
	return agentCall[agent.Agent](ctx, a, "agent.report", map[string]any{
		"id": id, "state": r.State, "ttl_ms": r.TTL.Milliseconds(),
		"session_id": r.SessionID, "message": r.Message,
	})
}

// Seen marks an agent looked at: done becomes idle.
func (a Agents) Seen(ctx context.Context, id string) error {
	return a.c.Call(ctx, "agent.seen", map[string]string{"id": id}, nil)
}

// Manifests returns the daemon's agent manifests.
func (a Agents) Manifests(ctx context.Context) ([]agent.Manifest, error) {
	return agentCall[[]agent.Manifest](ctx, a, "agent.manifests", struct{}{})
}

// Reload makes the daemon read the agent manifests again.
func (a Agents) Reload(ctx context.Context) (agent.ReloadResult, error) {
	return agentCall[agent.ReloadResult](ctx, a, "agent.reload", struct{}{})
}

// AgentStartRequest starts an agent by its manifest; see agent.start.
type AgentStartRequest struct {
	Kind          string `json:"kind"`
	EnvironmentID string `json:"environment_id"`
	// Worktree, when set, is a branch to start the agent on, in a new
	// worktree of the environment's repository with its own environment.
	Worktree string            `json:"worktree,omitempty"`
	Base     string            `json:"base,omitempty"`
	Name     string            `json:"name,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// AgentStarted is what Start returns.
type AgentStarted struct {
	Agent       agent.Agent             `json:"agent"`
	Tab         pane.Tab                `json:"tab"`
	Pane        pane.Pane               `json:"pane"`
	Environment environment.Environment `json:"environment"`
}

// PromptOptions tune a prompt; see agent.PromptRequest.
type PromptOptions struct {
	Wait  bool
	Until agent.Until // done (default) or idle
	// Timeout bounds the whole prompt; StartTimeout how long the agent gets
	// to react; StallTimeout how long it may work with a still screen
	// (negative: never). Zero uses the daemon's defaults (no timeout, 20s,
	// 10m).
	Timeout, StartTimeout, StallTimeout time.Duration
	// Read returns that many of the agent's last lines with the result.
	Read int
}

// WaitOptions say what Wait waits for; see agent.WaitRequest.
type WaitOptions struct {
	Until   agent.Until
	After   *uint64
	Timeout time.Duration
	Stall   time.Duration
}

// Start starts an agent in a new tab (and, with Worktree, a new worktree).
func (a Agents) Start(ctx context.Context, req AgentStartRequest) (AgentStarted, error) {
	return agentCall[AgentStarted](ctx, a, "agent.start", req)
}

// Prompt sends an agent a prompt.
func (a Agents) Prompt(ctx context.Context, id, text string, o PromptOptions) (agent.PromptResult, error) {
	return agentCall[agent.PromptResult](ctx, a, "agent.prompt", map[string]any{
		"id": id, "text": text, "wait": o.Wait, "until": o.Until, "read": o.Read,
		"timeout_ms": o.Timeout.Milliseconds(), "start_timeout_ms": o.StartTimeout.Milliseconds(),
		"stall_timeout_ms": o.StallTimeout.Milliseconds(),
	})
}

// Wait waits for an agent's state.
func (a Agents) Wait(ctx context.Context, id string, o WaitOptions) (agent.WaitResult, error) {
	return agentCall[agent.WaitResult](ctx, a, "agent.wait", map[string]any{
		"id": id, "until": o.Until, "after": o.After,
		"timeout_ms": o.Timeout.Milliseconds(), "stall_ms": o.Stall.Milliseconds(),
	})
}

// Read returns an agent's text.
func (a Agents) Read(ctx context.Context, id string, req ReadRequest) ([]string, error) {
	return agentCall[[]string](ctx, a, "agent.read", map[string]any{
		"id": id, "source": req.Source, "lines": req.Lines, "ansi": req.ANSI,
	})
}

// SendKeys types named keys into an agent.
func (a Agents) SendKeys(ctx context.Context, id string, keys []string) error {
	return a.c.Call(ctx, "agent.send_keys", map[string]any{"id": id, "keys": keys}, nil)
}

// Rename renames an agent's pane.
func (a Agents) Rename(ctx context.Context, id, name string) (agent.Agent, error) {
	return agentCall[agent.Agent](ctx, a, "agent.rename", map[string]string{"id": id, "name": name})
}

// Focus shows an agent's pane in every client.
func (a Agents) Focus(ctx context.Context, id string) (agent.Agent, error) {
	return agentCall[agent.Agent](ctx, a, "agent.focus", map[string]string{"id": id})
}

// Stop stops an agent's process; its pane stays.
func (a Agents) Stop(ctx context.Context, id string) (agent.Agent, error) {
	return agentCall[agent.Agent](ctx, a, "agent.stop", map[string]string{"id": id})
}

// APISnapshot is the whole workspace in one reply (api.snapshot).
type APISnapshot struct {
	Daemon       Status                    `json:"daemon"`
	Environments []environment.Environment `json:"environments"`
	Tabs         []pane.Tab                `json:"tabs"`
	Panes        []pane.Pane               `json:"panes"`
	Processes    []process.Process         `json:"processes"`
	Agents       []agent.Agent             `json:"agents"`
	Rollups      agent.Rollups             `json:"rollups"`
}

// Snapshot reads the whole workspace at once.
func (a Agents) Snapshot(ctx context.Context) (APISnapshot, error) {
	return agentCall[APISnapshot](ctx, a, "api.snapshot", struct{}{})
}

// Schema describes the daemon's API (api.schema).
func (a Agents) Schema(ctx context.Context) (protocol.Schema, error) {
	return agentCall[protocol.Schema](ctx, a, "api.schema", struct{}{})
}
