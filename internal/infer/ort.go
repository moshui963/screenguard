package infer

import (
	"fmt"
	"image"
	"math"

	"screenguard/internal/types"
)

// ============================================================
// 推理层 — ONNX Runtime Go 绑定 + 两级调度 + 坐标逆变换
// 与 popup_infer.py 的坐标系完全对齐（PRD 4.3.5 / 附录A 约束2）
// ============================================================

// Detector 推理器接口（抽象层，便于测试和 mock）
type Detector interface {
	// DetectLevel1 第一级：整屏 → imgsz → 只读容器类
	DetectLevel1(img image.Image, imgsz int) ([]types.DetectionBox, int, error)
	// DetectLevel2 第二级：容器框外扩 15% 裁切 → imgsz2
	DetectLevel2(roi image.Image, roiOrigin image.Point, imgsz2 int, screenW, screenH int) ([]types.DetectionBox, int, error)
	// Imgsz2 计算第二级 imgsz = clamp(ceil32(max(crop_w, crop_h)), 768, 1280)
	Imgsz2(cropW, cropH int) int
	// ModelInputSize 返回模型静态输入尺寸；动态输入返回 0（红线 C9）
	ModelInputSize() int
}

// ============================================================
// 坐标逆变换 — 必须与 popup_infer.py 逐像素一致
// Ultralytics predict() 返回的已经是原图像素坐标（内部逆运算了 letterbox）
// 但我们在 Go 侧用 ONNX 直接推理，需要自己实现 letterbox 逆变换
// ============================================================

// LetterboxParams letterbox 参数
type LetterboxParams struct {
	NewShape [2]int     // [h, w] = imgsz
	Ratio    [2]float64 // [rh, rw]
	Dw       float64    // pad width
	Dh       float64    // pad height
}

// ComputeLetterbox 计算从原图 → 模型输入的 letterbox 参数
// 与 Ultralytics 的 letterbox 实现一致：等比缩放 + 居中 pad
func ComputeLetterbox(origW, origH, imgsz int) LetterboxParams {
	r := math.Min(float64(imgsz)/float64(origW), float64(imgsz)/float64(origH))
	newW := int(math.Round(float64(origW) * r))
	newH := int(math.Round(float64(origH) * r))
	dw := (float64(imgsz) - float64(newW)) / 2.0
	dh := (float64(imgsz) - float64(newH)) / 2.0
	return LetterboxParams{
		NewShape: [2]int{imgsz, imgsz},
		Ratio:    [2]float64{r, r},
		Dw:       dw,
		Dh:       dh,
	}
}

// ModelToOriginal 将模型空间坐标 → 原图坐标
// 与 Ultralytics 的 letterbox 逆变换一致
func (p LetterboxParams) ModelToOriginal(x1, y1, x2, y2 float64) types.BoxXYXY {
	// 减去 pad
	ox1 := (x1 - p.Dw) / p.Ratio[0]
	oy1 := (y1 - p.Dh) / p.Ratio[1]
	ox2 := (x2 - p.Dw) / p.Ratio[0]
	oy2 := (y2 - p.Dh) / p.Ratio[1]
	return types.BoxXYXY{ox1, oy1, ox2, oy2}
}

// OriginalToModel 原图坐标 → 模型输入坐标（ModelToOriginal 的逆运算）
// 供调试台双画布联动使用：在原屏画布上悬停，反查该点在模型空间的位置与尺寸
func (p LetterboxParams) OriginalToModel(x1, y1, x2, y2 float64) types.BoxXYXY {
	return types.BoxXYXY{
		x1*p.Ratio[0] + p.Dw,
		y1*p.Ratio[1] + p.Dh,
		x2*p.Ratio[0] + p.Dw,
		y2*p.Ratio[1] + p.Dh,
	}
}

// ============================================================
// 两级调度 — PRD 4.3.4
// ============================================================

// TwoStageResult 两级检测结果
type TwoStageResult struct {
	Containers []types.DetectionBox // 第一级检出的容器
	Exits      []types.DetectionBox // 第二级检出的出口（坐标已还原到屏幕）
	Level1Ms   int
	Level2Ms   int
}

