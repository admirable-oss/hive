package runtime

import (
	"context"

	"github.com/admirable-oss/hive/internal/protocol"
)

type StatusHandler struct {
	reader Reader
}

func NewStatusHandler(reader Reader) protocol.Handler {
	return &StatusHandler{
		reader: reader,
	}
}

func (h *StatusHandler) Handle(
	ctx context.Context,
	request protocol.Request,
) protocol.Response {
	model, err := h.reader.Runtime(ctx)
	if err != nil {
		return protocol.Response{
			Version: protocol.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      request.ID,
			Error: &protocol.Error{
				Code:    protocol.ErrorCodeInternal,
				Message: "internal error",
			},
		}
	}

	var startedAt string
	if !model.StartedAt.IsZero() {
		startedAt = model.StartedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return protocol.Response{
		Version: protocol.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      request.ID,
		Result: map[string]any{
			"id":         model.ID,
			"status":     string(model.Status),
			"socket":     model.Socket,
			"started_at": startedAt,
		},
	}
}
