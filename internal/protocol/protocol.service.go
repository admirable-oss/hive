package protocol

import "context"

type Handler interface {
	Handle(context.Context, Request) Response
}

type Service interface {
	Register(string, Handler) error
	Handle(context.Context, Request) Response
}

type service struct {
	handlers map[string]Handler
}

func NewService() Service {
	return &service{
		handlers: make(map[string]Handler),
	}
}

func (s *service) Register(method string, handler Handler) error {
	if method == "" {
		return ErrInvalidMethod
	}

	if handler == nil {
		return ErrNilHandler
	}

	if _, exists := s.handlers[method]; exists {
		return ErrMethodAlreadyExists
	}

	s.handlers[method] = handler

	return nil
}

func (s *service) Handle(
	ctx context.Context,
	request Request,
) Response {
	handler, exists := s.handlers[request.Method]

	if !exists {
		return Response{
			Version: Version,
			Type:    MessageTypeResponse,
			ID:      request.ID,
			Error: &Error{
				Code:    ErrorCodeUnknownMethod,
				Message: "unknown method",
			},
		}
	}

	return handler.Handle(ctx, request)
}
