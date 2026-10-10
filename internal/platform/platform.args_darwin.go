//go:build darwin

package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// processArgs reads kern.procargs2: argc (int32), the executable path,
// NUL padding, then argc NUL-terminated arguments (then the environment,
// which is ignored).
func processArgs(pid int) ([]string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ESRCH) {
			return nil, ErrNoProcess // gone, or a zombie
		}
		return nil, fmt.Errorf("platform: kern.procargs2(%d): %w", pid, err)
	}
	return parseProcArgs2(buf)
}

func parseProcArgs2(buf []byte) ([]string, error) {
	if len(buf) < 4 {
		return nil, errors.New("platform: short kern.procargs2")
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	rest := buf[4:]
	// Skip the executable path and its padding.
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, errors.New("platform: malformed kern.procargs2")
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			j = len(rest)
		}
		args = append(args, string(rest[:j]))
		rest = rest[min(j+1, len(rest)):]
	}
	return args, nil
}

func foregroundGroup(fd uintptr) (int, error) {
	return unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
}
