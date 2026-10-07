package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/protocol"
)

type ShutdownHandler struct {
	controller Controller
}

func NewShutdownHandler(controller Controller) protocol.Handler {
	return &ShutdownHandler{
		controller: controller,
	}
}

func (h *ShutdownHandler) Handle(
	ctx context.Context,
	request protocol.Request,
) protocol.Response {
	if err := h.controller.Stop(ctx); err != nil {
		return protocol.Response{
			Version: protocol.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      request.ID,
			Error: &protocol.Error{
				Code:    protocol.ErrorCodeInternal,
				Message: "failed to stop runtime",
			},
		}
	}
	return protocol.Response{
		Version: protocol.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      request.ID,
		Result: protocol.MustEncodeResult(map[string]any{
			"stopped": true,
		}),
	}
}
