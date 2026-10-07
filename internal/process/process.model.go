package process

import "time"

type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusExited   Status = "exited"
	StatusFailed   Status = "failed"
	StatusKilled   Status = "killed"
)

type Process struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"environment_id"`
	Command       string     `json:"command"`
	Args          []string   `json:"args"`
	WorkingDir    string     `json:"working_dir"`
	PID           int        `json:"pid"`
	Status        Status     `json:"status"`
	ExitCode      *int       `json:"exit_code"`
	Terminal      bool       `json:"terminal"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at"`
}
