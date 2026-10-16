package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestIcons: every icon the web observer lists exists, has its size, and
// shows the cell (amber ring, dark centre and corners). Pixels are not
// compared byte for byte: floating point may round differently on other
// processors. `go run ./tools/webicons internal/web/assets` redraws them.
func TestIcons(t *testing.T) {
	for _, ic := range Icons {
		for name, data := range map[string][]byte{"committed": read(t, ic.Name), "drawn": Draw(ic.Size, ic.Scale)} {
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil || img.Bounds().Dx() != ic.Size || img.Bounds().Dy() != ic.Size {
				t.Fatalf("%s %s: %v", name, ic.Name, err)
			}
			c := ic.Size / 2
			ring := c + int(float64(c)*ic.Scale*0.7)
			if !isColor(img, 1, 1, background) || !isColor(img, c, c, background) || !isColor(img, ring, c, amber) {
				t.Errorf("%s %s does not show the cell", name, ic.Name)
			}
		}
	}
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "web", "assets", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func isColor(img image.Image, x, y int, want interface{ RGBA() (r, g, b, a uint32) }) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	wr, wg, wb, _ := want.RGBA()
	near := func(a, b uint32) bool { return a/257 >= b/257-8 && a/257 <= b/257+8 }
	return near(r, wr) && near(g, wg) && near(b, wb)
}
