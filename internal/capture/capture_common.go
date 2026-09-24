package capture

// ============================================================
// 抓屏层公共纯逻辑（双平台共享，无 build tag）
// 把不依赖具体系统 API 的判定逻辑放这里，便于在交叉编译环境跑单测，
// 也避免 macOS / Windows 各自实现导致语义漂移。
// ============================================================

import "image"

// IsBlackFrame 检测黑帧（锁屏 / 安全桌面 / DRM 受保护内容）
// PRD 第6章：抓屏返回黑帧应判定该区域不可监控，跳过并记一次，不重复刷日志。
// 纯逻辑：统计采样点中非黑像素数量，超过 2 个即视为"有内容"返回 false。
// 空图 / 空 bounds 直接视为黑帧（true）。
func IsBlackFrame(img image.Image, sampleStep int) bool {
	if img == nil {
		return true
	}
	b := img.Bounds()
	if b.Empty() {
		return true
	}
	if sampleStep <= 0 {
		sampleStep = 32
	}
	nonBlack := 0
	for y := b.Min.Y; y < b.Max.Y; y += sampleStep {
		for x := b.Min.X; x < b.Max.X; x += sampleStep {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r|g|bl != 0 {
				nonBlack++
				// 保守策略：只要采样到 1 个非黑像素，就认为"有内容"，
				// 不判为黑帧。避免锁屏界面一个光标/时钟被漏判为黑帧而跳过监控。
				if nonBlack >= 1 {
					return false
				}
			}
		}
	}
	return nonBlack == 0
}
