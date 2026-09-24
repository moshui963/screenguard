package infer

// ============================================================
// 推理层 — 纯 Go 公共实现（双平台共享，无 build tag）
// 本文件中的函数与 ONNX Runtime 的 C 绑定无关，仅做 tensor 解析 / 坐标
// 逆变换 / 去重 / NMS，因此 macOS(darwin) 与 Windows 共用同一份实现，
// 避免重复实现导致的坐标或阈值漂移（用户偏好：不重复造轮子）。
// 改动这里会同时影响两个平台，需同步回归验证。
// ============================================================

import (
	"image"
	"math"

	"screenguard/internal/types"
)

// preprocessImage 图像预处理: letterbox + RGB → BGR + NCHW float32
func preprocessImage(img image.Image, targetW, targetH int) []float32 {
	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	r := math.Min(float64(targetW)/float64(origW), float64(targetH)/float64(origH))
	newW := int(math.Round(float64(origW) * r))
	newH := int(math.Round(float64(origH) * r))

	inputData := make([]float32, 3*targetW*targetH)

	padW := (targetW - newW) / 2
	padH := (targetH - newH) / 2

	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			srcX := int(float64(x) / r)
			srcY := int(float64(y) / r)
			if srcX >= origW {
				srcX = origW - 1
			}
			if srcY >= origH {
				srcY = origH - 1
			}

			r32, g32, b32, _ := img.At(bounds.Min.X+srcX, bounds.Min.Y+srcY).RGBA()
			r8 := float32(r32>>8) / 255.0
			g8 := float32(g32>>8) / 255.0
			b8 := float32(b32>>8) / 255.0

			dx := x + padW
			dy := y + padH

			// NCHW, BGR
			inputData[(0*targetH+dy)*targetW+dx] = b8
			inputData[(1*targetH+dy)*targetW+dx] = g8
			inputData[(2*targetH+dy)*targetW+dx] = r8
		}
	}

	return inputData
}

// postprocess 后处理: 解析 YOLO 输出 → 过滤 → NMS → 坐标逆变换
// postprocess 后处理 — 支持两种输出格式（红线 P0-3/P0-4）
//
// 格式 A（本模型）: YOLO26 端到端导出，shape=[B, 300, 6]
//
//	每行 = [x1, y1, x2, y2, conf, cls]，已含 NMS，直接解析。
//	端到端输出仍可能同目标多框，保留同类几何去重（IoU>0.30 保最高分）。
//
// 格式 B（兜底）: 老 YOLOv5 anchor 格式 [B, N, 5+classes]
//
//	每行 = [cx, cy, w, h, objConf, clsScores...]。
func postprocess(output []float32, numClasses, inputW, inputH int, lb LetterboxParams, origin image.Point) []types.DetectionBox {
	confThreshold := 0.25
	var candidates []types.DetectionBox

	appendBox := func(x1, y1, x2, y2, conf float64, cls int) {
		if conf < confThreshold {
			return
		}
		origBox := lb.ModelToOriginal(x1, y1, x2, y2)
		clsName := ""
		if cls >= 0 && cls < len(types.ClassNames) {
			clsName = types.ClassNames[cls]
		}
		box := types.DetectionBox{
			ClS:     cls,
			ClSName: clsName,
			Conf:    conf,
			Box: types.BoxXYXY{
				origBox[0] + float64(origin.X),
				origBox[1] + float64(origin.Y),
				origBox[2] + float64(origin.X),
				origBox[3] + float64(origin.Y),
			},
			Center: types.Center{
				(origBox[0]+origBox[2])/2 + float64(origin.X),
				(origBox[1]+origBox[3])/2 + float64(origin.Y),
			},
		}
		box.GatePassed = true
		candidates = append(candidates, box)
	}

	// 判别格式：端到端导出最后一维 == 6 且输出元素总数可被 6 整除
	total := len(output)
	isEnd2End := false
	if total > 0 && total%6 == 0 {
		// 检查推断的检测数是否合理（端到端 top-300 量级）
		n := total / 6
		if n <= 2000 {
			isEnd2End = true
			// 进一步验证：前几个候选的 conf 是否落在 [0,1] 且 cls 合法
			probeOK := 0
			for i := 0; i < n && i < 8; i++ {
				c := float64(output[i*6+4])
				cl := int(output[i*6+5])
				if c >= 0 && c <= 1.0 && cl >= 0 && cl < 32 {
					probeOK++
				}
			}
			isEnd2End = probeOK >= 6 // 大部分像端到端格式
		}
	}

	if isEnd2End {
		// 格式 A: [B, 300, 6] = [x1,y1,x2,y2,conf,cls]
		n := total / 6
		for i := 0; i < n; i++ {
			o := i * 6
			conf := float64(output[o+4])
			if conf < confThreshold {
				continue
			}
			cls := int(output[o+5])
			appendBox(float64(output[o+0]), float64(output[o+1]),
				float64(output[o+2]), float64(output[o+3]), conf, cls)
		}
		// 端到端输出已是 top-300 唯一框，只做同类几何去重（红线 P0-3）
		return dedupeByClass(candidates, 0.30)
	}

	// 格式 B: anchor 格式
	stride := 5 + numClasses
	if stride <= 5 {
		stride = 15
	}
	numDetections := total / stride
	for i := 0; i < numDetections; i++ {
		offset := i * stride
		objConf := float64(output[offset+4])
		if objConf < confThreshold {
			continue
		}
		maxCls := 0
		maxClsScore := float64(output[offset+5])
		for c := 1; c < numClasses; c++ {
			score := float64(output[offset+5+c])
			if score > maxClsScore {
				maxClsScore = score
				maxCls = c
			}
		}
		conf := objConf * maxClsScore
		if conf < confThreshold {
			continue
		}
		cx := float64(output[offset+0])
		cy := float64(output[offset+1])
		w := float64(output[offset+2])
		h := float64(output[offset+3])
		appendBox(cx-w/2.0, cy-h/2.0, cx+w/2.0, cy+h/2.0, conf, maxCls)
	}
	// anchor 格式必须 NMS
	return nmsByClass(candidates, 0.45)
}

