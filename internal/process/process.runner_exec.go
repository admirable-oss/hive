package process

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/admirable-oss/hive/internal/pgroup"
)

// execRunner is the default Runner, backed by os/exec.
type execRunner struct {
	grace time.Duration // SIGTERM → SIGKILL delay of Kill
}

// NewExecRunner returns a Runner whose handles give a stopped process group
// grace before killing it. Zero means pgroup.Grace.
func NewExecRunner(grace time.Duration) Runner {
	if grace <= 0 {
		grace = pgroup.Grace
	}
	return execRunner{grace: grace}
}

// Start launches cmd as the leader of a new process group with its output
// appended to the log files. The process outlives ctx by design.
func (r execRunner) Start(_ context.Context, cmd Command) (Handle, error) {
	c := exec.Command(cmd.Path, cmd.Args...) //nolint:noctx // agents outlive any request context
	c.Dir = cmd.WorkingDir
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	h := &execHandle{cmd: c, grace: r.grace}
	for _, log := range []struct {
		path string
		dst  *io.Writer
	}{{cmd.StdoutPath, &c.Stdout}, {cmd.StderrPath, &c.Stderr}} {
		if log.path == "" {
			continue
		}
		// Agent output can contain secrets, so logs are private.
		f, err := os.OpenFile(log.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			h.closeLogs()
			return nil, err
		}
		*log.dst = f
		h.logs = append(h.logs, f)
	}

	if err := c.Start(); err != nil {
		h.closeLogs()
		return nil, err
	}
	return h, nil
}

type execHandle struct {
	cmd   *exec.Cmd
	logs  []*os.File
	grace time.Duration
}

func (h *execHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *execHandle) Wait() error {
	err := h.cmd.Wait()
	h.closeLogs()
	return err
}

func (h *execHandle) Kill() error { return pgroup.TerminateAfter(h.PID(), h.grace) }

func (h *execHandle) closeLogs() {
	for _, f := range h.logs {
		_ = f.Close()
	}
}
