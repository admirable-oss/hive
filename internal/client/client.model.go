package client

import "time"

// Status is the daemon state reported by runtime.status.
type Status struct {
	Status          string    `json:"status"`
	Socket          string    `json:"socket"`
	StartedAt       time.Time `json:"started_at"`
	PID             int       `json:"pid"`
	Version         string    `json:"version"`
	ProtocolVersion string    `json:"protocol_version"`
}

// Logs is the reply of process.logs.
type Logs struct {
	Logs string `json:"logs"`
	// Truncated means the daemon cut the reply to its size limit; stream the
	// log to read all of it.
	Truncated bool `json:"truncated,omitempty"`
}
