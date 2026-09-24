// frameprobe — 离线帧诊断工具（不需要屏幕录制权限）
//
// 用途：把一张真实截图喂进与线上完全相同的两级检测 + 坐标换算链路，
// 输出每个框在屏幕空间和模型空间的尺寸，用来回答那个最关键的问题：
//
//	"这个 × 在模型输入里到底还剩几个像素？"
//
// 这条链路和 app 内 FramePusher 共用 BuildFramePayload，
// 所以这里的数字就是调试台画布上看到的数字，不会两套口径。
//
// 用法：
//
//	go run ./cmd/frameprobe -model tangchuang/yolo26m.onnx -img shot.png
//	go run ./cmd/frameprobe -model ... -img shot.png -imgsz 1280 -out probe_out
//	go run ./cmd/frameprobe -model ... -img shot.png -csv boxes.csv
//
// 退出码：0 成功；2 参数/加载错误；3 检测失败
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strconv"

	"screenguard/internal/app"
	"screenguard/internal/engine"
	"screenguard/internal/infer"
	"screenguard/internal/policy"
	"screenguard/internal/types"
)

// joinShort 把逐帧动作拼成一行，相同动作折叠计数
func joinShort(parts []string) string {
	if len(parts) == 0 {
		return "(无)"
	}
	out := parts[0]
	prev := parts[0]
	run := 1
	for _, p := range parts[1:] {
		if p == prev {
			run++
			continue
		}
		if run > 1 {
			out += fmt.Sprintf(" ×%d", run)
		}
		out += " → " + p
		prev = p
		run = 1
	}
	if run > 1 {
		out += fmt.Sprintf(" ×%d", run)
	}
	return out
}

