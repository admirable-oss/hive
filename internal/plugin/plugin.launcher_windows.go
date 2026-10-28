//go:build windows

package plugin

import (
	"fmt"
	"os"
)

// SandboxMain is `hive __plugin-sandbox`, which only Linux's plugin
// sandbox runs.
func SandboxMain([]string) int {
	fmt.Fprintln(os.Stderr, "hive: plugin sandbox: not available on Windows")
	return 126
}
