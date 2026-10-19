package core

import (
	"encoding/json"
	"testing"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// busyScreen is a 220×50 screen of styled text, as agents print it: a
// new style every few cells.
func busyScreen() *hiveapi.TerminalScreen {
	s := &hiveapi.TerminalScreen{}
	f := &hiveapi.Frame{Keyframe: true, Cols: 220, Rows: 50}
	for y := range 50 {
		cells := make([]hiveapi.Cell, 220)
		for x := range cells {
			cells[x] = hiveapi.Cell{Content: string(rune('a' + (x+y)%26)), Width: 1, Style: hiveapi.CellStyle{Fg: hiveapi.Color(1<<24 | uint32(x/7%16))}}
		}
		f.Lines = append(f.Lines, hiveapi.LineUpdate{Y: y, Cells: cells})
	}
	s.Apply(f)
	return s
}

// BenchmarkScreenFull encodes a whole busy screen and its JSON: what a
// keyframe costs before it reaches the webview.
func BenchmarkScreenFull(b *testing.B) {
	s := busyScreen()
	b.ReportAllocs()
	for b.Loop() {
		u := update("p1", 1, s, true, nil)
		if _, err := json.Marshal(u); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkScreenLines encodes three changed lines: a typical frame while
// an agent streams output.
func BenchmarkScreenLines(b *testing.B) {
	s := busyScreen()
	b.ReportAllocs()
	for b.Loop() {
		u := update("p1", 1, s, false, []int{47, 48, 49})
		if _, err := json.Marshal(u); err != nil {
			b.Fatal(err)
		}
	}
}
