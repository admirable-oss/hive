//go:build !darwin && !linux && !windows

package platform

import "time"

func lookupProcess(int) (ProcessInfo, error) { return ProcessInfo{}, ErrUnsupported }

func processCwd(int) (string, error) { return "", ErrUnsupported }

func processArgs(int) ([]string, error) { return nil, ErrUnsupported }

func foregroundGroup(uintptr) (int, error) { return 0, ErrUnsupported }

func bootTime() (time.Time, error) { return time.Time{}, ErrUnsupported }

// NewestDescendant is Windows' stand-in for a terminal's foreground group.
func NewestDescendant(int) (int, error) { return 0, ErrUnsupported }
