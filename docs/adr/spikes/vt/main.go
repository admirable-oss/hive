package main

import (
	"fmt"
	"os"
)

func main() {
	emus := []func() screener{
		func() screener { return newXVT(80, 10) },
		func() screener { return newX10(80, 10) },
	}
	switch os.Args[1] {
	case "perf":
		runPerf()
	case "record":
		dir, _ := os.MkdirTemp("", "vtrec")
		recs := recordAll(dir)
		for _, r := range recs {
			compareRecording(r)
		}
	case "conformance":
		res := runConformance(emus)
		pass := map[string]int{}
		for _, r := range res {
			line := fmt.Sprintf("%-34s", r.name)
			for _, n := range []string{"x/vt", "vt10x"} {
				mark := "ok  "
				if !r.pass[n] {
					mark = "FAIL"
				} else {
					pass[n]++
				}
				line += fmt.Sprintf("  %s %s", n, mark)
			}
			fmt.Println(line)
			for n, d := range r.detail {
				fmt.Printf("      %s: %s\n", n, d)
			}
		}
		fmt.Printf("\nx/vt %d/%d, vt10x %d/%d\n", pass["x/vt"], len(res), pass["vt10x"], len(res))
		reply, err := dsrReply()
		fmt.Printf("x/vt DSR 6n reply: %q err=%v\n", reply, err)
	}
}

// compareRecording replays r into both emulators and compares the screens at
// every marker and at the end.
func compareRecording(r recording) {
	points := append(append([]int{}, r.markers...), len(r.data))
	fmt.Printf("\n== %s: %d bytes, %d checkpoints\n", r.name, len(r.data), len(points))
	for i, off := range points {
		a := newXVTsized(80, 24)
		b := newX10(80, 24)
		feedChunks(a, r.data[:off])
		feedChunks(b, r.data[:off])
		ra, rb := a.Rows(), b.Rows()
		diff := 0
		first := -1
		for y := range ra {
			if ra[y] != rb[y] {
				diff++
				if first < 0 {
					first = y
				}
			}
		}
		if diff == 0 {
			fmt.Printf("  checkpoint %d @%d: identical (%d non-blank rows)\n", i, off, nonBlank(ra))
			continue
		}
		fmt.Printf("  checkpoint %d @%d: %d rows differ; first row %d\n    x/vt : %q\n    vt10x: %q\n", i, off, diff, first, ra[first], rb[first])
	}
	if os.Getenv("DUMP") == r.name {
		a := newXVTsized(80, 24)
		feedChunks(a, r.data)
		for _, row := range a.Rows() {
			fmt.Printf("    |%s\n", row)
		}
	}
}

func nonBlank(rows []string) int {
	n := 0
	for _, r := range rows {
		if r != "" {
			n++
		}
	}
	return n
}

// feedChunks writes data in 4 KiB pieces like a PTY reader would, which
// also splits escape sequences and UTF-8 at arbitrary points.
func feedChunks(s screener, data []byte) {
	for len(data) > 0 {
		n := min(len(data), 4096)
		_, _ = s.Write(data[:n])
		data = data[n:]
	}
}