// TwoStageDetect 执行两级检测
// level1: 整屏 → imgsz(由 profile 决定) → 只读容器类，阈值放宽 0.30~0.40
// level2: 容器框外扩 15% 裁切 → imgsz2 = clamp(ceil32(max(crop_w, crop_h)), 768, 1280)
func TwoStageDetect(d Detector, screenImg image.Image, screenW, screenH, imgsz int,
	confThr map[string]float64, classNames []string) (*TwoStageResult, error) {

	// 第一级
	containers, l1ms, err := d.DetectLevel1(screenImg, imgsz)
	if err != nil {
		return nil, fmt.Errorf("第一级检测: %w", err)
	}

	// 只保留容器类，并显式赋 Role。
	// 之前这里漏了赋 Role，而引擎按 Role==container 筛容器，
	// 结果一级容器永远筛不到 → 所有出口都判 outside_container → 自动点击彻底失效。
	var realContainers []types.DetectionBox
	for _, b := range containers {
		if b.ClSName == types.ContainerClass {
			b.Role = types.RoleContainer
			if b.GatePassed == false {
				b.GatePassed = true // 容器本身不参与门控判定
			}
			realContainers = append(realContainers, b)
		}
	}

	result := &TwoStageResult{
		Containers: realContainers,
		Level1Ms:   l1ms,
	}

	if len(realContainers) == 0 {
		return result, nil
	}

	// 第二级：对每个容器做 ROI 裁切
	for _, c := range realContainers {
		// 容器框外扩 15%
		cw := c.Box[2] - c.Box[0]
		ch := c.Box[3] - c.Box[1]
		expand := 0.15
		rx1 := int(math.Max(0, c.Box[0]-cw*expand))
		ry1 := int(math.Max(0, c.Box[1]-ch*expand))
		rx2 := int(math.Min(float64(screenW), c.Box[2]+cw*expand))
		ry2 := int(math.Min(float64(screenH), c.Box[3]+ch*expand))

		roiRect := image.Rect(rx1, ry1, rx2, ry2)
		// 裁切 ROI 子图
		// 注意：image.Image 的 SubImage 不拷贝数据
		roi, ok := screenImg.(interface {
			SubImage(r image.Rectangle) image.Image
		})
		if !ok {
			// 如果不是 SubImage 接口，手动裁切
			continue
		}
		roiImg := roi.SubImage(roiRect)

		imgsz2 := d.Imgsz2(roiRect.Dx(), roiRect.Dy())
		roiOrigin := image.Pt(rx1, ry1)

		boxes, l2ms, err := d.DetectLevel2(roiImg, roiOrigin, imgsz2, screenW, screenH)
		if err != nil {
			continue
		}
		// 第二级只看出口：容器类必须真正丢弃。
		// 之前只改 Role 标签就把全部框塞进 Exits，导致同一个弹窗在
		// containers 里出现两次（一级一个 + 二级误检一个），
		// 污染 ROI 排序、弹窗计数与画布显示。
		for i := range boxes {
			if boxes[i].ClSName == types.ContainerClass {
				continue
			}
			boxes[i].Role = types.RoleExit
			result.Exits = append(result.Exits, boxes[i])
		}
		result.Level2Ms += l2ms
	}

	return result, nil
}

// ============================================================
// imgsz 基线公式 — PRD 4.3.4
// imgsz = clamp(ceil32(0.75 × 逻辑宽), 1024, 2048)
// ============================================================

// ComputeImgszBaseline 计算 profile 推荐的整屏 imgsz
func ComputeImgszBaseline(logicalW int) int {
	raw := int(float64(logicalW) * 0.75)
	ceil32 := ((raw + 31) / 32) * 32
	if ceil32 < 1024 {
		return 1024
	}
	if ceil32 > 2048 {
		return 2048
	}
	return ceil32
}

// ComputeImgsz2 计算第二级 imgsz = clamp(ceil32(max(crop_w, crop_h)), 768, 1280)
func ComputeImgsz2(cropW, cropH int) int {
	mx := cropW
	if cropH > mx {
		mx = cropH
	}
	ceil32 := ((mx + 31) / 32) * 32
	if ceil32 < 768 {
		return 768
	}
	if ceil32 > 1280 {
		return 1280
	}
	return ceil32
}

// ============================================================
// 物理像素 ↔ 逻辑点 坐标换算 — PRD 4.3.5
// ============================================================

// PxToPt 物理像素 → 逻辑点
func PxToPt(px float64, scaleFactor float64) float64 {
	return px / scaleFactor
}

// PtToPx 逻辑点 → 物理像素
func PtToPx(pt float64, scaleFactor float64) float64 {
	return pt * scaleFactor
}

// AbsInputCoord 模拟输入的归一化坐标
// macOS: CGEventPost 以全局坐标系（逻辑点）为单位
// Windows: SendInput 用 abs = int(x / 虚拟桌面宽 * 65535)
func AbsInputCoord(x float64, totalW int) int {
	return int(x / float64(totalW) * 65535)
}
