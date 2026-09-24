package app

import (
	"image"
	"image/color"
	"testing"

	"screenguard/internal/infer"
	"screenguard/internal/types"
)

// 纯色渐变测试图
func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	return img
}

func TestDownscaleNoopWhenSmall(t *testing.T) {
	src := testImage(800, 600)
	out, w, h, scale := downscale(src, 1280)
	if w != 800 || h != 600 || scale != 1.0 {
		t.Fatalf("不该缩放: w=%d h=%d scale=%v", w, h, scale)
	}
	if out == nil {
		t.Fatal("返回 nil")
	}
}

func TestDownscaleKeepsAspectRatio(t *testing.T) {
	src := testImage(3840, 2160)
	_, w, h, scale := downscale(src, 1280)
	if w != 1280 {
		t.Fatalf("宽度应为 1280，得到 %d", w)
	}
	if h < 719 || h > 721 {
		t.Fatalf("高度应保持 16:9，得到 %d", h)
	}
	if scale < 0.332 || scale > 0.335 {
		t.Fatalf("scale 异常: %v", scale)
	}
}

// 面积平均缩放不能把高对比小目标抹掉（这是抓屏预览的关键性质）
func TestAreaResizePreservesBrightMarker(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			src.Set(x, y, color.Black)
		}
	}
	// 画一个 10x10 白块
	for y := 95; y < 105; y++ {
		for x := 95; x < 105; x++ {
			src.Set(x, y, color.White)
		}
	}
	out := areaResize(src, 100, 100)
	maxV := uint8(0)
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if v := out.Pix[out.PixOffset(x, y)]; v > maxV {
				maxV = v
			}
		}
	}
	if maxV < 200 {
		t.Fatalf("白块被抹平了，最大亮度只有 %d", maxV)
	}
}

func TestCropClipsToBounds(t *testing.T) {
	src := testImage(100, 100)
	got := crop(src, -20, 50, 300, 80)
	if got == nil {
		t.Fatal("crop 返回 nil")
	}
	b := got.Bounds()
	if b.Min.X != 0 || b.Min.Y != 0 {
		t.Fatalf("crop 结果原点应为 0,0，得到 %v", b.Min)
	}
	if b.Dx() != 100 || b.Dy() != 30 {
		t.Fatalf("clip 后尺寸应为 100x30，得到 %dx%d", b.Dx(), b.Dy())
	}
}

func TestCropTooSmallReturnsNil(t *testing.T) {
	if crop(testImage(100, 100), 10, 10, 11, 11) != nil {
		t.Fatal("1x1 裁剪应返回 nil")
	}
}

func TestEncodeJPEGAndB64(t *testing.T) {
	b, err := encodeJPEG(testImage(64, 64), 62)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 100 {
		t.Fatalf("JPEG 太小: %d", len(b))
	}
	s := b64(b)
	if len(s) <= len(b) {
		t.Fatal("base64 应比原字节长")
	}
}

func TestClampI(t *testing.T) {
	if clampI(-5, 0, 10) != 0 || clampI(99, 0, 10) != 10 || clampI(5, 0, 10) != 5 {
		t.Fatal("clampI 行为不对")
	}
}

// 双坐标一致性：OriginalToModel 必须是 ModelToOriginal 的逆
func TestLetterboxRoundTrip(t *testing.T) {
	lb := infer.ComputeLetterbox(3840, 2160, 1280)
	orig := types.BoxXYXY{100, 200, 300, 400}
	m := lb.OriginalToModel(orig[0], orig[1], orig[2], orig[3])
	back := lb.ModelToOriginal(m[0], m[1], m[2], m[3])
	for i := 0; i < 4; i++ {
		d := back[i] - orig[i]
		if d < -0.01 || d > 0.01 {
			t.Fatalf("第 %d 维往返误差 %v（orig=%v back=%v）", i, d, orig[i], back[i])
		}
	}
}

func TestBuildPayloadNilImage(t *testing.T) {
	lb := infer.ComputeLetterbox(1920, 1080, 1280)
	if fp := BuildFramePayload(nil, lb, 1280, nil, nil, nil, 10, 20, 1920, 1080, 1280, jpegQ); fp != nil {
		t.Fatal("nil 图应返回 nil")
	}
}

