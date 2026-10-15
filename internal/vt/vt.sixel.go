package vt

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
)

// MaxSixelPixels bounds conversion work and the size of a generated terminal
// control sequence. Callers must still check the encoded byte limit.
const (
	MaxSixelPixels = 2000000
	maxSixelBytes  = 8 << 20
)

// EncodeSixel encodes an image as a DEC Sixel DCS sequence. It uses a fixed
// 3:3:2 RGB palette, which avoids slow per-image quantization and keeps the
// conversion deterministic. Transparent pixels are rendered as the terminal
// background.
func EncodeSixel(src image.Image) ([]byte, error) {
	if src == nil {
		return nil, fmt.Errorf("vt: nil sixel image")
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 || int64(w)*int64(h) > MaxSixelPixels {
		return nil, fmt.Errorf("vt: sixel image dimensions exceed limit")
	}
	pixels := make([]uint8, w*h)
	opaque := make([]bool, w*h)
	used := [256]bool{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := src.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			if a < 0x8000 {
				continue
			}
			// RGB 3:3:2 quantization maps each channel directly to a palette slot.
			idx := uint8((r>>13)<<5 | (g>>13)<<2 | (b >> 14))
			n := y*w + x
			pixels[n], opaque[n], used[idx] = idx, true, true
		}
	}
	var out bytes.Buffer
	// Pixel aspect ratio 1:1 and raster attributes permit proper sizing.
	fmt.Fprintf(&out, "\x1bP0;1q\"1;1;%d;%d", w, h)
	for idx, ok := range used {
		if !ok {
			continue
		}
		r := ((idx >> 5) & 7) * 100 / 7
		g := ((idx >> 2) & 7) * 100 / 7
		b := (idx & 3) * 100 / 3
		fmt.Fprintf(&out, "#%d;2;%d;%d;%d", idx, r, g, b)
		if out.Len() > maxSixelBytes {
			return nil, fmt.Errorf("vt: sixel output exceeds %d bytes", maxSixelBytes)
		}
	}
	for band := 0; band < h; band += 6 {
		if band != 0 {
			out.WriteByte('-')
		}
		for idx, ok := range used {
			if !ok {
				continue
			}
			fmt.Fprintf(&out, "#%d", idx)
			last := -1
			for x := 0; x < w; x++ {
				mask := byte(0)
				for dy := 0; dy < 6 && band+dy < h; dy++ {
					n := (band+dy)*w + x
					if opaque[n] && pixels[n] == uint8(idx) {
						mask |= 1 << dy
					}
				}
				if mask != 0 {
					for last+1 < x {
						out.WriteByte('?')
						last++
					}
					out.WriteByte('?' + mask)
					last = x
				}
			}
			out.WriteByte('$')
			if out.Len() > maxSixelBytes {
				return nil, fmt.Errorf("vt: sixel output exceeds %d bytes", maxSixelBytes)
			}
		}
	}
	out.WriteString("\x1b\\")
	return out.Bytes(), nil
}

// SixelPaletteColor returns the RGB color represented by a 3:3:2 palette
// index. Kept small and explicit for users that need palette inspection.
func SixelPaletteColor(idx uint8) color.RGBA {
	r := ((idx >> 5) & 7) * 255 / 7
	g := ((idx >> 2) & 7) * 255 / 7
	b := (idx & 3) * 255 / 3
	return color.RGBA{R: r, G: g, B: b, A: 255}
}
