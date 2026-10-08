package process

import (
	"strings"
	"time"
	"unicode"
)

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

// Active reports whether the process is (or is about to be) running.
func (p Process) Active() bool {
	return p.Status == StatusRunning || p.Status == StatusStarting
}

// DisplayName is a short human label: the first plain argument (for example
// the name in `sh -c <script> <name>`), falling back to the command. Arguments
// without a letter (`sleep 30`) are values, not names.
func (p Process) DisplayName() string {
	for _, arg := range p.Args {
		if arg != "" && !strings.HasPrefix(arg, "-") && !strings.ContainsAny(arg, ";\n") && len(arg) < 30 &&
			strings.ContainsFunc(arg, unicode.IsLetter) {
			return arg
		}
	}
	return p.Command
}

// StartRequest is what a client sends to launch a process.
type StartRequest struct {
	EnvironmentID string   `json:"environment_id"`
	Command       string   `json:"command"`
	Args          []string `json:"args"`
	// Terminal starts the process inside a PTY; Width and Height size it.
	Terminal bool   `json:"terminal"`
	Width    uint16 `json:"width"`
	Height   uint16 `json:"height"`
}

// Stream names one of a process's output logs.
type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

// LogsRequest selects part of a process's output.
type LogsRequest struct {
	ID string `json:"id"`
	// Stream is stdout (the default) or stderr.
	Stream Stream `json:"stream,omitempty"`
	// Tail limits the output to the last Tail lines. For process.logs a value
	// of zero or less means DefaultTail; when streaming it means everything.
	Tail int `json:"tail,omitempty"`
	// Follow keeps a stream open, sending new output until the process ends.
	Follow bool `json:"follow,omitempty"`
}

// normalize applies the default stream and rejects unknown ones.
func (r LogsRequest) normalize() (LogsRequest, error) {
	switch r.Stream {
	case "":
		r.Stream = StreamStdout
	case StreamStdout, StreamStderr:
	default:
		return r, ErrInvalidStream
	}
	return r, nil
}
