package runtime

import "time"

type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
)

type RuntimeModel struct {
	ID        string
	Status    Status
	Socket    string
	StartedAt time.Time
}
