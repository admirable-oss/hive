//go:build !linux && !windows

package plugin

import "errors"

// ErrNoLandlock means this kernel has no Landlock: every system but Linux.
var ErrNoLandlock = errors.New("landlock is not available")

func restrictExec([]string) error { return ErrNoLandlock }
