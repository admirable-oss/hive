package protocol

import "errors"

var (
	ErrInvalidMethod       = errors.New("protocol: invalid method")
	ErrInvalidRequest      = errors.New("protocol: invalid request")
	ErrMethodExists        = errors.New("protocol: method exists")
	ErrNilHandler          = errors.New("protocol: nil handler")
	ErrMethodAlreadyExists = errors.New("protocol: method already registered")
)

const (
	ErrorCodeInvalidRequest = "invalid_request"
	ErrorCodeUnknownMethod  = "unknown_method"
	ErrorCodeInvalidParams  = "invalid_params"
	ErrorCodeInternal       = "internal_error"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
