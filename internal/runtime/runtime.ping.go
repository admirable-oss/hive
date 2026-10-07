package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/protocol"
)

type PingHandler struct{}

func NewPingHandler() protocol.Handler {
	return PingHandler{}
}

func (PingHandler) Handle(
	ctx context.Context,
	request protocol.Request,
) protocol.Response {
	return protocol.Response{
		Version: protocol.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      request.ID,
		Result: protocol.MustEncodeResult(map[string]any{
			"pong": true,
		}),
	}
}
