//go:build linux

package platform

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"
)

// userHZ is the clock-tick unit of /proc/<pid>/stat. It is fixed at 100 in
// the kernel's userspace ABI on every architecture Go supports.
const userHZ = 100

func lookupProcess(pid int) (ProcessInfo, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ProcessInfo{}, ErrNoProcess
		}
		return ProcessInfo{}, err
	}
	pgid, ticks, err := parseStat(data)
	if err != nil {
		return ProcessInfo{}, err
	}
	boot, err := bootTime()
	if err != nil {
		return ProcessInfo{}, err
	}
	start := boot.Add(time.Duration(ticks) * time.Second / userHZ)
	return ProcessInfo{PID: pid, PGID: pgid, StartTime: start}, nil
}

// parseStat extracts the process group (field 5) and start time in clock
// ticks since boot (field 22). The command name (field 2) is parenthesised
// and may itself contain spaces or parentheses, so fields are counted from
// the last ')'.
func parseStat(data []byte) (pgid int, ticks uint64, err error) {
	end := bytes.LastIndexByte(data, ')')
	if end < 0 {
		return 0, 0, fmt.Errorf("platform: malformed stat %q", data)
	}
	fields := bytes.Fields(data[end+1:]) // fields[0] is field 3 (state)
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("platform: short stat %q", data)
	}
	if pgid, err = strconv.Atoi(string(fields[2])); err != nil {
		return 0, 0, fmt.Errorf("platform: stat pgid: %w", err)
	}
	if ticks, err = strconv.ParseUint(string(fields[19]), 10, 64); err != nil {
		return 0, 0, fmt.Errorf("platform: stat starttime: %w", err)
	}
	return pgid, ticks, nil
}

func bootTime() (time.Time, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for line := range bytes.Lines(data) {
		if rest, ok := bytes.CutPrefix(line, []byte("btime ")); ok {
			sec, err := strconv.ParseInt(string(bytes.TrimSpace(rest)), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("platform: btime: %w", err)
			}
			return time.Unix(sec, 0), nil
		}
	}
	return time.Time{}, errors.New("platform: btime missing from /proc/stat")
}
