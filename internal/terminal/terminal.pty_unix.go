//go:build !windows

package terminal

import (
	"os"
	"os/exec"

	"github.com/creack/pty"

	"github.com/admirable-oss/hive/internal/platform"
)

// unixPTY is a Unix pseudo-terminal: the master side, and the process
// leading the new session on the slave side.
type unixPTY struct {
	*os.File
	cmd *exec.Cmd
}

func openPTY(cmd Command, size Size, env []string) (ptyDevice, error) {
	c := exec.Command(cmd.Path, cmd.Args...) //nolint:noctx // agents outlive any request context
	c.Dir, c.Env = cmd.WorkingDir, env
	// pty.Start makes the child a session (and so process-group) leader.
	f, err := pty.StartWithSize(c, &pty.Winsize{Cols: size.Width, Rows: size.Height})
	if err != nil {
		return nil, err
	}
	return &unixPTY{File: f, cmd: c}, nil
}

func (p *unixPTY) Resize(size Size) error {
	return pty.Setsize(p.File, &pty.Winsize{Cols: size.Width, Rows: size.Height})
}

func (p *unixPTY) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *unixPTY) Wait() error { return p.cmd.Wait() }

func (p *unixPTY) Foreground() (int, error) {
	raw, err := p.SyscallConn() // unlike Fd, keeps the file non-blocking
	if err != nil {
		return 0, err
	}
	var pgid int
	var ferr error
	if err := raw.Control(func(fd uintptr) { pgid, ferr = platform.ForegroundGroup(fd) }); err != nil {
		return 0, err
	}
	return pgid, ferr
}
