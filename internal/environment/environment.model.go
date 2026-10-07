package environment

import "time"

type Status string

const StatusReady Status = "ready"

type Environment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"` // workspace directory agents run in
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
