// Command webicons draws the web observer's app icons (PNG), so they are
// made from code, not committed as opaque art: an amber honeycomb cell on
// the dark background, sized for Android, iOS and maskable use.
//
//	go run ./tools/webicons internal/web/assets
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// Icons are the files written: name, size and how much of the square the
// cell fills (maskable icons keep to the inner safe zone).
var Icons = []struct {
	Name  string
	Size  int
	Scale float64
}{
	{"icon-192.png", 192, 0.72},
	{"icon-512.png", 512, 0.72},
	{"icon-maskable-512.png", 512, 0.56},
	{"icon-180.png", 180, 0.66},
}

var (
	background = color.NRGBA{0x16, 0x1b, 0x22, 0xff}
	amber      = color.NRGBA{0xf2, 0xc6, 0x6d, 0xff}
)

// hexagon is a pointy-topped hexagon of circumradius r around (cx, cy).
func hexagon(cx, cy, r float64) [][2]float64 {
	pts := make([][2]float64, 6)
	for i := range pts {
		a := math.Pi/6 + float64(i)*math.Pi/3
		pts[i] = [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)}
	}
	return pts
}

func inside(p [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(p)-1; i < len(p); j, i = i, i+1 {
		if (p[i][1] > y) != (p[j][1] > y) && x < (p[j][0]-p[i][0])*(y-p[i][1])/(p[j][1]-p[i][1])+p[i][0] {
			in = !in
		}
	}
	return in
}

// Draw renders one icon: an amber cell with a smaller dark cell inside,
// antialiased by 4×4 supersampling.
func Draw(size int, scale float64) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	c := float64(size) / 2
	outer := hexagon(c, c, c*scale)
	inner := hexagon(c, c, c*scale*0.42)
	const ss = 4
	for y := range size {
		for x := range size {
			hit := 0
			for sy := range ss {
				for sx := range ss {
					px, py := float64(x)+(float64(sx)+0.5)/ss, float64(y)+(float64(sy)+0.5)/ss
					if inside(outer, px, py) && !inside(inner, px, py) {
						hit++
					}
				}
			}
			f := float64(hit) / (ss * ss)
			mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-f) + float64(b)*f)) }
			img.SetNRGBA(x, y, color.NRGBA{mix(background.R, amber.R), mix(background.G, amber.G), mix(background.B, amber.B), 0xff})
		}
	}
	var b bytes.Buffer
	_ = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, img)
	return b.Bytes()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: webicons <dir>")
		os.Exit(2)
	}
	for _, ic := range Icons {
		if err := os.WriteFile(filepath.Join(os.Args[1], ic.Name), Draw(ic.Size, ic.Scale), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
