package capture

import (
	"fmt"
	"image"
)

// ============================================================
// 抓屏抽象接口 — PRD 附录A 技术栈
// macOS: ScreenCaptureKit (SCStream, 12.3+/13+)
// Windows (v1.1): C# WGC sidecar
// ============================================================

// Capturer 抓屏接口
type Capturer interface {
	// Capture 截取全屏，返回物理像素的 image.Image
	Capture() (img image.Image, captureMs int, err error)
	// ScreenSize 获取主屏物理像素尺寸
	ScreenSize() (w, h int, scaleFactor float64, err error)
	// ExcludeWindow 排除指定窗口（PRD 4.3.7 自激防护）
	ExcludeWindow(windowID int) error
	// ExcludeSelfWindows 排除当前进程的全部顶层窗口（PRD 4.3.7，自身全部窗口）
	ExcludeSelfWindows() error
	// IsAuthorized 检查是否有屏幕录制权限
	IsAuthorized() bool
}

// FrameDiff 帧差检测 — PRD 4.3.6 帧差门控
// 返回差异比例 [0, 1]，低于阈值的区域不推理
func FrameDiff(a, b image.Image, threshold int) (float64, error) {
	if a == nil || b == nil {
		return 1.0, nil // 无前帧则认为全变
	}
	bounds := a.Bounds()
	if b.Bounds() != bounds {
		return 1.0, nil
	}
	var changed, total int64
	// 采样间隔（每 4 像素取 1 个，降低 CPU）
	step := 4
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			dr := int(ar>>8) - int(br>>8)
			dg := int(ag>>8) - int(bg>>8)
			db := int(ab>>8) - int(bb>>8)
			if abs(dr) > threshold || abs(dg) > threshold || abs(db) > threshold {
				changed++
			}
			total++
		}
	}
	if total == 0 {
		return 1.0, nil
	}
	return float64(changed) / float64(total), nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// StubCapturer 占位实现（非 macOS 平台或权限缺失时使用）
type StubCapturer struct{}

func (s *StubCapturer) Capture() (image.Image, int, error) {
	return nil, 0, fmt.Errorf("抓屏器不可用（非 macOS 或权限缺失）")
}
func (s *StubCapturer) ScreenSize() (int, int, float64, error) {
	return 0, 0, 1.0, fmt.Errorf("不可用")
}
func (s *StubCapturer) ExcludeWindow(windowID int) error { return nil }
func (s *StubCapturer) ExcludeSelfWindows() error        { return nil }
func (s *StubCapturer) IsAuthorized() bool               { return false }