func TestBuildPayloadWithBoxes(t *testing.T) {
	src := testImage(1920, 1080)
	lb := infer.ComputeLetterbox(1920, 1080, 1280)

	containers := []types.DetectionBox{{
		ClS: 0, ClSName: "TanChuang", Conf: 0.9,
		Role: types.RoleContainer, GatePassed: true,
		Box: types.BoxXYXY{700, 320, 1220, 700}, Center: types.Center{960, 510},
	}}
	exits := []types.DetectionBox{{
		ClS: 1, ClSName: "GuanBi", Conf: 0.62,
		Role: types.RoleExit, GatePassed: false, RejectReason: "outside_container",
		Box: types.BoxXYXY{1184, 332, 1206, 354}, Center: types.Center{1195, 343},
	}}
	ev := &types.DetectionEvent{Action: types.ActionBlockedOutsideContainer, Result: types.ResultNone, FrameIndex: 7}

	fp := BuildFramePayload(src, lb, 1280, containers, exits, ev, 12, 34, 1920, 1080, 1280, jpegQ)
	if fp == nil {
		t.Fatal("payload 为 nil")
	}
	if fp.ImgW > 1280 {
		t.Fatalf("未降尺度: %d", fp.ImgW)
	}
	if len(fp.Boxes) != 2 {
		t.Fatalf("应有 2 个框，得到 %d", len(fp.Boxes))
	}
	if fp.JpegB64 == "" {
		t.Fatal("JPEG 为空")
	}
	if fp.Action != "blocked_outside_container" || fp.FrameIndex != 7 {
		t.Fatalf("事件字段丢失: %+v", fp)
	}
	if !fp.HasPopup || fp.ROI == nil {
		t.Fatal("有容器时应有 ROI")
	}
	if fp.ROI.W <= 0 || fp.ROI.H <= 0 || fp.ROI.Imgsz2 < 768 {
		t.Fatalf("ROI 参数异常: %+v", fp.ROI)
	}
	// 模型空间框必须落在 [0, imgsz] 内
	for _, b := range fp.Boxes {
		if b.BoxModel[0] < -1 || b.BoxModel[2] > 1281 {
			t.Fatalf("模型空间 x 越界: %+v", b.BoxModel)
		}
	}
}

func TestFramePusherThrottle(t *testing.T) {
	p := NewFramePusher()
	var got int
	p.SetEmitter(func(string, any) { got++ })

	lb := infer.ComputeLetterbox(1920, 1080, 1280)
	src := testImage(1920, 1080)

	// 连续 10 次无决策帧 → 只应推 1 次（第一次），其余被节流
	for i := 0; i < 10; i++ {
		p.Push(src, lb, 1280, nil, nil, nil, 5, 10, 1920, 1080)
	}
	pushed, skipped, _, _ := p.Stats()
	if pushed != 1 {
		t.Fatalf("节流失效：推了 %d 次", pushed)
	}
	if skipped != 9 {
		t.Fatalf("skipped 应为 9，得到 %d", skipped)
	}
	if got != 1 {
		t.Fatalf("emit 调用 %d 次", got)
	}
}

func TestFramePusherFrozenBlocks(t *testing.T) {
	p := NewFramePusher()
	p.SetFrozen(true)
	var got int
	p.SetEmitter(func(string, any) { got++ })
	lb := infer.ComputeLetterbox(1920, 1080, 1280)
	p.Push(testImage(1920, 1080), lb, 1280, nil, nil, nil, 5, 10, 1920, 1080)
	if got != 0 {
		t.Fatal("冻结时不应推送")
	}
}

func TestFramePusherDisabledBlocks(t *testing.T) {
	p := NewFramePusher()
	p.SetEnabled(false)
	var got int
	p.SetEmitter(func(string, any) { got++ })
	lb := infer.ComputeLetterbox(1920, 1080, 1280)
	p.Push(testImage(1920, 1080), lb, 1280, nil, nil, nil, 5, 10, 1920, 1080)
	if got != 0 {
		t.Fatal("关闭时不应推送")
	}
}

// 关键性质：有决策的帧必须绕过节流，否则维护者会丢掉最想看的瞬间
func TestFramePusherForcePushOnEvent(t *testing.T) {
	p := NewFramePusher()
	var got int
	p.SetEmitter(func(string, any) { got++ })
	lb := infer.ComputeLetterbox(1920, 1080, 1280)
	src := testImage(1920, 1080)

	p.Push(src, lb, 1280, nil, nil, nil, 5, 10, 1920, 1080) // 第 1 次：通过
	ev := &types.DetectionEvent{Action: types.ActionClick, Result: types.ResultVanished}
	boxes := []types.DetectionBox{{ClSName: "GuanBi", Conf: 0.9, Role: types.RoleExit,
		Box: types.BoxXYXY{10, 10, 40, 40}, Center: types.Center{25, 25}}}
	p.Push(src, lb, 1280, nil, boxes, ev, 5, 10, 1920, 1080) // 紧接第 2 次：应强制推

	if got != 2 {
		t.Fatalf("有决策的帧应绕过节流，实际推送 %d 次", got)
	}
}

func TestFramePusherLatest(t *testing.T) {
	p := NewFramePusher()
	if p.Latest() != nil {
		t.Fatal("初始应为 nil")
	}
	lb := infer.ComputeLetterbox(1920, 1080, 1280)
	p.Push(testImage(1920, 1080), lb, 1280, nil, nil, nil, 5, 10, 1920, 1080)
	if p.Latest() == nil {
		t.Fatal("推送后 Latest 不应为 nil")
	}
}