// dedupeByClass 同类几何去重（红线 P0-3: 端到端模型实测也会重复框）
// 只在同类内合并 IoU>thr 的重复框保留最高分，不做跨类抑制
func dedupeByClass(boxes []types.DetectionBox, thr float64) []types.DetectionBox {
	var kept []types.DetectionBox
	// 按置信度降序
	for i := 0; i < len(boxes); i++ {
		for j := i + 1; j < len(boxes); j++ {
			if boxes[j].Conf > boxes[i].Conf {
				boxes[i], boxes[j] = boxes[j], boxes[i]
			}
		}
	}
	for _, b := range boxes {
		dup := false
		for _, k := range kept {
			if k.ClS == b.ClS && iou(b.Box, k.Box) > thr {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, b)
		}
	}
	return kept
}

// nmsByClass 逐类 NMS
func nmsByClass(boxes []types.DetectionBox, iouThreshold float64) []types.DetectionBox {
	byClass := make(map[string][]types.DetectionBox)
	for _, b := range boxes {
		byClass[b.ClSName] = append(byClass[b.ClSName], b)
	}

	var result []types.DetectionBox
	for _, classBoxes := range byClass {
		sortBoxesByConf(classBoxes)

		kept := make([]types.DetectionBox, 0, len(classBoxes))
		for _, b := range classBoxes {
			suppressed := false
			for _, k := range kept {
				if iou(b.Box, k.Box) > iouThreshold {
					suppressed = true
					break
				}
			}
			if !suppressed {
				kept = append(kept, b)
			}
		}
		result = append(result, kept...)
	}
	return result
}

// sortBoxesByConf 按置信度降序排序
func sortBoxesByConf(boxes []types.DetectionBox) {
	for i := 0; i < len(boxes); i++ {
		for j := i + 1; j < len(boxes); j++ {
			if boxes[j].Conf > boxes[i].Conf {
				boxes[i], boxes[j] = boxes[j], boxes[i]
			}
		}
	}
}

// iou 计算两个框的 IoU
func iou(a, b types.BoxXYXY) float64 {
	x1 := math.Max(a[0], b[0])
	y1 := math.Max(a[1], b[1])
	x2 := math.Min(a[2], b[2])
	y2 := math.Min(a[3], b[3])

	interW := math.Max(0, x2-x1)
	interH := math.Max(0, y2-y1)
	inter := interW * interH

	areaA := math.Max(0, a[2]-a[0]) * math.Max(0, a[3]-a[1])
	areaB := math.Max(0, b[2]-b[0]) * math.Max(0, b[3]-b[1])
	union := areaA + areaB - inter

	if union <= 0 {
		return 0
	}
	return inter / union
}
