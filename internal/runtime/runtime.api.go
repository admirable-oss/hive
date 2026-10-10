package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
)

// APISnapshot is the whole workspace in one reply: what `hive api
// snapshot` prints, for tools that would otherwise make a call per kind.
type APISnapshot struct {
	Daemon       Snapshot                  `json:"daemon"`
	Environments []environment.Environment `json:"environments"`
	Tabs         []pane.Tab                `json:"tabs"`
	Panes        []pane.Pane               `json:"panes"`
	Processes    []process.Process         `json:"processes"`
	Agents       []agent.Agent             `json:"agents"`
	Rollups      agent.Rollups             `json:"rollups"`
}

// registerAPI adds api.snapshot and api.schema. It goes last: the schema
// describes every method registered before it.
func registerAPI(r *protocol.Router, s *Server, envs environment.Service, panes *pane.Service, procs process.Service, agents *agent.Service) {
	r.MustRegister("api.snapshot", protocol.Method(func(ctx context.Context, _ struct{}) (APISnapshot, error) {
		var snap APISnapshot
		var err error
		snap.Daemon = s.Snapshot()
		if snap.Environments, err = envs.List(ctx); err != nil {
			return snap, err
		}
		if snap.Tabs, err = panes.Tabs(ctx, ""); err != nil {
			return snap, err
		}
		if snap.Panes, err = panes.Panes(ctx, "", ""); err != nil {
			return snap, err
		}
		if snap.Processes, err = procs.List(ctx, ""); err != nil {
			return snap, err
		}
		if snap.Agents, err = agents.List(ctx, true); err != nil {
			return snap, err
		}
		snap.Rollups = agent.RollupOf(snap.Agents)
		snap.Environments = nonNil(snap.Environments)
		snap.Tabs = nonNil(snap.Tabs)
		snap.Panes = nonNil(snap.Panes)
		snap.Processes = nonNil(snap.Processes)
		snap.Agents = nonNil(snap.Agents)
		return snap, nil
	}))
	r.MustRegister("api.schema", protocol.Method(func(context.Context, struct{}) (protocol.Schema, error) {
		return r.Schema(), nil
	}))
}

// nonNil makes empty lists encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
