package edits

import (
	"context"

	"github.com/admirable-oss/hive/internal/protocol"
)

// Register exposes the service on the wire as agent.files.
func Register(r *protocol.Router, s *Service) {
	r.MustRegister("agent.files", protocol.Method(func(ctx context.Context, p ListRequest) (Files, error) {
		return s.List(ctx, p), nil
	}))
}
