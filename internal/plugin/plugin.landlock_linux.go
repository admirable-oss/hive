//go:build linux

package plugin

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlockRuleset is the ABI 1 ruleset attribute: only filesystem access
// is handled, so every Landlock kernel (5.13 and later) accepts it.
type landlockRuleset struct {
	handledAccessFS uint64
}

// ErrNoLandlock means this kernel has no Landlock (before 5.13, or turned
// off).
var ErrNoLandlock = errors.New("landlock is not available")

// landlockABI is the kernel's Landlock ABI version, or an error.
func landlockABI() (int, error) {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, fmt.Errorf("%w: %w", ErrNoLandlock, errno)
	}
	return int(v), nil
}

// restrictExec limits, for this process and everything it starts, which
// files may be executed to those beneath paths (files or directories).
// Paths that do not exist are skipped.
func restrictExec(paths []string) error {
	if _, err := landlockABI(); err != nil {
		return err
	}
	attr := landlockRuleset{handledAccessFS: unix.LANDLOCK_ACCESS_FS_EXECUTE}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0) //nolint:gosec // the Landlock ABI takes a pointer to the attribute struct
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer unix.Close(ruleset)
	for _, p := range paths {
		f, err := os.OpenFile(p, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		rule := unix.LandlockPathBeneathAttr{Allowed_access: unix.LANDLOCK_ACCESS_FS_EXECUTE, Parent_fd: int32(f.Fd())}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0) //nolint:gosec // the Landlock ABI takes a pointer to the rule
		_ = f.Close()
		if errno != 0 {
			return fmt.Errorf("landlock_add_rule %s: %w", p, errno)
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", errno)
	}
	return nil
}
