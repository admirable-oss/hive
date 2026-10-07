package client

import "time"

type Status struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Socket    string    `json:"socket"`
	StartedAt time.Time `json:"started_at"`
}
