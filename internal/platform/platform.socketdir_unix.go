//go:build !windows

package platform

import (
	"os"
	"strconv"
)

func shortSocketDir() string {
	return "/tmp/hive-" + strconv.Itoa(os.Getuid())
}
