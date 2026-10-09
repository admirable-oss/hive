package shim

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/terminal"
)

// Spec is what a shim runs. The daemon writes it before starting the shim.
type Spec struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	Args       []string `json:"args"`
	WorkingDir string   `json:"working_dir"`
	Env        []string `json:"env,omitempty"` // nil inherits the shim's environment
	// Terminal runs the agent in a PTY with a terminal emulator. Otherwise
	// it runs plainly with its output appended to the log files.
	Terminal        bool          `json:"terminal"`
	Size            terminal.Size `json:"size"`
	StdoutPath      string        `json:"stdout_path"`
	StderrPath      string        `json:"stderr_path,omitempty"`
	ScrollbackBytes int           `json:"scrollback_bytes,omitempty"`
	StopGrace       time.Duration `json:"stop_grace,omitempty"`
}

// Status is a shim's lifecycle stage.
type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	// StatusExited: the agent has exited; ExitCode is set. The shim lingers
	// until the daemon has collected the result, or a timeout.
	StatusExited Status = "exited"
	// StatusFailed: the agent could not be started; Error says why.
	StatusFailed Status = "failed"
)

// State is the shim's record of its agent, kept in state.json and returned
// by shim.status.
type State struct {
	Schema    int        `json:"schema"`
	Status    Status     `json:"status"`
	ShimPID   int        `json:"shim_pid"`
	PID       int        `json:"pid,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// ExitCode is the agent's exit code, -1 when a signal ended it.
	ExitCode *int   `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
	// Width and Height are the terminal's current size (terminal agents).
	Width  uint16 `json:"width,omitempty"`
	Height uint16 `json:"height,omitempty"`
}

const stateSchema = 1

// Exit describes how an agent ended. It is an error whose ExitCode the
// process supervisor reads, like *exec.ExitError.
type Exit struct {
	Code int
}

func (e *Exit) Error() string {
	if e.Code == -1 {
		return "agent killed by a signal"
	}
	return fmt.Sprintf("agent exited with status %d", e.Code)
}

// ExitCode returns the exit code, -1 for a signal.
func (e *Exit) ExitCode() int { return e.Code }

var (
	// ErrNoShim means there is no shim directory for the ID: the agent was
	// not started under a shim (or its directory was cleaned up).
	ErrNoShim = errors.New("shim: no shim for this agent")
	// ErrShimGone means the shim's directory exists but the shim does not
	// answer: it crashed, was killed, or the machine rebooted.
	ErrShimGone = errors.New("shim: the agent's shim is gone")
	// ErrNotTerminal is returned for terminal calls on a plain agent.
	ErrNotTerminal = errors.New("shim: the agent has no terminal")
	// ErrDetached is returned by Wait after the daemon let go of the shim.
	ErrDetached = errors.New("shim: detached")
)

func specPath(dir string) string   { return filepath.Join(dir, "spec.json") }
func statePath(dir string) string  { return filepath.Join(dir, "state.json") }
func socketPath(dir string) string { return platform.SocketPath(dir, "shim.sock") }
func logPath(dir string) string    { return filepath.Join(dir, "shim.log") }
func stderrPath(dir string) string { return filepath.Join(dir, "shim.stderr") }
