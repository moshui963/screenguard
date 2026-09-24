package selftest

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"time"

	"screenguard/internal/infer"
	"screenguard/internal/types"
)

// ============================================================
// 自检系统 — PRD 3.5 / 4.3.4
// 合成一张真值 100% 已知的假弹窗，走完整链路
// 自动得出 imgsz、哪些类别允许自动点 — 用户零输入
// ============================================================

// ProbeConfig 探针配置 — 合成弹窗的已知真值
type ProbeConfig struct {
	ScreenW      int // 屏幕物理像素宽
	ScreenH      int // 屏幕物理像素高
	PopupX1      int // 弹窗左上角 X
	PopupY1      int // 弹窗左上角 Y
	PopupX2      int // 弹窗右下角 X
	PopupY2      int // 弹窗右下角 Y
	CloseBtnX    int // 关闭按钮中心 X
	CloseBtnY    int // 关闭按钮中心 Y
	CloseBtnSize int // 关闭按钮尺寸 (px)
}

// DefaultProbe 默认探针配置 — 屏幕中央偏右的假弹窗
func DefaultProbe(screenW, screenH int) ProbeConfig {
	pw := screenW * 27 / 100 // 弹窗宽 ≈ 27% 屏宽
	ph := screenH * 35 / 100 // 弹窗高 ≈ 35% 屏高
	x1 := (screenW - pw) / 2
	y1 := (screenH - ph) / 2
	btnSize := 22
	if screenW > 3000 { // 5K 屏幕放大按钮
		btnSize = 32
	}
	return ProbeConfig{
		ScreenW:      screenW,
		ScreenH:      screenH,
		PopupX1:      x1,
		PopupY1:      y1,
		PopupX2:      x1 + pw,
		PopupY2:      y1 + ph,
		CloseBtnX:    x1 + pw - 36,
		CloseBtnY:    y1 + 12,
		CloseBtnSize: btnSize,
	}
}

// ProbeResult 单次探针结果
type ProbeResult struct {
	Imgsz          int
	ContainerFound bool
	ExitFound      bool
	ContainerBias  float64 // 容器中心偏差 (px)
	ExitBias       float64 // 出口中心偏差 (px)
	CaptureMs      int
	ModelMs        int
	Pass           bool // 偏差 < 3px 且两个都检出
}

// SelfTestResult 完整自检结果
type SelfTestResult struct {
	Profile     *types.DeviceProfile
	Results     []ProbeResult // 各 imgsz 档位
	Recommended int           // 推荐 imgsz
	AllPassed   bool
	Reason      string // 不达标原因
}

// Run 执行自检
// probeImg: 合成的探针图片
// detector: 推理器
// imgszCandidates: 待测 imgsz 列表（如 [640, 1024, 1280]）
func Run(probeImg image.Image, probe ProbeConfig, detector infer.Detector,
	imgszCandidates []int, modelSha256 string) (*SelfTestResult, error) {

	results := make([]ProbeResult, 0, len(imgszCandidates))
	var bestResult *ProbeResult

	for _, imgsz := range imgszCandidates {
		// 跑两级检测
		res, err := infer.TwoStageDetect(detector, probeImg, probe.ScreenW, probe.ScreenH, imgsz,
			map[string]float64{"TanChuang": 0.30, "GuanBi": 0.25},
			types.ClassNames)
		if err != nil {
			results = append(results, ProbeResult{Imgsz: imgsz, Pass: false})
			continue
		}

		// 与真值对比
		pr := ProbeResult{Imgsz: imgsz, CaptureMs: 0, ModelMs: res.Level1Ms + res.Level2Ms}

		// 检查容器
		for _, c := range res.Containers {
			cx := (c.Box[0] + c.Box[2]) / 2
			cy := (c.Box[1] + c.Box[3]) / 2
			gcx := float64(probe.PopupX1+probe.PopupX2) / 2
			gcy := float64(probe.PopupY1+probe.PopupY2) / 2
			bias := math.Hypot(float64(cx)-gcx, float64(cy)-gcy)
			if bias < pr.ContainerBias || pr.ContainerBias == 0 {
				pr.ContainerBias = bias
				pr.ContainerFound = true
			}
		}

		// 检查出口
		for _, e := range res.Exits {
			ex := e.Center[0]
			ey := e.Center[1]
			bias := math.Hypot(ex-float64(probe.CloseBtnX), ey-float64(probe.CloseBtnY))
			if bias < pr.ExitBias || pr.ExitBias == 0 {
				pr.ExitBias = bias
				pr.ExitFound = true
			}
		}

		pr.Pass = pr.ContainerFound && pr.ExitFound &&
			pr.ContainerBias < 3.0 && pr.ExitBias < 3.0

		results = append(results, pr)
		if pr.Pass && (bestResult == nil || pr.ModelMs < bestResult.ModelMs) {
			bestResult = &pr
		}
	}

	// 结论
	str := &SelfTestResult{Results: results}

	if bestResult != nil {
		str.Recommended = bestResult.Imgsz
		str.AllPassed = true
		// 所有类别设为各自策略模式
		str.Profile = buildProfile(bestResult, probe, modelSha256, "pass")
	} else {
		// 全档不达标 → 所有类别设为 report
		str.AllPassed = false
		str.Reason = "所有 imgsz 档位偏差均超过 3px，本机视觉条件不足"
		if len(results) > 0 {
			str.Recommended = results[len(results)-1].Imgsz
		}
		str.Profile = buildProfile(nil, probe, modelSha256, "fail")
	}

	return str, nil
}

