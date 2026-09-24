//go:build windows

package main

import (
	"screenguard/internal/capture"
	"screenguard/internal/platform/win"
)

// initPlatform Windows 必须在创建任何窗口之前声明 DPI 感知，
// 否则抓屏拿到的坐标是系统 DPI 虚拟化后的逻辑像素，与 PRD 4.3.5
// "全链路物理像素" 的红线冲突，点击会整体偏移。
func initPlatform() {
	capture.InitDPIAware()
}

// newPlatformCapturer Windows 抓屏器（GDI BitBlt，纯 Go syscall，无 cgo）
func newPlatformCapturer() capture.Capturer {
	return win.NewCapturer()
}

// platformIdleSeconds Windows 用户空闲时长（GetLastInputInfo + GetTickCount）
func platformIdleSeconds() float64 {
	return win.SecondsSinceLastUserInput()
}

// platformForbiddenZone Windows 禁区判定（PRD 4.3.1 第 1 步）。
// Windows 没有 macOS 的 TCC 授权机制，禁区判定是唯一的安全边界，
// 覆盖：全屏应用 / 锁屏 / 屏保 / 远程桌面 / UAC 安全桌面 / 系统安装器 / 低电量。
func platformForbiddenZone() func() (bool, string) {
	return func() (bool, string) {
		st := win.CheckForbiddenZones()
		return st.Any(), st.Reason()
	}
}

// warnBundleIdentity Windows 没有 TCC 身份机制，exe 本身就是身份，无需自检。
// 但安装到 Program Files 时目录不可写，这里提示数据目录已落在用户配置目录。
func warnBundleIdentity() {
	// Windows 的授权与路径在 internal/paths/paths_windows.go 已处理，无需额外提示
}
