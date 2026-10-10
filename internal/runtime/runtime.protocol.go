package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/persist"

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

// registerSession exposes snapshots, restore and clean-up.
func registerSession(r *protocol.Router, k *keeper) {
	r.MustRegister("session.save", protocol.Method(func(ctx context.Context, _ struct{}) (persist.SaveResult, error) {
		if k.saver == nil {
			return persist.SaveResult{}, errSnapshotsOff
		}
		wrote, err := k.saver.Save(ctx)
		return persist.SaveResult{Path: k.store.Path(), Written: wrote}, err
	}))
	r.MustRegister("session.restore", protocol.Method(func(ctx context.Context, req persist.RestoreRequest) (persist.RestoreReport, error) {
		resume := k.cfg == nil || k.cfg.ResumeAgents
		if req.ResumeAgents != nil {
			resume = *req.ResumeAgents
		}
		return k.restore(ctx, resume)
	}))
	r.MustRegister("runtime.gc", protocol.Method(k.gc))
}

type shutdownParams struct {
	StopAgents bool `json:"stop_agents"`
}
