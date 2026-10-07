package protocol

import "encoding/json"

type Response struct {
	Version string          `json:"version"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}
