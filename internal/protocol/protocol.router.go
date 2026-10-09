package protocol

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
)

// Handler answers one request. It is the only contract a domain package must
// satisfy to be reachable over the wire.
type Handler interface {
	Handle(context.Context, Request) Response
}

// HandlerFunc lets a plain function act as a Handler.
type HandlerFunc func(context.Context, Request) Response

func (f HandlerFunc) Handle(ctx context.Context, req Request) Response { return f(ctx, req) }

// Router dispatches requests to handlers by method name. Register everything
// before serving: the map is not locked because it is read-only afterwards.
type Router struct {
	handlers map[string]Handler
}

func NewRouter() *Router {
	return &Router{handlers: make(map[string]Handler)}
}

func (r *Router) Register(method string, h Handler) error {
	switch {
	case method == "":
		return ErrInvalidMethod
	case h == nil:
		return ErrNilHandler
	}
	if _, exists := r.handlers[method]; exists {
		return ErrMethodAlreadyExists
	}
	r.handlers[method] = h
	return nil
}

// Methods lists the registered method names, sorted. A protocol-2 server
// sends them in its welcome so clients can tell what it supports.
func (r *Router) Methods() []string {
	return slices.Sorted(maps.Keys(r.handlers))
}

// MustRegister is Register for startup wiring, where a failure is a bug.
func (r *Router) MustRegister(method string, h Handler) {
	if err := r.Register(method, h); err != nil {
		panic(err.Error() + ": " + method)
	}
}

func (r *Router) Handle(ctx context.Context, req Request) Response {
	h, ok := r.handlers[req.Method]
	if !ok {
		return Fail(req, &Error{Code: ErrorCodeUnknownMethod, Message: "unknown method " + req.Method})
	}
	return h.Handle(ctx, req)
}

// Method adapts a typed function into a Handler: params decode into P (absent
// params leave P zero), the result is encoded as JSON, and errors become error
// responses. Handlers then read like ordinary Go functions.
func Method[P, R any](fn func(context.Context, P) (R, error)) Handler {
	return HandlerFunc(func(ctx context.Context, req Request) Response {
		var params P
		if err := DecodeParams(req, &params); err != nil {
			return Fail(req, err)
		}
		result, err := fn(ctx, params)
		if err != nil {
			return Fail(req, err)
		}
		return Reply(req, result)
	})
}

// DecodeParams decodes req.Params into v for handlers that cannot use Method
// (for example because they hijack the connection).
func DecodeParams(req Request, v any) error {
	if len(req.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Params, v); err != nil {
		return NewError(ErrorCodeInvalidParams, err)
	}
	return nil
}

// PipeMethod adapts a typed function that opens a byte pipe: params decode
// into P, the result R is sent as the response, then the returned PipeFunc
// serves the pipe. An error means no pipe is opened.
func PipeMethod[P, R any](fn func(context.Context, P) (R, PipeFunc, error)) Handler {
	return HandlerFunc(func(ctx context.Context, req Request) Response {
		var params P
		if err := DecodeParams(req, &params); err != nil {
			return Fail(req, err)
		}
		result, pipe, err := fn(ctx, params)
		if err != nil {
			return Fail(req, err)
		}
		resp := Reply(req, result)
		if resp.Error == nil {
			resp.Pipe = pipe
		}
		return resp
	})
}
