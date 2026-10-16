//go:build windows

package platform

import (
	"os"
	"path/filepath"
)

// On Windows the temporary directory is already the user's own
// (%LOCALAPPDATA%\Temp).
func shortSocketDir() string {
	return filepath.Join(os.TempDir(), "hive")
}
