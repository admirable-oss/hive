package platform

import "os"

// OpenFDs counts this process's open file descriptors, for leak checks.
func OpenFDs() (int, error) {
	dir := "/dev/fd" // on Linux a link to /proc/self/fd
	if _, err := os.Stat("/proc/self/fd"); err == nil {
		dir = "/proc/self/fd"
	}
	d, err := os.Open(dir)
	if err != nil {
		return 0, err
	}
	defer d.Close()
	// Names only: stating each entry fails for the descriptor of this very
	// listing on macOS.
	names, err := d.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	return len(names) - 1, nil // the directory being read is one of them
}
