package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
)

// ============================================================
// 图像工具（纯 Go，无 cgo / 无第三方依赖，可跨平台单测）
// ============================================================

// toRGBA 统一转成 *image.RGBA，便于按字节处理
func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// downscale 按最大宽度等比缩小。返回图、宽、高、scale（输出/输入）。
// 已经是 RGBA 且不需要缩小时直接复用，不额外拷贝。
func downscale(src image.Image, maxWidth int) (image.Image, int, int, float64) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src, 0, 0, 1
	}
	if maxWidth <= 0 || w <= maxWidth {
		return src, w, h, 1.0
	}
	scale := float64(maxWidth) / float64(w)
	dw := int(float64(w) * scale)
	dh := int(float64(h) * scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	return areaResize(toRGBA(src), dw, dh), dw, dh, scale
}

// areaResize 面积平均缩放。抓屏预览这种"大幅下采样"场景下，
// 面积平均比双线性更保真（不会丢小目标），且实现简单无依赖。
func areaResize(src *image.RGBA, dw, dh int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))

	xRatio := float64(sw) / float64(dw)
	yRatio := float64(sh) / float64(dh)

	for y := 0; y < dh; y++ {
		y0 := int(float64(y) * yRatio)
		y1 := int(float64(y+1) * yRatio)
		if y1 > sh {
			y1 = sh
		}
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := int(float64(x) * xRatio)
			x1 := int(float64(x+1) * xRatio)
			if x1 > sw {
				x1 = sw
			}
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					i := src.PixOffset(sb.Min.X+sx, sb.Min.Y+sy)
					if i+3 >= len(src.Pix) {
						continue
					}
					r += uint64(src.Pix[i])
					g += uint64(src.Pix[i+1])
					bl += uint64(src.Pix[i+2])
					a += uint64(src.Pix[i+3])
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			di := dst.PixOffset(x, y)
			dst.Pix[di] = uint8(r / n)
			dst.Pix[di+1] = uint8(g / n)
			dst.Pix[di+2] = uint8(bl / n)
			dst.Pix[di+3] = uint8(a / n)
		}
	}
	return dst
}

// crop 按绝对像素矩形裁剪，自动 clip 到图像边界
func crop(src image.Image, x0, y0, x1, y1 int) image.Image {
	rgba := toRGBA(src)
	b := rgba.Bounds()
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > b.Dx() {
		x1 = b.Dx()
	}
	if y1 > b.Dy() {
		y1 = b.Dy()
	}
	if x1-x0 < 2 || y1-y0 < 2 {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	draw.Draw(out, out.Bounds(), rgba, image.Pt(x0, y0), draw.Src)
	return out
}

// encodeJPEG 编码为 JPEG 字节
func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	if quality <= 0 || quality > 100 {
		quality = 70
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// b64 标准 base64（不带 data: 前缀，前端自己拼）
func b64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Gray 给前端画叠加层用的常量（避免前端硬编码颜色值）
func Gray() color.Gray { return color.Gray{Y: 128} }
