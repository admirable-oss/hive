//go:build darwin

package platform

import (
	"bytes"
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// macOS has no /proc: a process's working directory comes from libproc's
// proc_pidinfo(pid, PROC_PIDVNODEPATHINFO), a public libSystem API. It is
// called the way package syscall and x/sys/unix call libSystem, without
// cgo: the dynamic import below binds the symbol, a one-instruction
// assembly trampoline jumps to it (platform.cwd_darwin.s), and the
// runtime's libc call path runs it on the system stack. Release builds
// stay cross-compiled with CGO_ENABLED=0.
//
// The layout is <sys/proc_info.h>'s, the same on arm64 and amd64 (fixed-
// size fields only); TestProcessCwd checks it against the kernel:
//
//	struct proc_vnodepathinfo {            // 2352 bytes
//		struct vnode_info_path pvi_cdir;   // working directory
//		struct vnode_info_path pvi_rdir;   // root directory
//	};
//	struct vnode_info_path {
//		struct vnode_info vip_vi;          // 152 bytes
//		char vip_path[MAXPATHLEN];         // 1024 bytes, NUL-terminated
//	};
const (
	procPIDVnodePathInfo = 9    // PROC_PIDVNODEPATHINFO
	vnodePathInfoSize    = 2352 // sizeof(struct proc_vnodepathinfo)
	cdirPathOffset       = 152  // offsetof(struct proc_vnodepathinfo, pvi_cdir.vip_path)
	maxPathLen           = 1024 // MAXPATHLEN
)

//go:cgo_import_dynamic libc_proc_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"

// libcProcPidinfoTrampolineAddr is the trampoline's address (set in
// platform.cwd_darwin.s).
var libcProcPidinfoTrampolineAddr uintptr

// syscall6 calls a libSystem function on the system stack, as package
// syscall does for its own calls.
//
//go:linkname syscall6 syscall.syscall6
func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

func processCwd(pid int) (string, error) {
	var info [vnodePathInfoSize]byte
	// int proc_pidinfo(int pid, int flavor, uint64_t arg, void *buffer, int buffersize)
	n, _, _ := syscall6(libcProcPidinfoTrampolineAddr,
		uintptr(pid), procPIDVnodePathInfo, 0,
		uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)), 0) //nolint:gosec // a fixed-size buffer the call fills
	if int32(n) <= 0 {
		// proc_pidinfo reports failure as 0, without an errno to read here.
		if _, err := lookupProcess(pid); errors.Is(err, ErrNoProcess) {
			return "", ErrNoProcess
		}
		return "", fmt.Errorf("platform: proc_pidinfo(%d) failed", pid)
	}
	if int(n) != vnodePathInfoSize {
		return "", fmt.Errorf("platform: proc_pidinfo(%d): %d bytes, want %d", pid, n, vnodePathInfoSize)
	}
	path := info[cdirPathOffset : cdirPathOffset+maxPathLen]
	if i := bytes.IndexByte(path, 0); i >= 0 {
		path = path[:i]
	}
	if len(path) == 0 {
		return "", errors.New("platform: the process has no working directory")
	}
	return string(path), nil
}
