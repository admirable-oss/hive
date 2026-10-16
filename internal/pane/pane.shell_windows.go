//go:build windows

package pane

import (
	"os"
	"os/exec"
)

// defaultShell is $SHELL when set (Git Bash, MSYS2), else PowerShell 7
// (pwsh), else Windows PowerShell, else %ComSpec% (cmd.exe).
func defaultShell() []string {
	if sh := os.Getenv("SHELL"); sh != "" {
		if path, err := exec.LookPath(sh); err == nil {
			return []string{path, "-l"}
		}
	}
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return []string{path, "-NoLogo"}
		}
	}
	if cs := os.Getenv("ComSpec"); cs != "" {
		return []string{cs}
	}
	return []string{"cmd.exe"}
}
