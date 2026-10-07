package environment

import (
	"context"
	"encoding/json"

	"github.com/admirable-oss/hive/internal/protocol"
)

func RegisterHandlers(service protocol.Service, envService Service) {
	service.Register("environment.list", NewListHandler(envService))
	service.Register("environment.create", NewCreateHandler(envService))
	service.Register("environment.get", NewGetHandler(envService))
	service.Register("environment.remove", NewRemoveHandler(envService))
}

func getIDParam(req protocol.Request) (string, bool) {
	if req.Params == nil {
		return "", false
	}
	m, ok := req.Params.(map[string]interface{})
	if !ok {
		return "", false
	}
	id, ok := m["id"].(string)
	return id, ok
}

type ListHandler struct {
	service Service
}

func NewListHandler(service Service) *ListHandler {
	return &ListHandler{service: service}
}

func (h *ListHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	envs, err := h.service.List(ctx)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	result, _ := json.Marshal(envs)
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}

type CreateHandler struct {
	service Service
}

func NewCreateHandler(service Service) *CreateHandler {
	return &CreateHandler{service: service}
}

func (h *CreateHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	id, ok := getIDParam(req)
	if !ok {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params, expected id"},
		}
	}
	env, err := h.service.Create(ctx, id)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	result, _ := json.Marshal(env)
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
	id, ok := getIDParam(req)
	if !ok {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params, expected id"},
		}
	}
	env, err := h.service.Get(ctx, id)
	if err != nil {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "internal_error", Message: err.Error()},
		}
	}
	result, _ := json.Marshal(env)
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}

type RemoveHandler struct {
	service Service
}

func NewRemoveHandler(service Service) *RemoveHandler {
	return &RemoveHandler{service: service}
}

func (h *RemoveHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	id, ok := getIDParam(req)
	if !ok {
		return protocol.Response{
			Version: req.Version,
			Type:    protocol.MessageTypeResponse,
			ID:      req.ID,
			Error:   &protocol.Error{Code: "invalid_params", Message: "invalid params, expected id"},
		}
	}
	err := h.service.Delete(ctx, id)
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