func main() {
	modelPath := flag.String("model", "tangchuang/yolo26m.onnx", "ONNX 模型路径")
	imgPath := flag.String("img", "", "待检测截图（必填）")
	imgsz := flag.Int("imgsz", 0, "整屏 imgsz；0 = 按基线公式 0.75×图像宽")
	threads := flag.Int("threads", 4, "推理线程数")
	outDir := flag.String("out", "", "把 payload 的 JPEG 与模型输入图写到该目录")
	csvPath := flag.String("csv", "", "把框导出成 CSV")
	confThr := flag.Float64("conf", 0.25, "最低置信度")
	policyPath := flag.String("policy", "configs/policy.yaml", "策略文件（不存在则用内置默认）")
	allowAuto := flag.Bool("auto", false, "以自动模式跑决策链（默认观察模式）")
	idleSec := flag.Float64("idle", 999, "模拟的用户空闲秒数（<1.5 会命中用户活跃拦截）")
	skipEngine := flag.Bool("no-engine", false, "只跑检测，不跑决策链")
	frames := flag.Int("frames", 1, "模拟连续帧数（4 帧才可能满足一致性门控并触发点击）")
	flag.Parse()

	if *imgPath == "" {
		fmt.Fprintln(os.Stderr, "必须指定 -img")
		flag.Usage()
		os.Exit(2)
	}

	f, err := os.Open(*imgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开图片失败: %v\n", err)
		os.Exit(2)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解码图片失败: %v\n", err)
		os.Exit(2)
	}
	b := src.Bounds()
	W, H := b.Dx(), b.Dy()

	size := *imgsz
	if size == 0 {
		size = infer.ComputeImgszBaseline(W)
	}

	det, err := infer.NewONNXDetector(*modelPath, *threads)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载模型失败: %v\n", err)
		os.Exit(2)
	}
	defer det.Close()
	if m := det.ModelInputSize(); m > 0 && m != size {
		fmt.Printf("[提示] 模型为静态输入 %d，已覆盖 imgsz %d\n", m, size)
		size = m
	}

	confMap := map[string]float64{}
	for _, n := range types.ClassNames {
		confMap[n] = *confThr
	}

	res, err := infer.TwoStageDetect(det, src, W, H, size, confMap, types.ClassNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "检测失败: %v\n", err)
		os.Exit(3)
	}

	lb := infer.ComputeLetterbox(W, H, size)
	payload := app.NewFramePayload(src, lb, size, res.Containers, res.Exits, nil,
		0, res.Level1Ms+res.Level2Ms, W, H)
	if payload == nil {
		fmt.Fprintln(os.Stderr, "payload 为空")
		os.Exit(3)
	}

	fmt.Printf("图片      : %dx%d px\n", W, H)
	fmt.Printf("imgsz     : %d  (基线公式 0.75×%d = %d)\n", size, W, infer.ComputeImgszBaseline(W))
	fmt.Printf("letterbox : ratio=%.4f  newShape=%dx%d  pad=(%.1f, %.1f)\n",
		lb.Ratio[0], lb.NewShape[0], lb.NewShape[1], lb.Dw, lb.Dh)
	fmt.Printf("检出      : 容器 %d 个 / 出口 %d 个\n\n", len(res.Containers), len(res.Exits))

	// 关键表：屏幕尺寸 vs 模型尺寸
	fmt.Printf("%-12s %-6s %-16s %-16s %-10s %s\n",
		"类别", "conf", "屏幕框(宽x高)", "模型框(宽x高)", "短边px", "判定")
	fmt.Println("-------------------------------------------------------------------------------")
	rows := [][]string{{"cls_name", "conf", "screen_w", "screen_h", "model_w", "model_h", "model_short", "verdict"}}
	for _, box := range payload.Boxes {
		sw := box.Box[2] - box.Box[0]
		sh := box.Box[3] - box.Box[1]
		mw := box.BoxModel[2] - box.BoxModel[0]
		mh := box.BoxModel[3] - box.BoxModel[1]
		short := mw
		if mh < short {
			short = mh
		}
		verdict := "OK"
		switch {
		case short < 8:
			verdict = "过小·基本不可用"
		case short < 12:
			verdict = "临界·需多帧补偿"
		}
		fmt.Printf("%-12s %-6.2f %-16s %-16s %-10.1f %s\n",
			box.ClSName, box.Conf,
			fmt.Sprintf("%.0fx%.0f", sw, sh),
			fmt.Sprintf("%.1fx%.1f", mw, mh),
			short, verdict)
		rows = append(rows, []string{
			box.ClSName, strconv.FormatFloat(box.Conf, 'f', 3, 64),
			strconv.FormatFloat(sw, 'f', 1, 64), strconv.FormatFloat(sh, 'f', 1, 64),
			strconv.FormatFloat(mw, 'f', 1, 64), strconv.FormatFloat(mh, 'f', 1, 64),
			strconv.FormatFloat(short, 'f', 1, 64), verdict,
		})
	}

	// 与 popup_infer.py 对拍用的坐标输出
	fmt.Printf("\n出口中心（屏幕像素 / 模型像素）：\n")
	for _, box := range payload.Boxes {
		if box.Role == string(types.RoleContainer) {
			continue
		}
		fmt.Printf("  %-12s screen=(%.1f, %.1f)  model=(%.1f, %.1f)\n",
			box.ClSName, box.Center[0], box.Center[1],
			(box.BoxModel[0]+box.BoxModel[2])/2, (box.BoxModel[1]+box.BoxModel[3])/2)
	}

	if *outDir != "" {
		os.MkdirAll(*outDir, 0o755)
		jpgPath := filepath.Join(*outDir, "screen.jpg")
		if data, err := app.DecodeB64ToBytes(payload.JpegB64); err == nil {
			os.WriteFile(jpgPath, data, 0o644)
			fmt.Printf("\n屏幕 JPEG → %s (%dx%d)\n", jpgPath, payload.ImgW, payload.ImgH)
		}
		if payload.ROI != nil && payload.ROI.JpegB64 != "" {
			if data, err := app.DecodeB64ToBytes(payload.ROI.JpegB64); err == nil {
				roiPath := filepath.Join(*outDir, "roi.jpg")
				os.WriteFile(roiPath, data, 0o644)
				fmt.Printf("ROI JPEG   → %s (%dx%d, imgsz2=%d)\n", roiPath, payload.ROI.W, payload.ROI.H, payload.ROI.Imgsz2)
			}
		}
	}

	// ============ 决策链模拟（与线上同一套 Engine，含容器约束）============
	if !*skipEngine {
		cfg := types.DefaultConfig()
		cfg.Mode = types.ModeObserve
		if *allowAuto {
			cfg.Mode = types.ModeAuto
		}
		eng := engine.NewEngine(cfg)
		if pm := policy.NewManager(*policyPath); pm != nil {
			if snap, err := pm.LoadAndApply(); err == nil {
				eng.SetPolicy(snap)
			} else {
				fmt.Printf("[提示] 策略加载失败，用内置默认: %v\n", err)
			}
		}
		// 注入模拟空闲值，避免"没人喂空闲函数"导致保守拦截
		eng.SetIdleFunc(func() float64 { return *idleSec })

		merged := append([]types.DetectionBox{}, res.Containers...)
		merged = append(merged, res.Exits...)

		n := *frames
		if n < 1 {
			n = 1
		}
		fmt.Printf("\n决策链模拟（模式=%s, 空闲=%.0fs, 一致性需 %d 帧, 本次投 %d 帧）：\n",
			cfg.Mode, *idleSec, types.DefaultConfig().FrameConsistencyN, n)

		var ev *types.DetectionEvent
		trace := []string{}
		lastFrame := merged
		for i := 0; i < n; i++ {
			// 每帧重新克隆 boxes，避免上一帧对 GatePassed/RejectReason 的改写串味
			frameBoxes := make([]types.DetectionBox, len(merged))
			copy(frameBoxes, merged)
			lastFrame = frameBoxes
			ev = eng.Decide(frameBoxes, W, H, 0, res.Level1Ms+res.Level2Ms)
			if ev != nil {
				trace = append(trace, fmt.Sprintf("f%d=%s", i+1, ev.Action))
			}
		}
		fmt.Printf("  逐帧动作 : %s\n", joinShort(trace))
		fmt.Printf("  最终动作 : %s\n", ev.Action)
		fmt.Printf("  执行结果 : %s\n", ev.Result)
		if ev.Action == types.ActionReportOnly && n < types.DefaultConfig().FrameConsistencyN {
			fmt.Printf("  说明     : 单帧必然停在 report_only —— 一致性门控要求连续 %d 帧。"+
				"加 -frames %d 可验证点击路径是否真能触发\n",
				types.DefaultConfig().FrameConsistencyN, types.DefaultConfig().FrameConsistencyN)
		}
		if ev.Action == types.ActionClick {
			fmt.Printf("  ✅ 点击路径已打通：容器门控与一致性门控均通过\n")
		}
		fmt.Println("  末帧逐出口门控（引擎实际判定结果）：")
		for _, b := range lastFrame {
			if b.Role != types.RoleExit {
				continue
			}
			status := "✓ 通过"
			if !b.GatePassed {
				status = "✗ " + b.RejectReason
			}
			fmt.Printf("    %-12s conf=%.2f 中心=(%.0f,%.0f)  %s\n",
				b.ClSName, b.Conf, b.Center[0], b.Center[1], status)
		}
		fmt.Printf("  容器数=%d（为 0 说明一级失效，所有出口必被判 outside_container）\n",
			len(res.Containers))
	}

	if *csvPath != "" {
		fOut, err := os.Create(*csvPath)
		if err == nil {
			w := csv.NewWriter(fOut)
			w.WriteAll(rows)
			fOut.Close()
			fmt.Printf("CSV        → %s\n", *csvPath)
		}
	}
}
