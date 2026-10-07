package process

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/admirable-oss/hive/internal/pgroup"
)

// execRunner is the default Runner, backed by os/exec.
type execRunner struct{}

func NewExecRunner() Runner { return execRunner{} }

// Start launches cmd as the leader of a new process group with its output
// appended to the log files. The process outlives ctx by design.
func (execRunner) Start(_ context.Context, cmd Command) (Handle, error) {
	c := exec.Command(cmd.Path, cmd.Args...)
	c.Dir = cmd.WorkingDir
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	h := &execHandle{cmd: c}
	for _, log := range []struct {
		path string
		dst  *io.Writer
	}{{cmd.StdoutPath, &c.Stdout}, {cmd.StderrPath, &c.Stderr}} {
		if log.path == "" {
			continue
		}
		f, err := os.OpenFile(log.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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
	cmd  *exec.Cmd
	logs []*os.File
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

func (h *execHandle) Kill() error { return pgroup.Terminate(h.PID()) }

func (h *execHandle) closeLogs() {
	for _, f := range h.logs {
		_ = f.Close()
	}
}
