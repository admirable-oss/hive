//go:build !darwin && !linux

package platform

import "time"

func lookupProcess(int) (ProcessInfo, error) { return ProcessInfo{}, ErrUnsupported }

func processCwd(int) (string, error) { return "", ErrUnsupported }

func processArgs(int) ([]string, error) { return nil, ErrUnsupported }

func foregroundGroup(uintptr) (int, error) { return 0, ErrUnsupported }

func bootTime() (time.Time, error) { return time.Time{}, ErrUnsupported }
