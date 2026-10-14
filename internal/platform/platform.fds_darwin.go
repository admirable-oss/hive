//go:build darwin

package platform

import "golang.org/x/sys/unix"

// OpenFDs counts this process's open descriptors. macOS's /dev/fd directory
// may return a stale enumeration when opened while another descriptor is
// already live, so probe descriptor slots directly instead.
func OpenFDs() (int, error) {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		return 0, err
	}
	// A process may retain a descriptor inherited above a subsequently
	// lowered soft limit. Probe a small floor past tiny limits as well.
	maxFD := limit.Cur
	if maxFD < 1024 {
		maxFD = 1024
	}
	count := 0
	for fd := 0; uint64(fd) < maxFD; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			count++
		}
	}
	return count, nil
}
