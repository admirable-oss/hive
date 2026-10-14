package main

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// agentStream imitates agent output: coloured log lines, an Ink-style
// redraw of a status block every few lines, wide characters.
func agentStream(size int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "\x1b[38;5;%dm› edit\x1b[0m src/module_%04d.ts \x1b[32m+%d\x1b[0m \x1b[31m-%d\x1b[0m %s 日本\r\n", i%256, i, i%97, i%13, strings.Repeat("·", i%40))
		if i%8 == 0 {
			b.WriteString("\x1b[2K\x1b[1A\x1b[2K\x1b[1A\x1b[2K\x1b[G\x1b[1m✻ Thinking…\x1b[0m (esc to interrupt)\r\n\x1b[38;2;120;120;255m> \x1b[0m\r\n")
		}
	}
	return b.Bytes()
}

func throughput(name string, mk func() screener, data []byte) {
	e := mk()
	start := time.Now()
	feedChunks(e, data)
	d := time.Since(start)
	fmt.Printf("  %-6s %6.1f MB/s  (%d MB in %s)\n", name, float64(len(data))/1e6/d.Seconds(), len(data)>>20, d.Round(time.Millisecond))
}

func heapAfter(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.GC()
	runtime.ReadMemStats(&after)
	return after.HeapAlloc - before.HeapAlloc
}

func runPerf() {
	data := agentStream(32 << 20)
	fmt.Println("throughput, 220x50 screen, 4 KiB chunks:")
	throughput("x/vt", func() screener { return newXVTsized(220, 50) }, data)
	throughput("vt10x", func() screener { return newX10(220, 50) }, data)

	fmt.Println("memory per emulator, 220x50:")
	var keep []*xvt
	empty := heapAfter(func() {
		for range 20 {
			keep = append(keep, newXVTsized(220, 50))
		}
	})
	fmt.Printf("  x/vt empty:                     %6.1f KiB\n", float64(empty)/20/1024)
	full := heapAfter(func() {
		for _, e := range keep {
			e.e.SetScrollbackSize(10000)
			feedChunks(e, agentStream(4<<20)) // ~10k+ lines into scrollback
		}
	})
	fmt.Printf("  x/vt with %d scrollback lines: %6.1f MiB\n", keep[0].e.ScrollbackLen(), float64(full)/20/1024/1024)
	runtime.KeepAlive(keep)
}
