package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/protocol"
)

// Register exposes the daemon itself on the wire as runtime.*.
func Register(r *protocol.Router, s *Server) {
	r.MustRegister("runtime.ping", protocol.Method(func(context.Context, struct{}) (map[string]bool, error) {
		return map[string]bool{"pong": true}, nil
	}))
	r.MustRegister("runtime.status", protocol.Method(func(context.Context, struct{}) (Snapshot, error) {
		return s.Snapshot(), nil
	}))
	r.MustRegister("runtime.shutdown", protocol.HandlerFunc(func(ctx context.Context, req protocol.Request) protocol.Response {
		ctx, cancel := context.WithTimeout(ctx, stopTimeout)
		defer cancel()
		// Agents are stopped unless the caller asks to keep them (a daemon
		// restart); protocol-1 clients send no params and get the old
		// behaviour.
		params := shutdownParams{StopAgents: true}
		if err := protocol.DecodeParams(req, &params); err != nil {
			return protocol.Fail(req, err)
		}
		resp := protocol.Reply(req, map[string]bool{"stopped": true, "agents_kept": !params.StopAgents && s.cfg.Shim != nil})
		if err := s.shutdown(ctx, params.StopAgents); err != nil {
			resp = protocol.Fail(req, err)
		}
		// Signal Done only after the reply is flushed; otherwise the daemon
		// could exit while the caller is still waiting for its answer.
		resp.AfterSend = s.markDone
		return resp
	}))
}

type shutdownParams struct {
	StopAgents bool `json:"stop_agents"`
}
