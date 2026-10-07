package process

import (
	"context"
	"os"
	"os/exec"
)

type execHandle struct {
	cmd    *exec.Cmd
	stdout *os.File
	stderr *os.File
}

func (h *execHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *execHandle) Wait() error {
	err := h.cmd.Wait()
	if h.stdout != nil {
		h.stdout.Close()
	}
	if h.stderr != nil {
		h.stderr.Close()
	}
	return err
}

func (h *execHandle) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}

type execRunner struct{}

func NewExecRunner() Runner {
	return &execRunner{}
}

func (r *execRunner) Start(ctx context.Context, cmd Command) (Handle, error) {
	c := exec.CommandContext(ctx, cmd.Path, cmd.Args...)
	c.Dir = cmd.WorkingDir
	c.Env = cmd.Env

	var stdout, stderr *os.File
	var err error

	if cmd.StdoutPath != "" {
		stdout, err = os.OpenFile(cmd.StdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			c.Stdout = stdout
		}
	}
	if cmd.StderrPath != "" {
		stderr, err = os.OpenFile(cmd.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			c.Stderr = stderr
		}
	}

	if err := c.Start(); err != nil {
		if stdout != nil {
			stdout.Close()
		}
		if stderr != nil {
			stderr.Close()
		}
		return nil, err
	}

	return &execHandle{
		cmd:    c,
		stdout: stdout,
		stderr: stderr,
	}, nil
}
