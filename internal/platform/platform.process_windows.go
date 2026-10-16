//go:build windows

package platform

import (
	"errors"
	"slices"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stillActive is STILL_ACTIVE, the exit code of a running process.
const stillActive = 259

func openQuery(pid int) (windows.Handle, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return 0, ErrNoProcess
	}
	return h, err
}

func lookupProcess(pid int) (ProcessInfo, error) {
	h, err := openQuery(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return ProcessInfo{}, err
	}
	if code != stillActive {
		return ProcessInfo{}, ErrNoProcess
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return ProcessInfo{}, err
	}
	// No process groups: a job (pgroup) stands in, keyed by its leader.
	return ProcessInfo{PID: pid, PGID: pid, StartTime: time.Unix(0, created.Nanoseconds())}, nil
}

// processCommandLineInformation is ProcessCommandLineInformation (Windows
// 8.1+): the command line, without reading the process's memory.
const processCommandLineInformation = 60

func processArgs(pid int) ([]string, error) {
	h, err := openQuery(pid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]byte, 4096)
	for {
		var n uint32
		err := windows.NtQueryInformationProcess(h, processCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n) //nolint:gosec // writes at most len(buf) bytes
		if err == nil {
			break
		}
		if n > uint32(len(buf)) && n <= 1<<20 {
			buf = make([]byte, n)
			continue
		}
		return nil, err
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])) //nolint:gosec // the call wrote a UNICODE_STRING at the start of buf
	if us.Buffer == nil || us.Length == 0 {
		return nil, ErrNoProcess
	}
	return windows.DecomposeCommandLine(windows.UTF16ToString(unsafe.Slice(us.Buffer, us.Length/2))) //nolint:gosec // Buffer points into buf, Length bytes long
}

// The working directory of another process is in its memory (PEB); Hive
// does not read other processes' memory. New panes start in the
// environment's directory instead.
func processCwd(int) (string, error) { return "", ErrUnsupported }

// ConPTY has no foreground process group; see NewestDescendant.
func foregroundGroup(uintptr) (int, error) { return 0, ErrUnsupported }

var procGetTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

func bootTime() (time.Time, error) {
	ms, _, _ := procGetTickCount64.Call()
	return time.Now().Add(-time.Duration(ms) * time.Millisecond), nil
}

// NewestDescendant returns the most recently started live descendant of
// pid (pid itself when it has none): on Windows, the program a shell is
// running, as close as Windows gets to a terminal's foreground job.
func NewestDescendant(pid int) (int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	children := map[uint32][]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID != e.ParentProcessID {
			children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
		}
	}
	best, bestStart := pid, time.Time{}
	if info, err := lookupProcess(pid); err == nil {
		bestStart = info.StartTime
	}
	seen := map[uint32]bool{}
	queue := slices.Clone(children[uint32(pid)])
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if seen[c] {
			continue
		}
		seen[c] = true
		info, err := lookupProcess(int(c))
		if err != nil || info.StartTime.Before(bestStart) {
			continue // gone, or a reused PID older than its "parent"
		}
		if info.StartTime.After(bestStart) {
			best, bestStart = int(c), info.StartTime
		}
		queue = append(queue, children[c]...)
	}
	return best, nil
}
