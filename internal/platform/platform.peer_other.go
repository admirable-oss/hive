//go:build !darwin && !linux

package platform

func peerUID(int) (int, error) { return -1, ErrUnsupported }
