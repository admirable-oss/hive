//go:build linux

package platform

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

func processCwd(pid int) (string, error) {
	dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoProcess
	}
	if err != nil {
		return "", err
	}
	// The kernel marks a directory that was removed meanwhile.
	if strings.HasSuffix(dir, " (deleted)") {
		return "", errors.New("platform: the working directory was deleted")
	}
	return dir, nil
}
