package process

import (
	"context"
	"encoding/json"

	"github.com/admirable-oss/hive/internal/protocol"
)

func RegisterHandlers(service protocol.Service, processService Service) {
	_ = service.Register("process.start", NewStartHandler(processService))
	_ = service.Register("process.get", NewGetHandler(processService))
	_ = service.Register("process.list", NewListHandler(processService))
	_ = service.Register("process.stop", NewStopHandler(processService))
	_ = service.Register("process.logs", NewLogsHandler(processService))
}

type StartHandler struct {
	service Service
}

func NewStartHandler(service Service) *StartHandler {
	return &StartHandler{service: service}
}

func (h *StartHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params StartRequest
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params"},
		}
	}
	p, err := h.service.Start(ctx, params)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	result, _ := json.Marshal(p)
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}

type GetHandler struct {
	service Service
}

func NewGetHandler(service Service) *GetHandler {
	return &GetHandler{service: service}
}

func (h *GetHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		ID        string `json:"id"`
		ProcessID string `json:"process_id"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params"},
		}
	}
	id := params.ID
	if id == "" {
		id = params.ProcessID
	}
	if id == "" {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "process id is required"},
		}
	}
	p, err := h.service.Get(ctx, id)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	result, _ := json.Marshal(p)
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}

type ListHandler struct {
	service Service
}

func NewListHandler(service Service) *ListHandler {
	return &ListHandler{service: service}
}

func (h *ListHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		EnvironmentID string `json:"environment_id"`
		ID            string `json:"id"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params"},
		}
	}
	envID := params.EnvironmentID
	if envID == "" {
		envID = params.ID
	}
	if envID == "" {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "environment id is required"},
		}
	}
	ps, err := h.service.List(ctx, envID)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	result, _ := json.Marshal(ps)
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}

type StopHandler struct {
	service Service
}

func NewStopHandler(service Service) *StopHandler {
	return &StopHandler{service: service}
}

func (h *StopHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		ID        string `json:"id"`
		ProcessID string `json:"process_id"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params"},
		}
	}
	id := params.ID
	if id == "" {
		id = params.ProcessID
	}
	if id == "" {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "process id is required"},
		}
	}
	err := h.service.Stop(ctx, id)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  json.RawMessage(`{}`),
	}
}

type LogsHandler struct {
	service Service
}

func NewLogsHandler(service Service) *LogsHandler {
	return &LogsHandler{service: service}
}

func (h *LogsHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		ID        string `json:"id"`
		ProcessID string `json:"process_id"`
		Tail      int    `json:"tail"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params"},
		}
	}
	id := params.ID
	if id == "" {
		id = params.ProcessID
	}
	if id == "" {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "process id is required"},
		}
	}
	tail := params.Tail
	if tail <= 0 {
		tail = 50
	}
	logs, err := h.service.Logs(ctx, id, tail)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	result, _ := json.Marshal(map[string]string{"logs": logs})
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}
