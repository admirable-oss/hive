//go:build linux

package platform

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func processArgs(pid int) ([]string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoProcess
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, ErrNoProcess // a zombie or kernel thread
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), nil
}

func foregroundGroup(fd uintptr) (int, error) {
	return unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
}
