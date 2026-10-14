package main

import (
	"fmt"
	"io"
	"strings"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

type counter struct{ n int64 }

func (c *counter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func cpu() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

const paneW, paneH = 72, 16 // 3x3 panes fill 216x48

func main() {
	const n = 600
	for _, scenario := range []string{"9 panes scrolling", "1 pane typing"} {
		var panes [9]*vt.Emulator
		for i := range panes {
			panes[i] = vt.NewEmulator(paneW, paneH)
			go func() { _, _ = io.Copy(io.Discard, panes[i]) }()
		}
		out := &counter{}
		r := uv.NewTerminalRenderer(out, []string{"TERM=xterm-256color"})
		r.SetFullscreen(true)
		r.Resize(220, 50)
		buf := uv.NewScreenBuffer(220, 50)

		tick := time.NewTicker(time.Second / 120)
		start, c0 := time.Now(), cpu()
		for step := range n {
			<-tick.C
			if scenario == "9 panes scrolling" {
				for p, e := range panes {
					fmt.Fprintf(e, "\r\n\x1b[38;5;%dm› edit\x1b[0m src/file_%05d.ts +%d -%d %s", (step+p)%256, step, step%97, step%13, strings.Repeat("·", step%30))
				}
			} else {
				_, _ = panes[4].Write([]byte{byte('a' + step%26)})
			}
			for i, e := range panes {
				x, y := (i%3)*paneW, (i/3)*paneH
				e.Draw(buf, uv.Rect(x, y, paneW, paneH))
			}
			r.Render(buf.RenderBuffer)
			_ = r.Flush()
		}
		tick.Stop()
		wall, used := time.Since(start), cpu()-c0
		fmt.Printf("%-18s %-13s %8.1f KiB out  %6.0f B/update  cpu %5.0f ms (%4.1f%% of wall)\n",
			scenario, "uv + x/vt", float64(out.n)/1024, float64(out.n)/n, float64(used.Milliseconds()), 100*used.Seconds()/wall.Seconds())
	}
}
