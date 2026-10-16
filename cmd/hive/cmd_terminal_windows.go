//go:build windows

package main

import (
	"context"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
)

// windowResizes signals each change of this console's size. Windows sends
// no signal for it, so the size is polled.
func windowResizes(ctx context.Context, fd uintptr) (<-chan os.Signal, func()) {
	out := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		lastW, lastH, _ := term.GetSize(fd)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w, h, err := term.GetSize(fd)
				if err != nil || (w == lastW && h == lastH) {
					continue
				}
				lastW, lastH = w, h
				select {
				case out <- os.Interrupt: // any value: the size changed
				default:
				}
			}
		}
	}()
	return out, cancel
}
