//go:build !darwin && !linux && !windows

package platform

func peerUID(int) (int, error) { return -1, ErrUnsupported }
