package client

import "time"

// Status is the daemon state reported by runtime.status.
type Status struct {
	Status    string    `json:"status"`
	Socket    string    `json:"socket"`
	StartedAt time.Time `json:"started_at"`
}
