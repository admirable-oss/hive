package main

import (
	"fmt"
	"syscall"
	"time"
)

type counter struct{ n int64; writes int }

func (c *counter) Write(p []byte) (int, error) { c.n += int64(len(p)); c.writes++; return len(p), nil }

// drive sends n steps at roughly 120 per second (a busy fleet), then quits.
func drive(_ any, n int, send func(any), first any) {
	time.Sleep(50 * time.Millisecond)
	if first != nil {
		send(first)
	}
	tick := time.NewTicker(time.Second / 120)
	defer tick.Stop()
	for range n {
		<-tick.C
		send(stepMsg{})
	}
	time.Sleep(100 * time.Millisecond)
	send(doneMsg{})
}

func cpu() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func main() {
	const n = 600 // five seconds at 120 updates/s
	scenarios := []struct {
		name string
		step func(*grid)
	}{{"9 panes scrolling", (*grid).scrollAll}, {"1 pane typing", (*grid).typeChar}}
	for _, s := range scenarios {
		for _, impl := range []struct {
			name string
			run  func(*counter, func(*grid), int)
		}{
			{"bubbletea v2", func(c *counter, f func(*grid), n int) { runV2(c, f, n) }},
		} {
			c := &counter{}
			start, c0 := time.Now(), cpu()
			impl.run(c, s.step, n)
			wall, used := time.Since(start), cpu()-c0
			fmt.Printf("%-18s %-13s %8.1f KiB out  %6.0f B/update  %5d writes  cpu %5.0f ms (%4.1f%% of wall)\n",
				s.name, impl.name, float64(c.n)/1024, float64(c.n)/n, c.writes, float64(used.Milliseconds()), 100*used.Seconds()/wall.Seconds())
		}
	}
}
