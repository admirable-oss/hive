package protocol

import "errors"

var (
	ErrInvalidMethod       = errors.New("protocol: invalid method")
	ErrNilHandler          = errors.New("protocol: nil handler")
	ErrMethodAlreadyExists = errors.New("protocol: method already registered")
	ErrMessageTooLarge     = errors.New("protocol: message too large")
	ErrInvalidMessage      = errors.New("protocol: invalid message")
	ErrVersionMismatch     = errors.New("protocol: version mismatch")
)

// Error codes sent on the wire. Clients branch on the code, not the message.
const (
	ErrorCodeInvalidRequest = "invalid_request"
	ErrorCodeUnknownMethod  = "unknown_method"
	ErrorCodeInvalidParams  = "invalid_params"
	ErrorCodeNotFound       = "not_found"
	ErrorCodeInternal       = "internal_error"
	// ErrorCodeUnsupportedVersion answers a request stamped with a protocol
	// version this daemon does not speak. The connection stays open.
	ErrorCodeUnsupportedVersion = "unsupported_version"
	// ErrorCodeUnsupported answers a request the server understands but
	// cannot serve on this connection or build (e.g. a pipe over protocol 1
	// that needs framing).
	ErrorCodeUnsupported = "unsupported"
	// ErrorCodeUnavailable means the target exists but cannot answer now
	// (e.g. an agent whose terminal is gone).
	ErrorCodeUnavailable = "unavailable"
)

// Error is the error payload of a Response. It is also a Go error, so domain
// code can return one to choose the wire code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// NewError tags err with a wire code.
func NewError(code string, err error) *Error {
	return &Error{Code: code, Message: err.Error()}
}

// AsError returns the *Error in err's chain, or wraps err as internal_error.
func AsError(err error) *Error {
	if pe, ok := errors.AsType[*Error](err); ok {
		return pe
	}
	return NewError(ErrorCodeInternal, err)
}
