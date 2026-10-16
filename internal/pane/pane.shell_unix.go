//go:build !windows

package pane

import "os"

// defaultShell is $SHELL as a login shell, else /bin/sh.
func defaultShell() []string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh, "-l"}
	}
	return []string{"/bin/sh"}
}
