//go:build windows

package win

import "screenguard/internal/capture"

// NewCapturer 创建 Windows 抓屏器（与 platform/mac/factory_darwin.go 对称）
func NewCapturer() capture.Capturer {
	return capture.NewGDICapturer()
}
