//go:build !darwin && !linux

package platform

func lookupProcess(int) (ProcessInfo, error) { return ProcessInfo{}, ErrUnsupported }