func buildProfile(best *ProbeResult, probe ProbeConfig, modelSha256, status string) *types.DeviceProfile {
	p := &types.DeviceProfile{
		Fingerprint:    fingerprint(probe),
		Imgsz:          0,
		CaptureBackend: "ScreenCaptureKit",
		CaptureOK:      true,
		ModelSha256:    modelSha256,
		Status:         status,
	}

	// 逐类 profile
	for _, cls := range types.ClassNames {
		cp := types.ClassProfile{
			ClassName: cls,
			Mode:      types.PolicyReport, // 默认 report
		}
		if best != nil && status == "pass" {
			// 出口类 → 根据策略表决定
			if cls != types.ContainerClass {
				switch cls {
				case "GuanBi", "GuanBi_str", "ZhiDaoLe", "TiaoGuo", "WoBuShou", "QuXiao":
					cp.Mode = types.PolicyClick
				default:
					cp.Mode = types.PolicyReport
				}
			} else {
				cp.Mode = types.PolicyDisabled
			}
		}
		p.PerClass = append(p.PerClass, cp)
	}

	if best != nil {
		p.Imgsz = best.Imgsz
		p.BiasPx = best.ExitBias
		p.CaptureMs = best.CaptureMs
		p.ModelMs = best.ModelMs
	}
	p.CreatedAt = time.Now()
	return p
}

// fingerprint 生成设备指纹（屏幕分辨率+缩放比例）
func fingerprint(probe ProbeConfig) string {
	return fmt.Sprintf("%dx%d", probe.ScreenW, probe.ScreenH)
}

// DrawProbe 绘制合成探针图片
func DrawProbe(probe ProbeConfig) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, probe.ScreenW, probe.ScreenH))

	// 灰色桌面背景
	for y := 0; y < probe.ScreenH; y++ {
		for x := 0; x < probe.ScreenW; x++ {
			img.Set(x, y, color.RGBA{238, 238, 238, 255})
		}
	}

	// 白色弹窗框
	for y := probe.PopupY1; y <= probe.PopupY2; y++ {
		for x := probe.PopupX1; x <= probe.PopupX2; x++ {
			img.Set(x, y, color.RGBA{252, 252, 252, 255})
		}
	}
	// 边框
	for x := probe.PopupX1; x <= probe.PopupX2; x++ {
		img.Set(x, probe.PopupY1, color.RGBA{198, 198, 198, 255})
		img.Set(x, probe.PopupY2, color.RGBA{198, 198, 198, 255})
	}
	for y := probe.PopupY1; y <= probe.PopupY2; y++ {
		img.Set(probe.PopupX1, y, color.RGBA{198, 198, 198, 255})
		img.Set(probe.PopupX2, y, color.RGBA{198, 198, 198, 255})
	}

	// 关闭按钮 ×
	btn := probe.CloseBtnSize
	bx := probe.CloseBtnX - btn/2
	by := probe.CloseBtnY - btn/2
	// 灰色背景
	for y := by; y <= by+btn; y++ {
		for x := bx; x <= bx+btn; x++ {
			img.Set(x, y, color.RGBA{228, 228, 228, 255})
		}
	}
	// × 线条
	for i := 0; i < btn; i++ {
		img.Set(bx+i+5, by+i+5, color.RGBA{88, 88, 88, 255})
		img.Set(bx+btn-i-5, by+i+5, color.RGBA{88, 88, 88, 255})
	}

	return img
}

// SaveProbeImage 保存探针图片到临时文件
func SaveProbeImage(img image.Image) (string, error) {
	dir := os.TempDir()
	path := filepath.Join(dir, "screenguard_probe.png")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// 这里简化，实际需要 PNG 编码
	return path, f.Close()
}
