package protocol

const (
	Version = "1"
)

type MessageType string

type Metadata struct {
	Version string      `json:"version"`
	Type    MessageType `json:"type"`
}

const (
	MessageTypeRequest  MessageType = "request"
	MessageTypeResponse MessageType = "response"
)
