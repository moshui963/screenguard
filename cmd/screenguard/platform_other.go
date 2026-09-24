//go:build !darwin && !windows

package main

import (
	"screenguard/internal/capture"
)

// initPlatform 其他平台无 DPI 初始化需求
func initPlatform() {}

// newPlatformCapturer 其他平台使用占位抓屏器（不产出真实帧，仅保证可编译）
func newPlatformCapturer() capture.Capturer {
	return &capture.StubCapturer{}
}

// platformIdleSeconds 其他平台无法获取真实空闲时长。
// 返回 0 = "用户刚刚操作过"，引擎会判定 blocked_user_active 而不点击，
// 符合 PRD 附录 A 工程约束 3：不确定就不点。
func platformIdleSeconds() float64 {
	return 0
}

// platformForbiddenZone 其他平台无禁区实现
func platformForbiddenZone() func() (bool, string) {
	return nil
}

// warnBundleIdentity 其他平台无需身份自检
func warnBundleIdentity() {}
