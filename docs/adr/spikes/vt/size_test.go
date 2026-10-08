package main

import (
	"testing"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestCellSize(t *testing.T) {
	t.Logf("uv.Cell = %d bytes; 220x50 screen = %.1f KiB per buffer", unsafe.Sizeof(uv.Cell{}), float64(unsafe.Sizeof(uv.Cell{}))*220*50/1024)
}
