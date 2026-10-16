//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func restrictSocket(path string) error { return os.Chmod(path, 0o600) }

func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 || !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("platform: %s must be a directory only you can access (mode 0700)", dir)
	}
	return nil
}

func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func lock(f *os.File) error   { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

func startDetached(cmd *exec.Cmd) (*exec.Cmd, error) { return cmd, cmd.Start() }
