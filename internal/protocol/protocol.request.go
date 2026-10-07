package protocol

type Request struct {
	Version string      `json:"version"`
	Type    MessageType `json:"type"`
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Params  any         `json:"params,omitempty"`
}
