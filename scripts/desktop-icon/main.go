// desktop-icon 生成原创 Kivo 应用图标；纯 Go 绘制，不依赖图像编辑器。
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

func main() {
	path := "desktop/build/appicon.png"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	const size = 1024
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x), float64(y)
			// 圆角背景与三段 K 字笔画，采用距离场抗锯齿。
			cx := math.Max(138, math.Min(886, px))
			cy := math.Max(138, math.Min(886, py))
			alpha := clamp(139 - math.Hypot(px-cx, py-cy))
			if alpha == 0 {
				continue
			}
			base := color.NRGBA{R: 26, G: 27, B: 36, A: uint8(255 * alpha)}
			d := math.Min(segment(px, py, 333, 290, 333, 734), math.Min(segment(px, py, 343, 515, 677, 292), segment(px, py, 465, 436, 686, 731)))
			mix := clamp(50 - d)
			base.R = uint8(float64(base.R)*(1-mix) + 129*mix)
			base.G = uint8(float64(base.G)*(1-mix) + 140*mix)
			base.B = uint8(float64(base.B)*(1-mix) + 248*mix)
			img.SetNRGBA(x, y, base)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		panic(err)
	}
	// 直接生成 ICO，避免 Wails 复用第一次构建生成的默认图标。
	if path == "desktop/build/appicon.png" {
		small := image.NewNRGBA(image.Rect(0, 0, 256, 256))
		for y := 0; y < 256; y++ {
			for x := 0; x < 256; x++ {
				var r, g, b, a uint32
				for oy := 0; oy < 4; oy++ {
					for ox := 0; ox < 4; ox++ {
						c := img.NRGBAAt(x*4+ox, y*4+oy)
						r += uint32(c.R)
						g += uint32(c.G)
						b += uint32(c.B)
						a += uint32(c.A)
					}
				}
				small.SetNRGBA(x, y, color.NRGBA{R: uint8(r / 16), G: uint8(g / 16), B: uint8(b / 16), A: uint8(a / 16)})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, small); err != nil {
			panic(err)
		}
		var ico bytes.Buffer
		for _, value := range []uint16{0, 1, 1} {
			if err := binary.Write(&ico, binary.LittleEndian, value); err != nil {
				panic(err)
			}
		}
		ico.Write([]byte{0, 0, 0, 0}) // ICO 的 0 表示 256 像素。
		for _, value := range []uint16{1, 32} {
			if err := binary.Write(&ico, binary.LittleEndian, value); err != nil {
				panic(err)
			}
		}
		for _, value := range []uint32{uint32(encoded.Len()), 22} {
			if err := binary.Write(&ico, binary.LittleEndian, value); err != nil {
				panic(err)
			}
		}
		ico.Write(encoded.Bytes())
		if err := os.MkdirAll("desktop/build/windows", 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile("desktop/build/windows/icon.ico", ico.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
}

func clamp(value float64) float64 { return math.Max(0, math.Min(1, value)) }
func segment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := clamp(((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy))
	return math.Hypot(px-ax-t*dx, py-ay-t*dy)
}
