package protocol

type Response struct {
	Version string      `json:"version"`
	Type    MessageType `json:"type"`
	ID      string      `json:"id"`
	Result  any         `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}
