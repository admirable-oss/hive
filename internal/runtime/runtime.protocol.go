package runtime

import (
	"context"
	"net"

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

		resp := protocol.Reply(req, map[string]bool{"stopped": true})
		if err := s.shutdown(ctx); err != nil {
			resp = protocol.Fail(req, err)
		}
		// Signal Done only after the reply is flushed; otherwise the daemon
		// could exit while the caller is still waiting for its answer.
		resp.Hijack = func(context.Context, net.Conn) { s.markDone() }
		return resp
	}))
}
