package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// The tray icon is drawn, like the web observer's icons: Hive's honeycomb
// cell. A template icon is black on transparent (macOS tints it to the menu
// bar); the attention variant is amber.

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

// trayIcon draws the cell size × size in ink, antialiased.
func trayIcon(size int, ink color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	c := float64(size) / 2
	outer, inner := hexagon(c, c, c*0.92), hexagon(c, c, c*0.92*0.42)
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
			a := ink
			a.A = uint8(math.Round(float64(ink.A) * float64(hit) / (ss * ss)))
			img.SetNRGBA(x, y, a)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

var (
	trayTemplate  = trayIcon(44, color.NRGBA{0, 0, 0, 0xff})
	trayAttention = trayIcon(44, color.NRGBA{0xf2, 0xc6, 0x6d, 0xff})
)
