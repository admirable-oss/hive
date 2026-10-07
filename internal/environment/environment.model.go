package environment

import "time"

type Status string

const (
	StatusCreated Status = "created"
	StatusReady   Status = "ready"
	StatusStopped Status = "stopped"
)

type Environment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
