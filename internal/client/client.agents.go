package client

import (
	"context"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
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
