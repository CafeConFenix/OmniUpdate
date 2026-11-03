// Generates the application icon (PNG for the UI and ICO for the shortcuts).
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	N  = 256
	SS = 4
)

func sdfRoundRect(x, y, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(x-cx) - (hw - r)
	qy := math.Abs(y-cy) - (hh - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

func inTri(px, py, ax, ay, bx, by, cx, cy float64) bool {
	d := func(x1, y1, x2, y2, x3, y3 float64) float64 { return (x1-x3)*(y2-y3) - (x2-x3)*(y1-y3) }
	d1, d2, d3 := d(px, py, ax, ay, bx, by), d(px, py, bx, by, cx, cy), d(px, py, cx, cy, ax, ay)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

// render draws the icon at n×n pixels (design coordinates 256×256).
func render(n int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	k := 256.0 / float64(n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a float64
			for sy := 0; sy < SS; sy++ {
				for sx := 0; sx < SS; sx++ {
					fx := (float64(x) + (float64(sx)+0.5)/SS) * k
					fy := (float64(y) + (float64(sy)+0.5)/SS) * k
					if sdfRoundRect(fx, fy, 128, 128, 120, 120, 56) > 0 {
						continue
					}
					t := (fx + fy) / (2 * N)
					cr := 79 + (20-79)*t
					cg := 70 + (184-70)*t
					cb := 229 + (166-229)*t
					white := (fx >= 117 && fx <= 139 && fy >= 100 && fy <= 168) ||
						inTri(fx, fy, 128, 56, 78, 114, 178, 114) ||
						sdfRoundRect(fx, fy, 128, 192, 52, 9, 9) <= 0
					if white {
						cr, cg, cb = 255, 255, 255
					}
					r, g, b, a = r+cr, g+cg, b+cb, a+1
				}
			}
			if a > 0 {
				img.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / (SS * SS))})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func main() {
	if len(os.Args) < 3 {
		panic("uso: genicon icon.png app.ico")
	}
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	imgs := make([][]byte, len(sizes))
	for i, n := range sizes {
		imgs[i] = render(n)
	}
	if err := os.WriteFile(os.Args[1], imgs[len(imgs)-1], 0o644); err != nil {
		panic(err)
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, n := range sizes {
		w := byte(n)
		if n >= 256 {
			w = 0
		}
		ico.Write([]byte{w, w, 0, 0})
		binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&ico, binary.LittleEndian, uint32(len(imgs[i])))
		binary.Write(&ico, binary.LittleEndian, uint32(off))
		off += len(imgs[i])
	}
	for _, b := range imgs {
		ico.Write(b)
	}
	if err := os.WriteFile(os.Args[2], ico.Bytes(), 0o644); err != nil {
		panic(err)
	}
}
