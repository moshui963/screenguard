package capture

import (
	"image"
	"image/color"
	"testing"
)

func blackImg(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	return img
}

func coloredImg(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 100, 255})
		}
	}
	return img
}

func TestIsBlackFrame_NilAndEmpty(t *testing.T) {
	if !IsBlackFrame(nil, 32) {
		t.Fatal("nil 应判为黑帧")
	}
	if !IsBlackFrame(image.NewRGBA(image.Rect(0, 0, 0, 0)), 32) {
		t.Fatal("空 bounds 应判为黑帧")
	}
}

func TestIsBlackFrame_PureBlack(t *testing.T) {
	if !IsBlackFrame(blackImg(1920, 1080), 32) {
		t.Fatal("纯黑图应判为黑帧（锁屏/安全桌面场景）")
	}
}

func TestIsBlackFrame_AlmostBlackButHasContent(t *testing.T) {
	// 大部分黑，但采样网格上有亮点（sampleStep=32 → 落在 x,y 为 32 倍数处）
	img := blackImg(1920, 1080)
	for _, p := range []struct{ x, y int }{
		{32, 32}, {512, 512}, {1024, 800}, {1500, 300},
	} {
		img.Set(p.x, p.y, color.RGBA{255, 255, 255, 255})
	}
	if IsBlackFrame(img, 32) {
		t.Fatal("采样网格上含亮点的图不应判为黑帧")
	}
}

func TestIsBlackFrame_SparseHighlightMissed(t *testing.T) {
	// 亮点不在采样网格上 → 当前采样精度无法命中，仍判为黑帧。
	// 这是采样精度权衡（非 bug）：真实锁屏用更密采样/结合禁区判定兜底，
	// 用例仅固化"稀疏亮点不误判为内容"的行为，避免回归时误改成永远非黑。
	img := blackImg(1920, 1080)
	img.Set(500, 500, color.RGBA{255, 255, 255, 255})
	img.Set(1000, 800, color.RGBA{255, 255, 255, 255})
	if !IsBlackFrame(img, 32) {
		t.Fatal("采样网格外的稀疏亮点仍应判为黑帧（采样精度权衡）")
	}
}

func TestIsBlackFrame_DRMProtected(t *testing.T) {
	// DRM 受保护内容常表现为纯黑或近黑，应判为黑帧（跳过监控，不误点）
	if !IsBlackFrame(blackImg(2560, 1440), 16) {
		t.Fatal("DRM 黑帧应判为黑帧")
	}
}

func TestFrameDiff_Identical(t *testing.T) {
	a := coloredImg(800, 600)
	b := coloredImg(800, 600)
	diff, err := FrameDiff(a, b, 16)
	if err != nil {
		t.Fatal(err)
	}
	if diff != 0 {
		t.Fatalf("相同图帧差应为 0，得 %v", diff)
	}
}

func TestFrameDiff_Changed(t *testing.T) {
	a := coloredImg(800, 600)
	b := coloredImg(800, 600)
	// 在 b 上画一块明显的不同区域
	for y := 100; y < 300; y++ {
		for x := 100; x < 300; x++ {
			b.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	diff, err := FrameDiff(a, b, 16)
	if err != nil {
		t.Fatal(err)
	}
	if diff <= 0 {
		t.Fatalf("有变化的两帧帧差应 > 0，得 %v", diff)
	}
}
